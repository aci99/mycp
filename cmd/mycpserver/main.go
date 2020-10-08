package main

import (
	"flag"
	"log"
	"mycp/mycpserver"
)

var (
	host = flag.String("host", "0.0.0.0:31001", "ip:port")
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile | log.Lmicroseconds)
	flag.Parse()
	server := mycpserver.NewServer()
	err := server.LoadPassword()
	if err != nil {
		log.Fatalf("LoadPassword fail=>%v", err)
	}
	log.Printf("password=>\"%s\"", server.Password)
	_ = server.Start(*host)
}
