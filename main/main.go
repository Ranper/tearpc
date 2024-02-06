package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"sync"
	"tearpc"
	"time"
)

type Foo int
type Args struct{ Num1, Num2 int }

func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func startServer(addr chan string) {
	var foo Foo
	if err := tearpc.Register(&foo); err != nil {
		log.Fatal("register error")
	}

	l, err := net.Listen("tcp", ":9999")
	if err != nil {
		log.Println("Start server failed: ", err.Error())
		return
	}
	log.Println("Server: Listen on: ", l.Addr().String())
	addr <- l.Addr().String()
	// tearpc.Accept(l)
	tearpc.HandleHTTP() // day5
	_ = http.Serve(l, nil)

}

/*
func main() {
	log.SetFlags(0)
	addr := make(chan string)
	go startServer(addr)

	time.Sleep(time.Second)
	// client, _ := tearpc.Dial("tcp", addr)
	client, err := tearpc.DialHTTP("tcp", <-addr)
	if err != nil {
		log.Printf("tearpc.DialHTTP failed: %s", err)
		return
	}
	defer func() { _ = client.Close() }()

	var wg sync.WaitGroup
	for i := 1; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var reply int // 每次发请求的时候,清空

			args := Args{Num1: i, Num2: i * i * i}
			if err := client.Call(context.Background(), "Foo.Sum", args, &reply); err != nil { // 这里是阻塞的
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Printf("Client: receive reply: %d + %d = %d", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()

}
*/

func call(addrCh chan string) {
	client, _ := tearpc.DialHTTP("tcp", <-addrCh)
	defer func() { _ = client.Close() }()

	time.Sleep(time.Second)
	// send request & receive response
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := &Args{Num1: i, Num2: i * i}
			var reply int
			if err := client.Call(context.Background(), "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error:", err)
			}
			log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()
}

func main() {
	log.SetFlags(0)
	ch := make(chan string)
	go call(ch)
	startServer(ch)
}
