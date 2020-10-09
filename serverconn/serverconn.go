package serverconn

import (
	"bufio"
	"context"
	"encoding/binary"
	"io"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"
)

type ServerConn struct {
	conn   net.Conn
	reader io.Reader

	RequestCh  chan *Request
	responseCh chan *Request

	StopCtx  context.Context
	StopFunc context.CancelFunc

	closed uint64
}

type Request struct {
	serverConn *ServerConn
	seq        uint64
	Pkg        []byte
}

func (serverConn *ServerConn) IsClosed() bool {
	return atomic.LoadUint64(&serverConn.closed) == 1
}

func (serverConn *ServerConn) Close() {
	//log.Printf("be to close ServerConn")
	if atomic.CompareAndSwapUint64(&serverConn.closed, 0, 1) {
		//close(serverConn.RequestCh)
		close(serverConn.responseCh)
		_ = serverConn.conn.Close()
		log.Printf("success close ServerConn")
	}
}

func NewServerConn(ctx context.Context, conn net.Conn, requestCh chan *Request) (serverConn *ServerConn, err error) {
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		err = tcpConn.SetKeepAlive(true)
		if err != nil {
			log.Printf("SetKeepAlive fail=>%v", err)
			return
		}
		err = tcpConn.SetKeepAlivePeriod(10 * time.Second)
		if err != nil {
			log.Printf("SetKeepAlivePeriod fail=>%v", err)
			return
		}
	}

	serverConn = &ServerConn{
		conn:   conn,
		reader: bufio.NewReader(conn),

		RequestCh:  requestCh,
		responseCh: make(chan *Request, 4096),
	}
	serverConn.StopCtx, serverConn.StopFunc = context.WithCancel(ctx)

	go serverConn.GoReceive()
	go serverConn.GoSend()

	return
}

const (
	HeadSize = 16
)

func (serverConn *ServerConn) GoReceive() {
	//log.Printf("enter GoReceive")
	defer serverConn.Close()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("serverConn.RequestCh closed")
		}
	}()

	var headPkg = make([]byte, HeadSize)
	var pkgLen int
	var totalCnt = 0
	var thisCnt = 0
	var err error
	for !serverConn.IsClosed() {
		// read head
		totalCnt = 0
		for totalCnt < HeadSize {
			thisCnt, err = serverConn.reader.Read(headPkg[totalCnt:])
			if err != nil {
				if err == io.EOF || strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				log.Printf("Read fail=>%v", err)
				return
			}
			totalCnt += thisCnt
		}
		// read body
		pkgLen = int(binary.BigEndian.Uint64(headPkg))
		var pkg = make([]byte, pkgLen)
		totalCnt = 0
		for totalCnt < pkgLen {
			thisCnt, err = serverConn.reader.Read(pkg[totalCnt:])
			if err != nil {
				log.Printf("Read fail=>%v", err)
				return
			}
			//log.Printf("%s", pkg)
			totalCnt += thisCnt
		}
		// 构造 Request
		var request = &Request{
			serverConn: serverConn,
			seq:        binary.BigEndian.Uint64(headPkg[8:]),
			Pkg:        pkg,
		}
		//log.Printf("got Request=>%#v", request)
		//time.Sleep(5 * time.Second)
		serverConn.RequestCh <- request
	}
}

func (serverConn *ServerConn) GoSend() {
	//log.Printf("enter GoSend")
	defer serverConn.Close()

	var request *Request
	var ok bool
	var pkgLen int
	var n int
	var err error
	var ticker100ms = time.NewTicker(100 * time.Millisecond)
	for !serverConn.IsClosed() {
		select {
		case request, ok = <-serverConn.responseCh:
			if !ok {
				//log.Printf("responseCh has closed. ServerConn close->GoSend End")
				return
			}

			pkgLen = HeadSize + len(request.Pkg)
			pkg := make([]byte, pkgLen)
			n = 0
			binary.BigEndian.PutUint64(pkg[n:], uint64(len(request.Pkg)))
			n += 8
			binary.BigEndian.PutUint64(pkg[n:], request.seq)
			n += 8
			copy(pkg[n:], request.Pkg)
			_, err = serverConn.conn.Write(pkg)
			if err != nil {
				log.Printf("Write fail=>%v", err)
				return
			}
		case <-ticker100ms.C:
			err = serverConn.conn.SetWriteDeadline(time.Now().Add(30_100 * time.Millisecond))
			if err != nil {
				log.Printf("SetWriteDeadline fail=>%v", err)
				return
			}
		}
	}
}

func (request *Request) Done() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("responseCh have closed so drop this rsp")
		}
	}()

	select {
	case request.serverConn.responseCh <- request:
	default:
		log.Printf("responseCh full so drop this rsp")
	}
}
