package mycpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mycp/mycpproto"
	"mycp/serverconn"
	"mycp/util"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

type Server struct {
	processCnt int
	requestCh  chan *serverconn.Request

	NeedAuth bool
	Password string

	WrongPasswordTimes uint64

	StopCtx  context.Context
	StopFunc context.CancelFunc

	closed uint64
}

func NewServer() (server *Server) {
	server = &Server{
		processCnt: 4,
		NeedAuth:   true,
	}
	server.StopCtx, server.StopFunc = context.WithCancel(context.Background())
	server.requestCh = make(chan *serverconn.Request)
	for idx := 0; idx < server.processCnt; idx++ {
		go server.GoProcess(idx)
	}
	return
}

func (server *Server) GoProcess(goID int) {
	defer server.Close()
	var request *serverconn.Request
	var ok bool
	for !server.IsClosed() {
		select {
		case request, ok = <-server.requestCh:
			if !ok {
				log.Printf("requestCh closed, GoProcess(%d) end", goID)
				return
			}

			dropped := MyCP(request, server)
			if dropped {
				continue
			}

			request.Done()
		case <-server.StopCtx.Done():
			return
		}
	}
}

func (server *Server) Start(host string) (err error) {
	var listener net.Listener
	listener, err = net.Listen("tcp", host)
	if err != nil {
		log.Printf("Listen fail=>%v", err)
		return
	}
	log.Printf("listening on %s", host)

	var conn net.Conn
	for !server.IsClosed() {
		conn, err = listener.Accept()
		if err != nil {
			log.Printf("Accept fail=>%v", err)
			time.Sleep(1 * time.Second)
			continue
		}
		log.Printf("=============================")
		log.Printf("new conn: local=>%v, remote=>%v", conn.LocalAddr(), conn.RemoteAddr())
		_, _ = serverconn.NewServerConn(server.StopCtx, conn, server.requestCh) // ServerConn 是什么时候 gc 的?
	}
	return nil
}

func (server *Server) IsClosed() bool {
	return atomic.LoadUint64(&server.closed) == 1
}

func (server *Server) Close() {
	if atomic.CompareAndSwapUint64(&server.closed, 0, 1) {
		close(server.requestCh)
	}
}

func (server *Server) LoadPassword() (err error) {
	binDir, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable fail=>%w", err)
	}
	binDir, err = filepath.Abs(filepath.Dir(binDir))
	if err != nil {
		return fmt.Errorf("fail to get bin dir=>%w", err)
	}
	passwordFileName := "mycp_password.txt"
	passwordFilePath := fmt.Sprintf("%s/%s", binDir, passwordFileName)
	passwordFile, err := os.Open(passwordFilePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("Open fail=>%w", err)
	}
	passwordLen := 16
	var password string
	if err == nil {
		defer passwordFile.Close()
		pwd := make([]byte, passwordLen)
		n, err := passwordFile.Read(pwd)
		if err != nil && err != io.EOF {
			return fmt.Errorf("Read passwordFile fail=>%w", err)
		}
		if 0 < n && n < passwordLen {
			return fmt.Errorf("len of password from password file is invalid. len=>%d, pwd=>%s", n, pwd)
		}
		if n >= passwordLen {
			password = string(pwd)
		} else {
			password = util.GenPassword(passwordLen)
		}
	} else {
		// 如果文件不存在
		password = util.GenPassword(passwordLen)
	}
	server.Password = password
	return nil
}

func MyCP(request *serverconn.Request, server *Server) (dropped bool) {
	password := server.Password
	// 解码
	pkgDecryted, err := util.Decrypt(request.Pkg, password)
	if err != nil {
		log.Printf("Decrypt fail=>%v", err)
		dropped = true
		atomic.AddUint64(&server.WrongPasswordTimes, 1)
		log.Printf("wrongPasswordTimes=>%d", atomic.LoadUint64(&server.WrongPasswordTimes))
		wrongPasswordTimes := atomic.LoadUint64(&server.WrongPasswordTimes)
		if wrongPasswordTimes >= 5 {
			log.Fatalf("wrongPasswordTimes beyond max times, exit now.")
		}
		return
	}
	myCPPackage := &mycpproto.MyCPPackage{}
	err = json.Unmarshal(pkgDecryted, myCPPackage)
	if err != nil {
		log.Printf("json.Unmarshal fail=>%v", err)
		dropped = true
		return
	}

	// 编码
	defer func() {
		if !dropped {
			pkgEncoded, err := json.Marshal(myCPPackage)
			if err != nil {
				log.Printf("Marshal fail=>%v", err)
				dropped = true
				return
			}
			request.Pkg = util.Encrypt(pkgEncoded, password)
		}
	}()

	if myCPPackage.Direction == mycpproto.DirectionRemoteIsSrc {
		MyCPFromRemoteToLocal(myCPPackage)
	} else {
		MyCPFromLocalToRemote(myCPPackage)
	}
	return
}

func MyCPFromRemoteToLocal(myCPPackage *mycpproto.MyCPPackage) {
	srcFileInfo, err := os.Stat(myCPPackage.SrcPath)
	if err != nil {
		log.Printf("os.Stat fail=>%v", err)
		myCPPackage.Status = mycpproto.MyCPPackageStatusFail
		return
	}
	if !srcFileInfo.IsDir() {
		// 如果 src 是 file, 则 copy 内容

		if myCPPackage.OnlyModified {
			if srcFileInfo.ModTime().Before(myCPPackage.LastMyCPTime) {
				log.Printf("no need to cp because no modification")
				myCPPackage.Status = mycpproto.MyCPPackageStatusNoNeedToCP
				return
			}
		}

		myCPPackage.SrcIsDir = false
		inputFile, err := os.Open(myCPPackage.SrcPath)
		if err != nil {
			log.Printf("Open fail=>%v", err)
			myCPPackage.Status = mycpproto.MyCPPackageStatusFail
			return
		}
		defer inputFile.Close()
		var maxSize = 500 * 1024 * 1024
		myCPPackage.Data = make([]byte, maxSize)
		n, err := inputFile.Read(myCPPackage.Data)
		if err != nil {
			if err != io.EOF {
				log.Printf("Read fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
		}
		if n >= maxSize {
			log.Printf("file larger than %d Bytes, filename=>%s", maxSize, srcFileInfo.Name())
			myCPPackage.Status = mycpproto.MyCPPackageStatusFail
			return
		}
		myCPPackage.Data = myCPPackage.Data[:n]
		//log.Printf("total read: %d Bytes", n)
		myCPPackage.Status = mycpproto.MyCPPackageStatusSucc
		return
	} else {
		// 如果 src 是路径

		myCPPackage.SrcIsDir = true
		fileInfos, err := ioutil.ReadDir(myCPPackage.SrcPath)
		if err != nil {
			log.Printf("ioutil.ReadDir fail => %v", err)
			myCPPackage.Status = mycpproto.MyCPPackageStatusFail
			return
		}
		for _, info := range fileInfos {
			if !info.IsDir() && myCPPackage.OnlyModified && info.ModTime().Before(myCPPackage.LastMyCPTime) {
				continue
			}
			//if strings.HasPrefix(info.Name(), ".") {
			//	continue
			//}
			//if strings.HasSuffix(info.Name(), "exe") {
			//	continue
			//}
			var myFileInfo = mycpproto.MyFileInfo{
				Name:  info.Name(),
				IsDir: info.IsDir(),
			}
			myCPPackage.MyFileInfoSlice = append(myCPPackage.MyFileInfoSlice, myFileInfo)
		}
		myCPPackage.Status = mycpproto.MyCPPackageStatusSucc
		return
	}
}

func MyCPFromLocalToRemote(myCPPackage *mycpproto.MyCPPackage) {
	if !myCPPackage.SrcIsDir {
		// 源是文件
		dstPathInfo, err := os.Stat(myCPPackage.DstPath)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("os.Stat fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
		}

		if os.IsNotExist(err) {
			// 如果 dst 不存在
			// 如果 dst 以 / 结尾则当成是路径, 否则视为文件

			log.Printf("myCPPackage.DstPath=>#%v#", myCPPackage.DstPath)
			realDstPath, _ := filepath.Split(myCPPackage.DstPath)
			if realDstPath != "" {
				err := os.MkdirAll(realDstPath, 0775)
				if err != nil {
					log.Printf("MkdirAll fail=>%v", err)
					myCPPackage.Status = mycpproto.MyCPPackageStatusFail
					return
				}
			}
			var realDstFile = myCPPackage.DstPath
			if len(realDstPath) == len(myCPPackage.DstPath) {
				// 如果 dst 以 / 结尾, 则视为路径
				_, realSrcFileName := filepath.Split(myCPPackage.SrcPath)
				realDstFile = fmt.Sprintf("%s/%s", realDstPath, realSrcFileName)
			}

			log.Printf("be to write=>%s", realDstFile)
			outputFile, err := os.OpenFile(realDstFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
			if err != nil {
				log.Printf("OpenFile fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
			defer outputFile.Close()
			n, err := outputFile.Write(myCPPackage.Data)
			if err != nil {
				log.Printf("write fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
			log.Printf("total write %d Bytes", n)
			myCPPackage.Status = mycpproto.MyCPPackageStatusSucc
		} else if !dstPathInfo.IsDir() {
			// dst 存在且是文件
			log.Printf("be to write=>%s", myCPPackage.DstPath)
			outputFile, err := os.OpenFile(myCPPackage.DstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
			if err != nil {
				log.Printf("OpenFile fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
			defer outputFile.Close()
			n, err := outputFile.Write(myCPPackage.Data)
			if err != nil {
				log.Printf("write fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
			log.Printf("total write %d Bytes", n)
			myCPPackage.Status = mycpproto.MyCPPackageStatusSucc
		} else {
			// dst 存在且是路径
			_, realSrcFileName := filepath.Split(myCPPackage.SrcPath)
			dstFileName := fmt.Sprintf("%s/%s", myCPPackage.DstPath, realSrcFileName)
			log.Printf("be to write=>%s", dstFileName)
			outputFile, err := os.OpenFile(dstFileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
			if err != nil {
				log.Printf("OpenFile fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
			defer outputFile.Close()
			n, err := outputFile.Write(myCPPackage.Data)
			if err != nil {
				log.Printf("Write fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
			//log.Printf("total write %d Bytes", n)
			_ = n
			myCPPackage.Status = mycpproto.MyCPPackageStatusSucc
		}
	} else {
		// 源是路径

		dstPathInfo, err := os.Stat(myCPPackage.DstPath)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("os.Stat fail=>%v", err)
				myCPPackage.Status = mycpproto.MyCPPackageStatusFail
				return
			}
		}

		if err == nil && !dstPathInfo.IsDir() {
			log.Printf("fail=>src is dir but dst is file")
			myCPPackage.Status = mycpproto.MyCPPackageStatusFail
			return
		}

		srcPathTrimmed := strings.TrimSuffix(myCPPackage.SrcPath, "/")
		//log.Printf("srcPathTrimmed=>%v", srcPathTrimmed)
		for len(srcPathTrimmed) >= 2 && strings.HasSuffix(srcPathTrimmed, "/") {
			srcPathTrimmed = strings.TrimSuffix(srcPathTrimmed, "/")
			//log.Printf("srcPathTrimmed=>%v", srcPathTrimmed)
		}
		_, srcPathLast := filepath.Split(srcPathTrimmed)
		realDstPath := fmt.Sprintf("%s/%s", myCPPackage.DstPath, srcPathLast)

		err = os.MkdirAll(realDstPath, 0775)
		if err != nil {
			log.Printf("os.MkdirAll fail=>%v", err)
			myCPPackage.Status = mycpproto.MyCPPackageStatusFail
			return
		}
		myCPPackage.Status = mycpproto.MyCPPackageStatusSucc
	}
	return
}
