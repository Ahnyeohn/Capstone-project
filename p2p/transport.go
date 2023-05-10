// Copyright 2020 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package p2p

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/bitutil"
	"github.com/ethereum/go-ethereum/metrics"
	"github.com/ethereum/go-ethereum/p2p/rlpx"
	"github.com/ethereum/go-ethereum/rlp"
	quic "github.com/quic-go/quic-go"
)

const (
	// total timeout for encryption handshake and protocol
	// handshake in both directions.
	handshakeTimeout = 5 * time.Second

	// This is the timeout for sending the disconnect reason.
	// This is shorter than the usual timeout because we don't want
	// to wait if the connection is known to be bad anyway.
	discWriteTimeout = 1 * time.Second
)

// rlpxTransport is the transport used by actual (non-test) connections.
// It wraps an RLPx connection with locks and read/write deadlines.
type rlpxTransport struct {
	rmu, wmu sync.Mutex
	wbuf     bytes.Buffer
	conn     *rlpx.Conn
}

// fix: func newRLPX(conn net.Conn, dialDest *ecdsa.PublicKey) transport {
// devp2p quic
// devp2p perf
func newRLPX(conn quic.Connection, stream quic.Stream, dialDest *ecdsa.PublicKey) transport {
	//fmt.Println("Func: newRLPX()")
	return &rlpxTransport{conn: rlpx.NewConn(conn, stream, dialDest)} // 여기에 인자가 있다는건 어디서 받아온걸 넣겠다는거 아닌가? 의심
}

func (t *rlpxTransport) ReadMsg() (Msg, error) {
	//fmt.Println("func: rt/ReadMsg()")
	t.rmu.Lock()
	defer t.rmu.Unlock()

	var msg Msg

	stream, err := t.conn.Conn.AcceptStream(context.Background())
	if err != nil {
		fmt.Println("Accept error")
	}
	defer stream.Close()

	t.conn.SetReadDeadline(time.Now().Add(frameReadTimeout))

	buf := make([]byte, 1024)
	n, err := stream.Read(buf)
	data := buf[:n]

	str := string(data)

	start, err := time.Parse(time.RFC3339Nano, str)
	if err != nil {
	}

	code, data, wireSize, err := t.conn.Read()
	if err == nil {
		// Protocol messages are dispatched to subprotocol handlers asynchronously,
		// but package rlpx may reuse the returned 'data' buffer on the next call
		// to Read. Copy the message data to avoid this being an issue.
		end := time.Now()
		data = common.CopyBytes(data)

		if code == 7 || code == 23 {
			delay := (end).Sub(start)
			fmt.Printf("msg code: %d, block size: %d (compress: %d), block prop delay %v\n", code, len(data), wireSize, delay)
		}
		msg = Msg{
			ReceivedAt: end,
			SendedAt:   start,
			Code:       code,
			Size:       uint32(len(data)),
			meterSize:  uint32(wireSize),
			Payload:    bytes.NewReader(data),
		}
	}
	return msg, err
}

// 적게나마 호출이 되는듯 그리고 내부적으로 rlpx.write함수호출
func (t *rlpxTransport) WriteMsg(msg Msg) error {
	//fmt.Println("Func: rt/WriteMsg()")
	t.wmu.Lock()
	defer t.wmu.Unlock()

	var size uint32
	var err error

	// Copy message data to write buffer.
	t.wbuf.Reset()
	if _, err := io.CopyN(&t.wbuf, msg.Payload, int64(msg.Size)); err != nil {
		return err
	}

	stream, err := t.conn.Conn.OpenStreamSync(context.Background())
	if err != nil {
		fmt.Println("OpenStream error")
	}
	defer stream.Close()

	// Write the message.
	t.conn.SetWriteDeadline(time.Now().Add(frameWriteTimeout))

	//devp2p perf
	ti := time.Now()
	loc, err := time.LoadLocation("Asia/Seoul")
	if err != nil {
		panic(err)
	}
	str := ti.In(loc)
	//str := time.Now().String()
	if msg.Code == 7 || msg.Code == 23 {

		fmt.Printf("msg code: %d, send time: %s, block size: %d\n", msg.Code, str, msg.Size)
	}
	b := []byte(str.Format(time.RFC3339Nano))

	_, err = stream.Write(b)
	if err != nil {
		return err
	}

	size, err = t.conn.Write(msg.Code, t.wbuf.Bytes()) // conn 구조체를 통해 세션에 접근
	if err != nil {
		return err
	}

	// Set metrics.
	msg.meterSize = size
	if metrics.Enabled && msg.meterCap.Name != "" { // don't meter non-subprotocol messages
		m := fmt.Sprintf("%s/%s/%d/%#02x", egressMeterName, msg.meterCap.Name, msg.meterCap.Version, msg.meterCode)
		metrics.GetOrRegisterMeter(m, nil).Mark(int64(msg.meterSize))
		metrics.GetOrRegisterMeter(m+"/packets", nil).Mark(1)
	}
	return nil
}

func (t *rlpxTransport) close(err error) {
	t.wmu.Lock()
	defer t.wmu.Unlock()

	// Tell the remote end why we're disconnecting if possible.
	// We only bother doing this if the underlying connection supports
	// setting a timeout tough.
	if t.conn != nil {
		if r, ok := err.(DiscReason); ok && r != DiscNetworkError {
			deadline := time.Now().Add(discWriteTimeout)
			if err := t.conn.SetWriteDeadline(deadline); err == nil {
				// Connection supports write deadline.
				t.wbuf.Reset()
				rlp.Encode(&t.wbuf, []DiscReason{r})
				t.conn.Write(discMsg, t.wbuf.Bytes())
			}
		}
	}
	t.conn.Close()
}

func (t *rlpxTransport) doEncHandshake(prv *ecdsa.PrivateKey) (*ecdsa.PublicKey, error) {
	//fmt.Println("Func: doEncHandshake()")
	t.conn.SetDeadline(time.Now().Add(handshakeTimeout))
	return t.conn.Handshake(prv)
}

func (t *rlpxTransport) doProtoHandshake(our *protoHandshake) (their *protoHandshake, err error) {
	// Writing our handshake happens concurrently, we prefer
	// returning the handshake read error. If the remote side
	// disconnects us early with a valid reason, we should return it
	// as the error so it can be tracked elsewhere.
	//fmt.Println("Func: doProtoHandshake()")
	werr := make(chan error, 1)
	go func() { werr <- Send(t, handshakeMsg, our) }()
	if their, err = readProtocolHandshake(t); err != nil {
		<-werr // make sure the write terminates too
		return nil, err
	}
	if err := <-werr; err != nil {
		return nil, fmt.Errorf("write error: %v", err)
	}
	// If the protocol version supports Snappy encoding, upgrade immediately
	t.conn.SetSnappy(their.Version >= snappyProtocolVersion)

	return their, nil
}

func readProtocolHandshake(rw MsgReader) (*protoHandshake, error) {
	msg, err := rw.ReadMsg()
	if err != nil {
		return nil, err
	}
	if msg.Size > baseProtocolMaxMsgSize {
		return nil, fmt.Errorf("message too big")
	}
	if msg.Code == discMsg {
		// Disconnect before protocol handshake is valid according to the
		// spec and we send it ourself if the post-handshake checks fail.
		// We can't return the reason directly, though, because it is echoed
		// back otherwise. Wrap it in a string instead.
		var reason [1]DiscReason
		rlp.Decode(msg.Payload, &reason)
		return nil, reason[0]
	}
	if msg.Code != handshakeMsg {
		return nil, fmt.Errorf("expected handshake, got %x", msg.Code)
	}
	var hs protoHandshake
	if err := msg.Decode(&hs); err != nil {
		return nil, err
	}
	if len(hs.ID) != 64 || !bitutil.TestBytes(hs.ID) {
		return nil, DiscInvalidIdentity
	}
	return &hs, nil
}
