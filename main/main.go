package main

import (
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
	var reply string
	call := &tearpc.Call{
		ServerMethod: "Foo.Sum",
		Argv:         "666",
		Reply:        &reply, // 这传入返回类型的地址
	}
	log.Println("Client: begin to call!")
	client.Call(call) // 这里是阻塞的
	log.Println("reply:", reply)
}
