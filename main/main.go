package main

import (
	"fmt"
	"log"
	"net"
	"tearpc"
	"time"
)

func startServer(addr chan string) {
	listen_addr := ":8000"
	listener, err := net.Listen("tcp", listen_addr)
	if err != nil {
		log.Println("Start server failed: ", err.Error())
		return
	}
	log.Println("Server: Listen on: ", listener.Addr().String())
	addr <- listen_addr
	tearpc.Accept(listener)
	log.Println("ServerFinish")
}

func main() {
	addr := make(chan string, 1)
	go startServer(addr)

	time.Sleep(time.Second)
	client := tearpc.Dial("tcp", addr)

	for i := 1; i < 10; i++ {
		var reply string // 每次发请求的时候,清空

		call := &tearpc.Call{
			ServerMethod: "Foo.Sum",
			Argv:         fmt.Sprintf("input of call [%v]", i),
			Reply:        &reply, // 这传入返回类型的地址
		}
		log.Println("Client: begin to call! idx=", i)
		client.Call(call) // 这里是阻塞的
		log.Printf("i=%d, reply=%v", i, reply)
	}
}
