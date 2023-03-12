package tearpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"tearpc/codec"
	"time"
)

// 封装一次rpc call
type Call struct {
	Seq          uint64 // 这次call的序列号, client客户端赋值,用户无需care
	ServerMethod string // user assignment // 赋值
	Argv         interface{}
	Reply        interface{}
	Error        string
	Done         chan *Call
}

// 当一次 call调用接收到rps的时候,调用done函数,向chan 发送消息,标识已经完成
func (c *Call) done() {
	c.Done <- c
}

type Client struct {
	cc       codec.Codec
	sending  *sync.Mutex
	opt      *Option
	header   *codec.Header
	mu       *sync.Mutex
	seq      uint64 // 内部使用,不导出
	pending  map[uint64]*Call
	closing  bool
	shutdown bool
}

func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		cc:       cc,
		sending:  &sync.Mutex{},
		opt:      opt,
		header:   &codec.Header{},
		mu:       &sync.Mutex{},
		seq:      1,
		pending:  make(map[uint64]*Call),
		closing:  false,
		shutdown: false,
	}

	// 这里可以开始receive 消息了
	go receive(client, cc)
	return client
}

// 根据opt 创建codec,
func NewClient(conn io.ReadWriteCloser, opt *Option) *Client {
	createCodecFunc := codec.CodccMap[opt.CodecType]
	if createCodecFunc == nil {
		log.Println("NewClient: ERR: createCodecFunc is nil " + opt.CodecType)
		return nil
	}
	// 经过确认 opt没问题了再发送
	_ = json.NewEncoder(conn).Encode(opt) // 发送option

	return newClientCodec(createCodecFunc((conn)), opt)
}

func funcTimeCost() func(string) {
	beginTime := time.Now()
	return func(fn string) {
		log.Printf("func time stats: %s|%v", fn, time.Since(beginTime))
	}
}

// 封装异步调用
// 在内部构造 call 结构体
func (c *Client) Go(ServerMethon string, argv, reply interface{}, done chan *Call) *Call {
	if done == nil {
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		log.Panic("Client: done channel is unbuffered")
	}
	call := &Call{
		ServerMethod: ServerMethon,
		Argv:         argv,
		Reply:        reply,
		Done:         done,
	}
	c.send(call)
	return call
}

func (c *Client) Call(call *Call) {
	if call.Done == nil {
		call.Done = make(chan *Call, 10)
	}
	defer funcTimeCost()(fmt.Sprintf("call: %s, Argv=%d", call.ServerMethod, call.Argv))
	<-c.send(call).Done // 当receive 接收到 返回值的时候, 会向Done 通道写消息来通知,然后这里就可以返回了; 如果是异步的话,可以返回一个channel
}

func Done(call *Call) {
	<-call.Done // wait call finish
}

func (c *Client) removeCall(seqId uint64) *Call {
	c.mu.Lock()
	defer c.mu.Unlock()
	call := c.pending[seqId]
	delete(c.pending, seqId)
	return call
}

// 读取 cc的消息
func receive(client *Client, cc codec.Codec) {
	var err error
	for err == nil {
		var header codec.Header
		// log.Println("receive  run")
		err = cc.ReadHead(&header)
		if err != nil {
			log.Println("receive: ReadHead err:", err.Error())
			// continue // 接收头部有问题,直接break
			break
		}

		log.Println("receive head", header.Seq)
		// call := client.pending[header.Seq] //? Note:收到请求后这里要删除对应的call, 否则内存无法释放
		call := client.removeCall(header.Seq)

		switch { // swich 是可以不带表达式的,直接在case里面判断
		case call == nil:
			log.Printf("call [is = %v] is not in client.pending", header.Seq)
			err = cc.ReadBody(nil)
		case header.Err != "":
			log.Printf("receive: ReadBody: err: %v", header.Err)

			/*
				2023/03/11 17:00:32 receive: ReadHead err: gob: type mismatch in decoder: want struct type codec.Header; got non-struct
				2023/03/11 17:00:32 Client: encounter error:  gob: type mismatch in decoder: want struct type codec.Header; got non-struct

				当客户端试图读取的类型与服务器写入的类型不一致时
			*/
			err = cc.ReadBody(nil)
			call.done()
		case header.Err == "":
			err = cc.ReadBody(call.Reply) //从body中读取数据到 call.replay中
			log.Println("Client: No Err: Reply: **, head.req", header.Seq)
			call.done()
		}
	}
	// log.Println("Client: encounter error: ", err.Error())
	client.terminalClient()
}

var ErrShutDown = errors.New("Client ShutDown")

func (c *Client) terminalClient() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, call_ptr := range c.pending {
		call_ptr.Error = ErrShutDown.Error()
		call_ptr.done()
	}

}

// send
// 注册 我要发送的 rpc, 到时候还要收到回包
// 执行真正的发送流程

// send的作用是把call发送出去
func (c *Client) send(call *Call) *Call {
	c.sending.Lock()
	defer c.sending.Unlock()

	seq, _ := c.registerCall(call) // 因为header结构每个call 可以复用,所以把header放在了client上

	c.header.Seq = seq
	c.header.Err = ""
	c.header.ServerMethod = call.ServerMethod

	if err := c.cc.Write(c.header, call.Argv); err != nil {
		log.Println("Client Write err: ", err.Error())
		call := c.removeCall(seq) // 这里发送失败要立即通知调用方哦
		if call != nil {
			call.Error = err.Error()
			call.done()
		}
	}
	return call
}

// 注册一个 call, 给call 分配一个seq, 标识这个call 已经发送了,在等待接受rsp
func (c *Client) registerCall(call *Call) (uint64, error) {
	// if c.pending[call.Seq] != nil {
	// 	log.Println("Error: Req is existed: " + strconv.Itoa(int(call.Seq)))
	// 	return
	// }
	c.mu.Lock()
	defer c.mu.Unlock()

	// 如果关闭了,就停止发送
	// todo

	call.Seq = c.seq
	c.seq++
	c.pending[call.Seq] = call

	return call.Seq, nil
}

func parseOption() (Option, error) {
	return DefaultOption, nil
}

// dial 服务器, 发送opt
func Dial(work string, addr chan string, opt ...*Option) *Client {
	option, _ := parseOption()

	conn, err := net.Dial(work, <-addr)
	if err != nil {
		log.Println("Dial failed")
		return nil
	}
	log.Println("Dial succedd! ", conn.RemoteAddr().String())

	defer func() {
		if err != nil {
			conn.Close()
		}
	}()

	return NewClient(conn, &option)
}
