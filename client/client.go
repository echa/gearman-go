// The client package helps developers connect to Gearmand, send
// jobs and fetch result.
package client

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"
)

var (
	DefaultResponseTimeout time.Duration = 10 * time.Second
	DefaultDialTimeout     time.Duration = 10 * time.Second
	DefaultKeepAlive       time.Duration = 30 * time.Second
)

// One client connect to one server.
// Use Pool for multi-connections.
type Client struct {
	sync.Mutex

	net, addr, lastcall string
	respHandler         *responseHandlerMap
	innerHandler        *responseHandlerMap
	in                  chan *Response
	conn                net.Conn
	rw                  *bufio.ReadWriter

	ResponseTimeout time.Duration // response timeout for do() in ms
	tlsConfig       *tls.Config

	ErrorHandler ErrorHandler
}

type responseHandlerMap struct {
	sync.RWMutex
	holder map[string]ResponseHandler
}

func newResponseHandlerMap() *responseHandlerMap {
	return &responseHandlerMap{holder: make(map[string]ResponseHandler, queueSize)}
}

func (r *responseHandlerMap) remove(key string) {
	r.Lock()
	delete(r.holder, key)
	r.Unlock()
}

func (r *responseHandlerMap) get(key string) (ResponseHandler, bool) {
	r.RLock()
	rh, b := r.holder[key]
	r.RUnlock()
	return rh, b
}
func (r *responseHandlerMap) put(key string, rh ResponseHandler) {
	r.Lock()
	r.holder[key] = rh
	r.Unlock()
}

// Return a client.
func New(network, addr string, tlsConfig *tls.Config) (client *Client) {
	client = &Client{
		net:             network,
		addr:            addr,
		respHandler:     newResponseHandlerMap(),
		innerHandler:    newResponseHandlerMap(),
		in:              make(chan *Response, queueSize),
		ResponseTimeout: DefaultResponseTimeout,
		tlsConfig:       tlsConfig,
	}
	// if err = client.dial(); err != nil {
	// 	return
	// }
	// go client.readLoop()
	// go client.processLoop()
	return
}

func (c *Client) Connect() error {
	c.Lock()
	defer c.Unlock()
	if err := c.connect(); err != nil {
		return err
	}
	c.work()
	return nil
}

func (c *client) work() {
	go c.readLoop()
	go c.processLoop()
}

func (c *Client) connect() (err error) {
	dialer := &net.Dialer{
		Timeout:   DefaultDialTimeout,
		KeepAlive: DefaultKeepAlive,
	}
	if client.tlsConfig != nil {
		client.conn, err = tls.DialWithDialer(dialer, client.net, client.addr, client.tlsConfig)
	} else {
		client.conn, err = dialer.Dial(client.net, client.addr)
	}
	if err != nil {
		return
	}
	client.rw = bufio.NewReadWriter(bufio.NewReader(client.conn),
		bufio.NewWriter(client.conn))
	return
}

func (client *Client) write(req *request) (err error) {
	var n int
	buf := req.Encode()
	for i := 0; i < len(buf); i += n {
		n, err = client.rw.Write(buf[i:])
		if err != nil {
			return
		}
	}
	return client.rw.Flush()
}

func (client *Client) read(length int) (data []byte, err error) {
	n := 0
	buf := getBuffer(bufferSize)
	// read until data can be unpacked
	for i := length; i > 0 || len(data) < minPacketLength; i -= n {
		if n, err = client.rw.Read(buf); err != nil {
			return
		}
		data = append(data, buf[0:n]...)
		if n < bufferSize {
			break
		}
	}
	return
}

func (client *Client) readLoop() {
	defer close(client.in)
	var data, leftdata []byte
	var err error
	var resp *Response
ReadLoop:
	for client.conn != nil {
		if data, err = client.read(bufferSize); err != nil {
			if opErr, ok := err.(*net.OpError); ok {
				if opErr.Temporary() {
					continue
				} else {
					client.disconnect_error(err)
					break
				}
			} else if err == io.EOF {
				client.disconnect_error(err)
				break
			}
			client.err(err)
			// If it is unexpected error and the connection wasn't
			// closed by Gearmand, the client should close the connection
			// and reconnect to job server.
			client.Close()
			if err = client.connect(); err != nil {
				client.disconnect_error(err)
				break
			}
			continue
		}
		if len(leftdata) > 0 { // some data left for processing
			data = append(leftdata, data...)
			leftdata = nil
		}
		for {
			l := len(data)
			if l < minPacketLength { // not enough data
				leftdata = data
				continue ReadLoop
			}
			if resp, l, err = decodeResponse(data); err != nil {
				leftdata = data[l:]
				continue ReadLoop
			} else {
				client.in <- resp
			}
			data = data[l:]
			if len(data) > 0 {
				continue
			}
			break
		}
	}
}

func (c *client) disconnect_error(err error) {
	if c.conn != nil {
		err = &DisconnectError{
			err:    err,
			client: c,
		}
		c.err(err)
	}
}

func (client *Client) processLoop() {
	for resp := range client.in {
		switch resp.DataType {
		case dtError:
			if client.lastcall != "" {
				resp = client.handleInner(client.lastcall, resp)
				client.lastcall = ""
			} else {
				client.err(getError(resp.Data))
			}
		case dtStatusRes:
			resp = client.handleInner("s"+resp.Handle, resp)
		case dtJobCreated:
			resp = client.handleInner("c", resp)
		case dtEchoRes:
			resp = client.handleInner("e", resp)
		case dtWorkData, dtWorkWarning, dtWorkStatus:
			resp = client.handleResponse(resp.Handle, resp)
		case dtWorkComplete, dtWorkFail, dtWorkException:
			client.handleResponse(resp.Handle, resp)
			client.respHandler.remove(resp.Handle)
		}
	}
}

func (client *Client) err(e error) {
	if client.ErrorHandler != nil {
		client.ErrorHandler(e)
	}
}

func (client *Client) handleResponse(key string, resp *Response) *Response {
	if h, ok := client.respHandler.get(key); ok {
		h(resp)
		return nil
	}
	return resp
}

func (client *Client) handleInner(key string, resp *Response) *Response {
	if h, ok := client.innerHandler.get(key); ok {
		h(resp)
		client.innerHandler.remove(key)
		return nil
	}
	return resp
}

type handleOrError struct {
	handle string
	err    error
}

func (client *Client) do(ctx context.Context, funcname string, data []byte,
	flag uint32) (handle string, err error) {
	if client.conn == nil {
		return "", ErrLostConn
	}
	var result = make(chan handleOrError, 1)
	client.lastcall = "c"
	client.innerHandler.put("c", func(resp *Response) {
		if resp.DataType == dtError {
			err = getError(resp.Data)
			result <- handleOrError{"", err}
			return
		}
		handle = resp.Handle
		result <- handleOrError{handle, nil}
	})
	id := IdGen.Id()
	req := getJob(id, []byte(funcname), data)
	req.DataType = flag
	if err = client.write(req); err != nil {
		client.innerHandler.remove("c")
		client.lastcall = ""
		return
	}
	var (
		callCtx context.Context
		cancel  context.CancelFunc
	)
	if client.ResponseTimeout > 0 {
		// The request has a timeout, so create a context that is
		// canceled automatically when the timeout expires.
		callCtx, cancel = context.WithTimeout(ctx, client.ResponseTimeout)
	} else {
		callCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	select {
	case ret := <-result:
		return ret.handle, ret.err
	case <-callCtx.Done():
		client.innerHandler.remove("c")
		client.lastcall = ""
		return "", ctx.Err()
	}
	return
}

// Call the function and get a response.
// flag can be set to: JobLow, JobNormal and JobHigh
func (client *Client) Do(ctx context.Context, funcname string, data []byte,
	flag byte, h ResponseHandler) (handle string, err error) {
	var datatype uint32
	switch flag {
	case JobLow:
		datatype = dtSubmitJobLow
	case JobHigh:
		datatype = dtSubmitJobHigh
	default:
		datatype = dtSubmitJob
	}
	handle, err = client.do(ctx, funcname, data, datatype)
	if err == nil && h != nil {
		client.respHandler.put(handle, h)
	}
	return
}

// Call the function in background, no response needed.
// flag can be set to: JobLow, JobNormal and JobHigh
func (client *Client) DoBg(ctx context.Context, funcname string, data []byte,
	flag byte) (handle string, err error) {
	if client.conn == nil {
		return "", ErrLostConn
	}
	var datatype uint32
	switch flag {
	case JobLow:
		datatype = dtSubmitJobLowBg
	case JobHigh:
		datatype = dtSubmitJobHighBg
	default:
		datatype = dtSubmitJobBg
	}
	handle, err = client.do(ctx, funcname, data, datatype)
	return
}

// Get job status from job server.
func (client *Client) Status(ctx context.Context, handle string) (status *Status, err error) {
	if client.conn == nil {
		return nil, ErrLostConn
	}
	done := make(chan bool, 1)
	client.lastcall = "s" + handle
	client.innerHandler.put("s"+handle, func(resp *Response) {
		defer close(done)
		var err error
		status, err = resp._status()
		if err != nil {
			client.err(err)
		}
	})
	req := getRequest()
	req.DataType = dtGetStatus
	req.Data = []byte(handle)
	if err = client.write(req); err != nil {
		client.innerHandler.remove("s" + handle)
		client.lastcall = ""
		return
	}

	var (
		callCtx context.Context
		cancel  context.CancelFunc
	)
	if client.ResponseTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, client.ResponseTimeout)
	} else {
		callCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	select {
	case <-callCtx.Done():
		return nil, callCtx.Err()
	case <-done:
		return
	}
	return
}

// Echo.
func (client *Client) Echo(ctx context.Context, data []byte) (echo []byte, err error) {
	if client.conn == nil {
		return nil, ErrLostConn
	}
	done := make(chan bool, 1)
	client.innerHandler.put("e", func(resp *Response) {
		defer close(done)
		echo = resp.Data
	})
	req := getRequest()
	req.DataType = dtEchoReq
	req.Data = data
	client.lastcall = "e"
	if err = client.write(req); err != nil {
		client.innerHandler.remove("e")
		client.lastcall = ""
		return
	}
	var (
		callCtx context.Context
		cancel  context.CancelFunc
	)
	if client.ResponseTimeout > 0 {
		// The request has a timeout, so create a context that is
		// canceled automatically when the timeout expires.
		callCtx, cancel = context.WithTimeout(ctx, client.ResponseTimeout)
	} else {
		callCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()
	select {
	case <-callCtx.Done():
		return nil, callCtx.Err()
	case <-done:
		return
	}
	return
}

// Close connection
func (client *Client) Close() (err error) {
	client.Lock()
	defer client.Unlock()
	if client.conn != nil {
		err = client.conn.Close()
		client.conn = nil
	}
	return
}
