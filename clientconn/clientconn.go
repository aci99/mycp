package clientconn

import (
	"bufio"
	"container/list"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrClientConnRequestChFull  = errors.New("ErrClientConnRequestChFull")
	ErrClientConnClosed         = errors.New("ErrClientConnClosed")
	ErrClientConnRequestTimeout = errors.New("ErrClientConnRequestTimeout")
)

type ClientConn struct {
	conn   net.Conn
	reader *bufio.Reader

	requestCh chan *Request

	seq uint64

	pendingRequestMutex sync.Mutex
	timeoutDur          time.Duration
	rList               *list.List // list.Element.Value 就是 *Request
	seq2requestElement  map[uint64]*list.Element

	closed uint64
}

type Request struct {
	ResponseCh chan *Request
	Pkg        []byte
	Err        error

	timeSend time.Time
	seq      uint64
}

const (
	HeadSize = 16
)

func (clientConn *ClientConn) GoReceive() {
	defer clientConn.Close()

	var headPkg = make([]byte, HeadSize)
	var pkgLen int
	var totalCnt = 0
	var thisCnt = 0
	var err error
	var seq uint64
	var request *Request
	var ok bool
	var element *list.Element
	for !clientConn.IsClosed() {
		// read head
		totalCnt = 0
		for totalCnt < HeadSize {
			thisCnt, err = clientConn.reader.Read(headPkg[totalCnt:])
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
			thisCnt, err = clientConn.reader.Read(pkg[totalCnt:])
			if err != nil {
				log.Printf("Read fail=>%v", err)
				return
			}
			totalCnt += thisCnt
		}
		// 取 Request
		seq = binary.BigEndian.Uint64(headPkg[8:])

		clientConn.pendingRequestMutex.Lock()
		element, ok = clientConn.seq2requestElement[seq]
		if !ok {
			log.Printf("miss request. drop this msg.")
			clientConn.pendingRequestMutex.Unlock()
			continue
		}
		request = element.Value.(*Request)
		clientConn.rList.Remove(element)
		delete(clientConn.seq2requestElement, seq)
		clientConn.pendingRequestMutex.Unlock()

		request.Pkg = pkg
		request.Err = nil
		//log.Printf("GoReceive->Done")
		request.Done()
	}
}

func (clientConn *ClientConn) GoSend() {
	defer clientConn.Close()

	var request *Request
	var ok bool
	var pkgLen int
	var n int
	var err error
	var ticker100ms = time.NewTicker(100 * time.Millisecond)
	var elementAdded *list.Element
FOR:
	for !clientConn.IsClosed() {
		select {
		case request, ok = <-clientConn.requestCh:
			if !ok {
				//log.Printf("requestCh has closed. be to close clientConn")
				break FOR
			}

			request.timeSend = time.Now()
			request.seq = clientConn.seq
			clientConn.seq += 1

			clientConn.pendingRequestMutex.Lock()
			elementAdded = clientConn.rList.PushBack(request)
			clientConn.seq2requestElement[request.seq] = elementAdded
			clientConn.pendingRequestMutex.Unlock()

			pkgLen = HeadSize + len(request.Pkg)
			pkg := make([]byte, pkgLen)
			n = 0
			binary.BigEndian.PutUint64(pkg[n:], uint64(len(request.Pkg)))
			n += 8
			binary.BigEndian.PutUint64(pkg[n:], request.seq)
			n += 8
			copy(pkg[n:], request.Pkg)
			//log.Printf("be to write=>%s", pkg)
			m, err := clientConn.conn.Write(pkg)
			if err != nil {
				log.Printf("Write fail=>%v", err)
				break FOR
			}
			//log.Printf("Write total %d Bytes", m)
			_ = m
		case <-ticker100ms.C:
			err = clientConn.conn.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
			if err != nil {
				log.Printf("SetWriteDeadline fail=>%v", err)
				break FOR
			}
			clientConn.Timeout()
		}
	}

	// 排空还未返回的请求
	clientConn.pendingRequestMutex.Lock()
	defer clientConn.pendingRequestMutex.Unlock()
	if clientConn.rList == nil {
		return
	}
	var nextElement *list.Element
	for e := clientConn.rList.Front(); e != nil; {
		request = e.Value.(*Request)
		delete(clientConn.seq2requestElement, request.seq) // TODO: 不删会内存泄漏吗?
		request.Err = ErrClientConnClosed
		log.Printf("GoSend->Done()")
		request.Done()
		nextElement = e.Next()
		clientConn.rList.Remove(e)
		e = nextElement
	}
}

func (clientConn *ClientConn) Timeout() {
	//log.Println(time.Now())
	var expireTime = time.Now().Add(-1 * clientConn.timeoutDur)
	//log.Println(expireTime)
	var timeoutRequests *list.List
	var request *Request
	var nextElement *list.Element
	clientConn.pendingRequestMutex.Lock()
	//log.Printf("clientConn.rList len=>%d", clientConn.rList.Len())

	for e := clientConn.rList.Front(); e != nil; {
		request = e.Value.(*Request)
		if request.timeSend.After(expireTime) {
			break
		}
		if timeoutRequests == nil {
			timeoutRequests = list.New()
		}
		timeoutRequests.PushBack(request)
		delete(clientConn.seq2requestElement, request.seq)
		nextElement = e.Next()
		clientConn.rList.Remove(e)
		e = nextElement
	}
	clientConn.pendingRequestMutex.Unlock()

	if timeoutRequests == nil {
		return
	}

	//log.Printf("timeoutRequests len=>%d", timeoutRequests.Len())

	for e := timeoutRequests.Front(); e != nil; e = e.Next() {
		request = e.Value.(*Request)
		request.Err = ErrClientConnRequestTimeout
		//log.Printf("timeout->done. request=>%#v", request)
		request.Done()
	}
}

func (clientConn *ClientConn) Send(request *Request) {
	defer func() {
		if r := recover(); r != nil {
			request.Err = ErrClientConnClosed
			log.Printf("Send->Done()")
			request.Done()
			log.Print("ClientConn has closed")
		}
	}()

	select {
	case clientConn.requestCh <- request:
	default:
		request.Err = ErrClientConnRequestChFull
		log.Printf("Send-full->Done()")
		request.Done()
		log.Print("requestCh full")
	}
}

func NewClientConn(conn net.Conn) (clientConn *ClientConn, err error) {
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

	clientConn = &ClientConn{
		conn:   conn,
		reader: bufio.NewReader(conn),

		requestCh:          make(chan *Request, 4096),
		timeoutDur:         30 * time.Second,
		rList:              list.New(),
		seq2requestElement: make(map[uint64]*list.Element),
	}

	go clientConn.GoReceive()
	go clientConn.GoSend()

	return
}

func (clientConn *ClientConn) Close() {
	if atomic.CompareAndSwapUint64(&clientConn.closed, 0, 1) {
		close(clientConn.requestCh)
		clientConn.rList = nil // 不做会 memory leak 吗?
		_ = clientConn.conn.Close()
	}
}

func (clientConn *ClientConn) IsClosed() bool {
	return atomic.LoadUint64(&clientConn.closed) == 1
}

func (request *Request) Done() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ResponseCh have closed so drop this rsp")
		}
	}()

	select {
	case request.ResponseCh <- request:
	default:
		log.Printf("ResponseCh full so drop this rsp")
	}
}
