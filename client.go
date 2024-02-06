package tearpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
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
	Error        error
	Done         chan *Call
}

// 当一次 call调用接收到rpc的时候,调用done函数,向chan 发送消息,标识已经完成
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

// client的构造函数
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

// NewClient 根据opt, 在已经建立连接的socket上建立client对象
/*
	1、选择编码方式
	2、跟服务器协商通信编码(向服务器发送数据)
	3、创建Client结构体
*/
func NewClient(conn net.Conn, opt *Option) (client *Client, err error) {
	createCodecFunc := codec.NewCodecFuncMap[opt.CodecType]
	if createCodecFunc == nil {
		err := fmt.Errorf("NewClient: invalid codec type %s", opt.CodecType)
		log.Println("rpc client: codec err: ", err)
		return nil, err
	}
	// 经过确认 opt没问题了再发送
	_ = json.NewEncoder(conn).Encode(opt) // 发送option
	log.Println("Client: send opt done!")

	// newClientCodec 不可能出问题
	return newClientCodec(createCodecFunc((conn)), opt), nil
}

func funcTimeCost() func(string) {
	beginTime := time.Now()
	return func(fn string) {
		log.Printf("func time stats: %s|%v", fn, time.Since(beginTime))
	}
}

// Go 封装异步调用
// 在内部构造 call 结构体
func (c *Client) Go(ServerMethon string, argv, reply interface{}, done chan *Call) *Call {
	// 因为使用了有缓冲的channel, 所以是非阻塞的
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

/*
// 阻塞调用
func (c *Client) Call(serviceMethod string, args, reply interface{}) error {
	// if call.Done == nil {
	// 	call.Done = make(chan *Call, 10)
	// }
	defer funcTimeCost()(fmt.Sprintf("call: %s, Argv=%d", serviceMethod, args))
	// 当前call没有收到回报的时候,会阻塞在当前语句, 直到调用done()函数,向通道写入内容
	// <-c.send(call).Done // 当receive 接收到 返回值的时候, 会向Done 通道写消息来通知,然后这里就可以返回了; 如果是异步的话,可以返回一个channel

	call := <-c.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
}
*/
/*
用户可以使用 context.WithTimeout 创建具备超时检测能力的 context 对象来控制。例如：
	ctx, _ := context.WithTimeout(context.Background(), time.Second)
	var reply int
	err := client.Call(ctx, "Foo.Sum", &Args{1, 2}, &reply)
	...
*/

func (c *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	call := c.Go(serviceMethod, args, reply, make(chan *Call, 1)) // 非阻塞
	// 看看超时和rpc调用哪个先完成
	select {
	case <-ctx.Done():
		c.removeCall(call.Seq)
		log.Println("client Call timeout: call.Seq = ", call.Seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case _call := <-call.Done: // 这里可能会名字冲突
		return _call.Error
	}
}

func Done(call *Call) {
	<-call.Done // wait call finish
}

func (c *Client) removeCall(seqId uint64) *Call {
	c.mu.Lock() // 操作共享资源, 加锁
	defer c.mu.Unlock()
	call := c.pending[seqId] // 从pending列表里面删除对应的序列号
	delete(c.pending, seqId)
	log.Printf("remove %v, call == nil ? %v, client = %p", seqId, call == nil, c)
	return call
}

// 读取 cc的消息
func receive(client *Client, cc codec.Codec) {
	var err error
	for err == nil {
		var header codec.Header
		// log.Println("receive  run")
		err = cc.ReadHeader(&header)
		if err != nil {
			// log.Println("Client receive: ReadHeader err:", err.Error())
			// continue // 接收头部有问题,直接break
			break
		}

		// call := client.pending[header.Seq] //? Note:收到请求后这里要删除对应的call, 否则内存无法释放
		call := client.removeCall(header.Seq)

		switch { // swich 是可以不带表达式的,直接在case里面判断
		case call == nil:
			log.Printf("call [seq = %v] is not in client.pending %p", header.Seq, client)
			err = cc.ReadBody(nil)
		case header.Error != "":
			log.Printf("receive: ReadBody: err: %v", header.Error)

			/*
				2023/03/11 17:00:32 receive: ReadHeader err: gob: type mismatch in decoder: want struct type codec.Header; got non-struct
				2023/03/11 17:00:32 Client: encounter error:  gob: type mismatch in decoder: want struct type codec.Header; got non-struct

				当客户端试图读取的类型与服务器写入的类型不一致时
			*/
			call.Error = fmt.Errorf(header.Error) // 将error通过string传输,现在再转换为对应的error
			err = cc.ReadBody(nil)
			call.done()
		case header.Error == "":
			log.Printf("receive: ReadBody Error = empty")
			err = cc.ReadBody(call.Reply) //从body中读取数据到 call.replay中
			call.done()
		}
	}
	// log.Println("Client: encounter error: ", err.Error())
	client.terminalClient(err)
}

var ErrShutDown = errors.New("Client ShutDown")

func (c *Client) terminalClient(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, call_ptr := range c.pending {
		call_ptr.Error = err
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
	c.header.Error = ""
	c.header.ServerMethod = call.ServerMethod
	// 注意, 这里只发送了 header 和 argv 参数, 服务器在读取的时候也只需要读这两部分就好了
	if err := c.cc.Write(c.header, call.Argv); err != nil {
		log.Println("Client Write err: ", err.Error())
		call := c.removeCall(seq) // 这里发送失败要立即通知调用方哦
		if call != nil {
			call.Error = err
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
	log.Printf("registerCall client.seq=%v, client.seq=%v call==nil ? %v client=%p", call.Seq, c.seq, call == nil, c)

	return call.Seq, nil
}

func parseOptions(opts ...*Option) (*Option, error) {
	// 没传参数,或者传递的第一个参数为nil
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}

	opt := opts[0]
	opt.MagicNumber = DefaultMagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	return opt, nil
}

/*
// dial 服务器, 发送opt
func Dial(work string, addr chan string, opts ...*Option) (client *Client, err error) {
	option, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}
	// 这里可能会超时
	conn, err := net.Dial(work, <-addr)
	if err != nil {
		log.Println("Dial failed")
		return nil, err
	}
	log.Println("Dial succedd! ", conn.RemoteAddr().String())

	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	// 这里也可能会超时
	return NewClient(conn, option)
}
*/

func Dial(network, address string, opts ...*Option) (*Client, error) {
	return dialTimeout(NewClient, network, address, opts...)
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return ErrShutDown
	}
	c.closing = true
	return c.cc.Close()
}

// day4 超时

// 将要传递的数据封装为一个结构体
type clientResult struct {
	client *Client
	err    error
}

type newClientFunc func(conn net.Conn, opt *Option) (client *Client, err error)

/*
https://geektutu.com/post/geerpc-day4.html
带超时的建立连接
1、解析option
2、带超时的建立连接 DialTimeout
3、使用通道实现NewClient的超时(主要是发送协商数据)
*/
func dialTimeout(f newClientFunc, network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialTimeout(network, address, opt.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	// 此时已经建立连接, 如果后续出什么错误, 要关闭连接
	defer func() {
		if err != nil {
			_ = conn.Close() // 强调ignore错误
		}
	}()

	ch := make(chan clientResult)
	go func() {
		client, err = f(conn, opt)
		ch <- clientResult{client: client, err: err} // 通过chan通知创建client的结果
	}()

	if opt.ConnectTimeout == 0 { // 如果是没有超时, 直接读取ch即可. 另一个协程写入成功了这里即可读取到,然后返回
		result := <-ch
		return result.client, result.err
	}

	// 创建客户端超时
	select { // 没有default语句,select 语句会阻塞,直到其中一个case 为真,如果同时为真, 随机选择一个
	case <-time.After(opt.ConnectTimeout):
		return nil, fmt.Errorf("rpc client: connect timeout: expect within %s", opt.ConnectTimeout)
	case result := <-ch:
		return result.client, result.err
	}
}

// NewHTTPClient new a Client instance via HTTP as transport protocol
func NewHTTPClient(conn net.Conn, opt *Option) (*Client, error) {
	_, _ = io.WriteString(conn, fmt.Sprintf("Connect %s HTTP/1.0\n\n", defaultRPCPath))

	// Require successful HTTP response
	// before swithing to RPC protocol.
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err == nil && resp.Status == connected {
		return NewClient(conn, opt)
	}
	if err == nil {
		err = errors.New(("unexpected HTTP response: " + resp.Status))
	}

	return nil, err
}

// DialHTTP connects to an HTTP RPC server at the specified network
// address listening on the default HTTP RPC path.
func DialHTTP(network, address string, opts ...*Option) (*Client, error) {
	return dialTimeout(NewHTTPClient, network, address, opts...)
}

func XDial(rpcAddr string, opts ...*Option) (*Client, error) {
	parts := strings.Split(rpcAddr, "@")
	if len(parts) != 2 {
		return nil, fmt.Errorf("rpc client err: wrong format '%s', expect protocol@addr", rpcAddr)
	}

	protocol, addr := parts[0], parts[1]
	switch protocol {
	case "http":
		return DialHTTP("tcp", addr, opts...)
	default:
		// tcp. unix or other transport protocol
		return Dial(protocol, addr, opts...)
	}
}
