package mycpclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mycp/clientconn"
	"mycp/mycpproto"
	"mycp/util"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	clientConn *clientconn.ClientConn
}

func NewClient(host string) (client *Client, err error) {
	client = &Client{}
	var conn net.Conn
	conn, err = net.DialTimeout("tcp", host, 1*time.Second)
	if err != nil {
		return
	}
	log.Printf("new conn. local=>%v, remote=>%v", conn.LocalAddr(), conn.RemoteAddr())
	var clientConn *clientconn.ClientConn
	clientConn, err = clientconn.NewClientConn(conn)
	if err != nil {
		return
	}
	client.clientConn = clientConn
	return
}

func (client *Client) Close() {
	client.clientConn.Close()
}

// windows 也使用 "/" 的形式.
// 比如 D:/work/gopaths/gopath-wtableplus/src/bj58.com/wtableplus/proxy/transaction.go
func (client *Client) MyCPFromRemoteToLocal(srcPath, dstPath string, onlyModified bool, lastMyCPTime time.Time, password string) (err error) {
	// 发请求
	var myCPPackage = &mycpproto.MyCPPackage{
		SrcPath:      srcPath,
		DstPath:      dstPath,
		OnlyModified: onlyModified,
		LastMyCPTime: lastMyCPTime,
		Direction:    mycpproto.DirectionRemoteIsSrc,
	}
	var request = &clientconn.Request{
		ResponseCh: make(chan *clientconn.Request, 1),
	}
	pkgEncoded, err := json.Marshal(myCPPackage)
	if err != nil {
		return fmt.Errorf("marshal fail=>%w", err)
	}
	request.Pkg = util.Encrypt(pkgEncoded, password)
	client.clientConn.Send(request)

	// 处理响应
	request = <-request.ResponseCh
	if request.Err != nil {
		return fmt.Errorf("request.Err=>%w", request.Err)
	}
	dycrpted, err := util.Decrypt(request.Pkg, password)
	if err != nil {
		return fmt.Errorf("Decrypt fail=>%w", err)
	}
	var rsp = &mycpproto.MyCPPackage{}
	err = json.Unmarshal(dycrpted, rsp)
	if err != nil {
		return fmt.Errorf("unmarshal fail=>%w", err)
	}
	if rsp.Status == mycpproto.MyCPPackageStatusFail {
		return fmt.Errorf("fail=>MyCPPackageStatusFail")
	} else if rsp.Status == mycpproto.MyCPPackageStatusNoNeedToCP {
		log.Printf("no need to cp")
		return nil
	}

	if !rsp.SrcIsDir {
		// 源是文件

		dstFileInfo, err := os.Stat(dstPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("os.Stat fail=>%w", err)
			}
		}

		if os.IsNotExist(err) {
			// 如果 dst 不存在
			// 如果 dst 以 / 结尾则当成是路径, 否则视为文件
			realDstPath, _ := filepath.Split(dstPath)
			if realDstPath != "" {
				err := os.MkdirAll(realDstPath, 0775)
				if err != nil {
					return fmt.Errorf("MkdirAll fail=>%w", err)
				}
			}
			var realDstFile = dstPath
			if len(realDstPath) == len(dstPath) {
				// 如果 dst 以 / 结尾, 则视为路径
				_, realSrcFileName := filepath.Split(srcPath)
				realDstFile = fmt.Sprintf("%s/%s", realDstPath, realSrcFileName)
			}
			log.Printf("be to write=>%s", realDstFile)
			outputFile, err := os.OpenFile(realDstFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
			if err != nil {
				return fmt.Errorf("OpenFile fail=>%w", err)
			}
			defer outputFile.Close()
			n, err := outputFile.Write(rsp.Data)
			if err != nil {
				return fmt.Errorf("write fail=>%w", err)
			}
			log.Printf("total write %d Bytes", n)
			return nil
		} else if !dstFileInfo.IsDir() {
			// dst 存在且是文件
			outputFile, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
			if err != nil {
				return fmt.Errorf("OpenFile fail=>%w", err)
			}
			defer outputFile.Close()
			n, err := outputFile.Write(rsp.Data)
			if err != nil {
				return fmt.Errorf("write fail=>%w", err)
			}
			log.Printf("total write %d Bytes", n)
			return nil
		} else {
			// dst 存在且是路径

			srcPathTrimmed := strings.TrimSuffix(myCPPackage.SrcPath, "/")
			for len(srcPathTrimmed) >= 2 && strings.HasSuffix(srcPathTrimmed, "/") {
				srcPathTrimmed = strings.TrimSuffix(srcPathTrimmed, "/")
			}

			_, realSrcFileName := filepath.Split(srcPathTrimmed)
			dstFileName := fmt.Sprintf("%s/%s", dstPath, realSrcFileName)
			log.Printf("be to write=>%s", dstFileName)
			outputFile, err := os.OpenFile(dstFileName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
			if err != nil {
				return fmt.Errorf("OpenFile fail=>%w", err)
			}
			defer outputFile.Close()
			n, err := outputFile.Write(rsp.Data)
			if err != nil {
				return fmt.Errorf("write fail=>%w", err)
			}
			//log.Printf("total write %d Bytes", n)
			_ = n
			return nil
		}
	} else {
		// 源是路径

		dstPathInfo, err := os.Stat(dstPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("os.Stat fail=>%w", err)
			}
		}
		// 如果 src 是路径, dst 不存在, 则把 dst 当成是路径

		if err == nil && !dstPathInfo.IsDir() {
			return fmt.Errorf("fail=>src is dir but dst is file")
		}

		srcPathTrimmed := strings.TrimSuffix(srcPath, "/")
		for len(srcPathTrimmed) >= 2 && strings.HasSuffix(srcPathTrimmed, "/") {
			srcPathTrimmed = strings.TrimSuffix(srcPathTrimmed, "/")
		}

		_, srcPathLast := filepath.Split(srcPathTrimmed)
		realDstPath := fmt.Sprintf("%s/%s", dstPath, srcPathLast)

		err = os.MkdirAll(realDstPath, 0775)
		if err != nil {
			return fmt.Errorf("os.MkdirAll fail=>%w", err)
		}

		for _, myFileInfo := range rsp.MyFileInfoSlice {
			newSrcPath := fmt.Sprintf("%s/%s", srcPath, myFileInfo.Name)
			err = client.MyCPFromRemoteToLocal(newSrcPath, realDstPath, onlyModified, lastMyCPTime, password)
			if err != nil {
				return fmt.Errorf("MyCPFromRemoteToLocal fail=>%w", err)
			}
		}
		return nil
	}
}

func (client *Client) MyCPFromLocalToRemote(srcPath, dstPath string, onlyModified bool, lastMyCPTime time.Time, password string) (err error) {
	srcPathInfo, err := os.Stat(srcPath)
	if err != nil {
		log.Printf("os.Stat fail=>%v", err)
		return
	}
	if !srcPathInfo.IsDir() {
		// 如果 src 是文件
		if onlyModified {
			if srcPathInfo.ModTime().Before(lastMyCPTime) {
				log.Printf("no need to cp because no modification")
				return
			}
		}

		var inputFile *os.File
		inputFile, err = os.Open(srcPath)
		if err != nil {
			log.Printf("open fail=>%v", err)
			return
		}
		var maxSize = 500 * 1024 * 1024
		data := make([]byte, maxSize)
		var n int
		n, err = inputFile.Read(data)
		if err != nil {
			log.Printf("Read fail=>%v", err)
			return
		}
		//log.Printf("file data=>#%v#", data[:n])
		//log.Printf("file data=>%#v", data[:n])
		_ = inputFile.Close()
		if n >= maxSize {
			log.Printf("file larger than %d Bytes, filename=>%s", maxSize, srcPath)
			err = fmt.Errorf("file larger than %d Bytes, filename=>%s", maxSize, srcPath)
			return
		}

		// 发请求
		var myCPPackage = &mycpproto.MyCPPackage{
			SrcPath:   srcPath,
			DstPath:   dstPath,
			Direction: mycpproto.DirectionRemoteIsDst,
			SrcIsDir:  false,
			Data:      data[:n],
		}
		var request = &clientconn.Request{
			ResponseCh: make(chan *clientconn.Request, 1),
		}
		var pkgEncoded []byte
		pkgEncoded, err = json.Marshal(myCPPackage)
		if err != nil {
			err = fmt.Errorf("marshal fail=>%w", err)
			return
		}
		request.Pkg = util.Encrypt(pkgEncoded, password)
		client.clientConn.Send(request)

		// 处理响应
		request = <-request.ResponseCh
		if request.Err != nil {
			return fmt.Errorf("request.Err=>%w", request.Err)
		}
		dycrypted, err := util.Decrypt(request.Pkg, password)
		if err != nil {
			return fmt.Errorf("Decrypt fail=>%w", err)
		}
		var rsp = &mycpproto.MyCPPackage{}
		err = json.Unmarshal(dycrypted, rsp)
		if err != nil {
			return fmt.Errorf("unmarshal fail=>%w", err)
		}
		if rsp.Status != mycpproto.MyCPPackageStatusSucc {
			return fmt.Errorf("remote execution fail")
		}
		return nil
	} else {
		// 如果 src 是路径

		// 发请求
		var myCPPackage = &mycpproto.MyCPPackage{
			SrcPath:   srcPath,
			DstPath:   dstPath,
			Direction: mycpproto.DirectionRemoteIsDst,
			SrcIsDir:  true,
		}
		var request = &clientconn.Request{
			ResponseCh: make(chan *clientconn.Request, 1),
		}
		var pkgEncoded []byte
		pkgEncoded, err = json.Marshal(myCPPackage)
		if err != nil {
			err = fmt.Errorf("marshal fail=>%w", err)
			return
		}
		request.Pkg = util.Encrypt(pkgEncoded, password)
		client.clientConn.Send(request)

		// 处理响应
		request = <-request.ResponseCh
		if request.Err != nil {
			return fmt.Errorf("request.Err=>%w", request.Err)
		}
		var dycrypted []byte
		dycrypted, err = util.Decrypt(request.Pkg, password)
		if err != nil {
			return fmt.Errorf("Decrypt fail=>%w", err)
		}
		var rsp = &mycpproto.MyCPPackage{}
		err = json.Unmarshal(dycrypted, rsp)
		if err != nil {
			return fmt.Errorf("unmarshal fail=>%w", err)
		}
		if rsp.Status != mycpproto.MyCPPackageStatusSucc {
			return fmt.Errorf("remote execution fail")
		}

		var fileInfos []os.FileInfo
		fileInfos, err = ioutil.ReadDir(srcPath)
		if err != nil {
			log.Printf("ioutil.ReadDir fail=>%v", err)
			err = fmt.Errorf("ioutil.ReadDir fail=>%v", err)
			return
		}

		srcPathTrimmed := strings.TrimSuffix(srcPath, "/")
		for len(srcPathTrimmed) >= 2 && strings.HasSuffix(srcPathTrimmed, "/") {
			srcPathTrimmed = strings.TrimSuffix(srcPathTrimmed, "/")
		}

		_, srcPathLast := filepath.Split(srcPathTrimmed)
		newDstPath := fmt.Sprintf("%s/%s", dstPath, srcPathLast)
		for _, fileInfo := range fileInfos {
			newSrcPath := fmt.Sprintf("%s/%s", srcPath, fileInfo.Name())
			err = client.MyCPFromLocalToRemote(newSrcPath, newDstPath, onlyModified, lastMyCPTime, password)
			if err != nil {
				log.Printf("MyCPFromLocalToRemote fail=>%v", err)
				return
			}
		}
		return nil
	}
}

var MyCPInfoFileName = "mycp_info.txt"

func ReadMyCPInfo() (myCPInfo *mycpproto.MyCPInfo, err error) {
	binDir, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("os.Executable fail=>%w", err)
	}
	binDir, err = filepath.Abs(filepath.Dir(binDir))
	if err != nil {
		return nil, fmt.Errorf("fail to get bin dir=>%w", err)
	}
	myCPInfoFilePath := fmt.Sprintf("%s/%s", binDir, MyCPInfoFileName)
	myCPInfoFile, err := os.Open(myCPInfoFilePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("Open fail=>%w", err)
	}
	if err == nil {
		defer myCPInfoFile.Close()
	}

	myCPInfo = &mycpproto.MyCPInfo{}
	if err == nil {
		// 文件确实存在
		pkgSize := 1024 * 1024 * 100
		pkg := make([]byte, pkgSize)
		n, err := myCPInfoFile.Read(pkg)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("Read fail=>%w", err)
		}
		if n >= pkgSize {
			return nil, fmt.Errorf("fail=>MyCPInfo large over %d Bytes", pkgSize)
		}
		pkg = pkg[:n]
		if len(pkg) > 0 {
			err := json.Unmarshal(pkg, myCPInfo)
			if err != nil {
				return nil, fmt.Errorf("unmarshal fail=>%w", err)
			}
		}
	}
	if myCPInfo.Path2LastMyCPTime == nil {
		myCPInfo.Path2LastMyCPTime = make(map[string]time.Time)
	}
	return myCPInfo, nil
}

func WriteMyCPInfo(myCPInfo *mycpproto.MyCPInfo) (err error) {
	binDir, err := os.Executable()
	if err != nil {
		return fmt.Errorf("os.Executable fail=>%w", err)
	}
	binDir, err = filepath.Abs(filepath.Dir(binDir))
	if err != nil {
		return fmt.Errorf("fail to get bin dir=>%w", err)
	}
	myCPInfoFilePath := fmt.Sprintf("%s/%s", binDir, MyCPInfoFileName)
	log.Printf("myCPInfoFilePath=>%s", myCPInfoFilePath)
	myCPInfoFile, err := os.OpenFile(myCPInfoFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0664)
	if err != nil {
		return fmt.Errorf("Open fail=>%w", err)
	}
	defer myCPInfoFile.Close()
	pkg, err := json.Marshal(myCPInfo)
	if err != nil {
		return fmt.Errorf("Marshal fail=>%w", err)
	}
	_, err = myCPInfoFile.Write(pkg)
	if err != nil {
		return fmt.Errorf("Write fail=>%w", err)
	}
	return nil
}

// @172.0.0.1:D:/work/study/study-golang03/demos/mycp/tmp/e_dir/e_b_dir
func ParsePath(srcPath, dstPath, lastRemoteHost string) (realSrcPath, realDstPath, remoteHost string, remoteIsSrc bool, err error) {
	srcPath = strings.TrimSpace(srcPath)
	dstPath = strings.TrimSpace(dstPath)

	if strings.HasPrefix(srcPath, "@") {
		if strings.HasPrefix(dstPath, "@") {
			err = errors.New("only one remote host needed but two offered")
			return
		}
		if strings.HasPrefix(srcPath, "@R:") {
			if lastRemoteHost == "" {
				err = errors.New("no last remote host, specify it pls")
				return
			}
			remoteHost = lastRemoteHost
			realSrcPath = srcPath[3:]
			realDstPath = dstPath
			remoteIsSrc = true
			return
		} else {
			idx01 := strings.IndexRune(srcPath, ':')
			if idx01 == -1 {
				err = errors.New("need remote host but nil")
				return
			}
			idx02 := strings.IndexRune(srcPath[idx01+1:], ':')
			if idx02 == -1 {
				err = errors.New("need remote host but nil")
				return
			}
			idx := idx01 + idx02 + 1
			remoteHost = srcPath[1:idx]
			realSrcPath = srcPath[idx+1:]
			realDstPath = dstPath
			remoteIsSrc = true
			return
		}
	} else if strings.HasPrefix(dstPath, "@") {
		if strings.HasPrefix(dstPath, "@R:") {
			if lastRemoteHost == "" {
				err = errors.New("no last remote host, specify it pls")
				return
			}
			remoteHost = lastRemoteHost
			realSrcPath = srcPath
			realDstPath = dstPath[3:]
			remoteIsSrc = false
			return
		} else {
			idx01 := strings.IndexRune(dstPath, ':')
			if idx01 == -1 {
				err = errors.New("need remote host but nil")
				return
			}
			idx02 := strings.IndexRune(dstPath[idx01+1:], ':')
			if idx02 == -1 {
				err = errors.New("need remote host but nil")
				return
			}
			idx := idx01 + idx02 + 1
			remoteHost = dstPath[1:idx]
			realDstPath = dstPath[idx+1:]
			realSrcPath = srcPath
			remoteIsSrc = false
			return
		}
	} else {
		err = errors.New("need remote host but nil")
		return
	}
}
