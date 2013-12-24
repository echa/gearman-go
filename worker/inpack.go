// Copyright 2011 Xing Xing <mikespook@gmail.com>
// All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package worker

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
)

// Worker side job
type inPack struct {
	dataType             uint32
	data                 []byte
	handle, uniqueId, fn string
	a                    *agent
}

// Create a new job
func getInPack() *inPack {
	return &inPack{}
}

func (inpack *inPack) Data() []byte {
	return inpack.data
}

// Send some datas to client.
// Using this in a job's executing.
func (inpack *inPack) SendData(data []byte) {
	outpack := getOutPack()
	outpack.dataType = WORK_DATA
	hl := len(inpack.handle)
	l := hl + len(data) + 1
	outpack.data = getBuffer(l)
	copy(outpack.data, []byte(inpack.handle))
	copy(outpack.data[hl+1:], data)
	inpack.a.write(outpack)
}

func (inpack *inPack) SendWarning(data []byte) {
	outpack := getOutPack()
	outpack.dataType = WORK_WARNING
	hl := len(inpack.handle)
	l := hl + len(data) + 1
	outpack.data = getBuffer(l)
	copy(outpack.data, []byte(inpack.handle))
	copy(outpack.data[hl+1:], data)
	inpack.a.write(outpack)
}

// Update status.
// Tall client how many percent job has been executed.
func (inpack *inPack) UpdateStatus(numerator, denominator int) {
	n := []byte(strconv.Itoa(numerator))
	d := []byte(strconv.Itoa(denominator))
	outpack := getOutPack()
	outpack.dataType = WORK_STATUS
	hl := len(inpack.handle)
	nl := len(n)
	dl := len(d)
	outpack.data = getBuffer(hl + nl + dl + 3)
	copy(outpack.data, []byte(inpack.handle))
	copy(outpack.data[hl+1:], n)
	copy(outpack.data[hl+nl+2:], d)
	inpack.a.write(outpack)
}

// Decode job from byte slice
func decodeInPack(data []byte) (inpack *inPack, l int, err error) {
	if len(data) < MIN_PACKET_LEN { // valid package should not less 12 bytes
		err = fmt.Errorf("Invalid data: %V", data)
		return
	}
	dl := int(binary.BigEndian.Uint32(data[8:12]))
	dt := data[MIN_PACKET_LEN : dl+MIN_PACKET_LEN]
	if len(dt) != int(dl) { // length not equal
		err = fmt.Errorf("Invalid data: %V", data)
		return
	}
	inpack = getInPack()
	inpack.dataType = binary.BigEndian.Uint32(data[4:8])
	switch inpack.dataType {
	case JOB_ASSIGN:
		s := bytes.SplitN(dt, []byte{'\x00'}, 3)
		if len(s) == 3 {
			inpack.handle = string(s[0])
			inpack.fn = string(s[1])
			inpack.data = s[2]
		}
	case JOB_ASSIGN_UNIQ:
		s := bytes.SplitN(dt, []byte{'\x00'}, 4)
		if len(s) == 4 {
			inpack.handle = string(s[0])
			inpack.fn = string(s[1])
			inpack.uniqueId = string(s[2])
			inpack.data = s[3]
		}
	default:
		inpack.data = dt
	}
	l = dl + MIN_PACKET_LEN
	return
}