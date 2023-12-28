package main

import (
	"fmt"
	"log"
	"net"
	"sync"
	"tearpc"
	"time"
)

func startServer(addr chan string) {
	listen_addr := ":0" // 这里不要固定使用一个端口, 否则频繁启动的时候会有问题
	listener, err := net.Listen("tcp", listen_addr)
	if err != nil {
		log.Println("Start server failed: ", err.Error())
		return
	}
	log.Println("Server: Listen on: ", listener.Addr().String())
	addr <- listener.Addr().String()
	tearpc.Accept(listener)
	log.Println("ServerFinish")
}

func main() {
	addr := make(chan string, 1)
	go startServer(addr)

	time.Sleep(time.Second)
	client, _ := tearpc.Dial("tcp", addr)
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	for i := 1; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var reply string // 每次发请求的时候,清空

			client.Call("Foo.Sum", fmt.Sprintf("Test Argv of call [%v]", i), &reply) // 这里是阻塞的
			log.Printf("i=%d, reply=%v", i, reply)
		}(i)
	}
	wg.Wait()

}
