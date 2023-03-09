package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"reflect"
	"strconv"
	"tearpc"
	"tearpc/codec"
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

	conn, err := net.Dial("tcp", <-addr)
	if err != nil {
		log.Println("Start server failed: ", err.Error())
		return
	}
	log.Println("Client Dial success: ", conn.RemoteAddr().String())
	time.Sleep(time.Second)
	if err = json.NewEncoder(conn).Encode(tearpc.DefaultOption); err != nil {
		log.Println("Client: Send opt: err: ", err.Error())
	}

	coder := codec.CodccMap[tearpc.DefaultOption.CodecType](conn)
	// go client_receive(coder)
	for i := 1; i < 10; i++ {
		var h = &codec.Header{
			ServerMethod: "Calc.Sum",
			Seq:          uint64(i),
		}
		argv := reflect.ValueOf(fmt.Sprintf("input argv is %s", strconv.Itoa(i)))
		coder.Write(h, argv.Interface())

		var reply string
		// reply = reflect.ValueOf("")
		_ = coder.ReadHead(h) // fuck,这里忘记读 head了
		_ = coder.ReadBody(&reply)
		log.Println("Receive: head: ", h, ", body: ", reply)
	}
	time.Sleep(5 * time.Second)
}
