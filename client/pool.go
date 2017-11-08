package client

import (
	"context"
	"crypto/tls"
	"errors"
	"math/rand"
	"sync"
)

var (
	ErrNotFound = errors.New("Server Not Found")
)

type PoolClient struct {
	*Client
	Rate  int
	mutex sync.Mutex
}

type SelectionHandler func(map[string]*PoolClient, string) string

func SelectWithRate(pool map[string]*PoolClient,
	last string) (addr string) {
	total := 0
	for _, item := range pool {
		total += item.Rate
		if rand.Intn(total) < item.Rate {
			return item.addr
		}
	}
	return last
}

func SelectRandom(pool map[string]*PoolClient,
	last string) (addr string) {
	r := rand.Intn(len(pool))
	i := 0
	for k, _ := range pool {
		if r == i {
			return k
		}
		i++
	}
	return last
}

type Pool struct {
	SelectionHandler SelectionHandler
	ErrorHandler     ErrorHandler
	Clients          map[string]*PoolClient

	last string

	mutex sync.Mutex
}

// Return a new pool.
func NewPool(poolSize int) (pool *Pool) {
	return &Pool{
		Clients:          make(map[string]*PoolClient, poolSize),
		SelectionHandler: SelectWithRate,
	}
}

// Add a server with rate.
func (pool *Pool) Add(net, addr string, rate int, tlsConfig *tls.Config) (c *Client, err error) {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	if item, ok := pool.Clients[addr]; ok {
		item.Rate = rate
		return item.Client, nil
	} else {
		item = &PoolClient{
			Client: New(net, addr, tlsConfig),
			Rate:   rate,
		}
		item.Client.ErrorHandler = pool.ErrorHandler
		pool.Clients[addr] = item
		// try connecting
		return item.Client, item.Client.Connect()
	}
}

// Remove a server.
func (pool *Pool) Remove(addr string) {
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	if item, ok := pool.Clients[addr]; ok {
		item.Client.Close()
	}
	delete(pool.Clients, addr)
}

func (pool *Pool) Do(ctx context.Context, funcname string, data []byte,
	flag byte, h ResponseHandler) (addr, handle string, err error) {
	client := pool.selectServer()
	client.Lock()
	defer client.Unlock()
	handle, err = client.Do(ctx, funcname, data, flag, h)
	addr = client.addr
	return
}

func (pool *Pool) DoBg(ctx context.Context, funcname string, data []byte,
	flag byte) (addr, handle string, err error) {
	client := pool.selectServer()
	client.Lock()
	defer client.Unlock()
	handle, err = client.DoBg(ctx, funcname, data, flag)
	addr = client.addr
	return
}

// Get job status from job server.
// !!!Not fully tested.!!!
func (pool *Pool) Status(ctx context.Context, addr, handle string) (status *Status, err error) {
	if client, ok := pool.Clients[addr]; ok {
		client.Lock()
		defer client.Unlock()
		status, err = client.Status(ctx, handle)
	} else {
		err = ErrNotFound
	}
	return
}

// Send a something out, get the samething back.
func (pool *Pool) Echo(ctx context.Context, addr string, data []byte) (echo []byte, err error) {
	var client *PoolClient
	if addr == "" {
		client = pool.selectServer()
	} else {
		var ok bool
		if client, ok = pool.Clients[addr]; !ok {
			err = ErrNotFound
			return
		}
	}
	client.Lock()
	defer client.Unlock()
	echo, err = client.Echo(ctx, data)
	return
}

// Close
func (pool *Pool) Close() (err map[string]error) {
	err = make(map[string]error)
	for _, c := range pool.Clients {
		err[c.addr] = c.Close()
	}
	return
}

// selecting server
func (pool *Pool) selectServer() (client *PoolClient) {
	for client == nil {
		addr := pool.SelectionHandler(pool.Clients, pool.last)
		var ok bool
		if client, ok = pool.Clients[addr]; ok {
			pool.last = addr
			break
		}
	}
	return
}
