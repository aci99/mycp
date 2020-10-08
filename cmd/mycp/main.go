package main

import (
	"flag"
	"fmt"
	"log"
	"mycp/mycpclient"
	"time"
)

var (
	srcPath      = flag.String("src", "@10.252.156.170:31001:D:/work/study/study-golang03/demos/mycp/tmp/a_dir/", "src path")
	dstPath      = flag.String("dst", "D:/work/study/study-golang03/demos/mycp/tmp/b_dir/", "dst path")
	onlyModified = flag.Bool("modified", false, "only cp modified files")
	password     = flag.String("password", "OarTkJdFdjYzLEjS", "password")
)

func MyCP() {
	var err error

	// 读 MyCPInfo
	myCPInfo, err := mycpclient.ReadMyCPInfo()
	if err != nil {
		log.Fatalf("ReadMyCPInfo fail=>%v", err)
	}

	realSrcPath, realDstPath, remoteHost, remoteIsSrc, err := mycpclient.ParsePath(*srcPath, *dstPath, myCPInfo.LastRemoteHost)
	if err != nil {
		log.Fatalf("ParsePath fail=>%v", err)
	}

	var client *mycpclient.Client
	client, err = mycpclient.NewClient(remoteHost)
	if err != nil {
		log.Fatalf("NewClient fail=>%v", err)
	}
	defer client.Close()

	var hostSrcPath string
	if remoteIsSrc {
		hostSrcPath = fmt.Sprintf("@%s:%s", remoteHost, realSrcPath)
	} else {
		hostSrcPath = realSrcPath
	}

	thisMyCPTime := time.Now()
	if remoteIsSrc {
		// 执行 MyCPFromRemoteToLocal
		log.Printf("MyCPFromRemoteToLocal start")
		err = client.MyCPFromRemoteToLocal(realSrcPath, realDstPath, *onlyModified, myCPInfo.Path2LastMyCPTime[hostSrcPath], *password)
		if err != nil {
			log.Fatalf("MyCPFromRemoteToLocal fail=>%v", err)
		}
		log.Printf("MyCPFromRemoteToLocal done.")
	} else {
		// 执行 MyCPFromLocalToRemote
		log.Printf("MyCPFromLocalToRemote start")
		err = client.MyCPFromLocalToRemote(realSrcPath, realDstPath, *onlyModified, myCPInfo.Path2LastMyCPTime[hostSrcPath], *password)
		if err != nil {
			log.Fatalf("MyCPFromLocalToRemote fail=>%v", err)
		}
		log.Printf("MyCPFromLocalToRemote done.")
	}

	// 更新 MyCPInfo
	myCPInfo.Path2LastMyCPTime[hostSrcPath] = thisMyCPTime
	myCPInfo.LastRemoteHost = remoteHost
	err = mycpclient.WriteMyCPInfo(myCPInfo)
	if err != nil {
		log.Fatalf("WriteMyCPInfo fail=>%v", err)
	}

	return
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds)
	flag.Parse()
	MyCP()
}
