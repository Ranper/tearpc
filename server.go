package tearpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"tearpc/codec" // 以最后一个/后面的内容作为imported 的name
	"time"
)

/*
纵观整个远程调用的过程，需要客户端处理超时的地方有：

与服务端建立连接，导致的超时
发送请求到服务端，写报文导致的超时
等待服务端处理时，等待处理导致的超时（比如服务端已挂死，迟迟不响应）
从服务端接收响应时，读报文导致的超时
需要服务端处理超时的地方有：

读取客户端请求报文时，读报文导致的超时
发送响应报文时，写报文导致的超时
调用映射服务的方法时，处理报文导致的超时
GeeRPC 在 3 个地方添加了超时处理机制。分别是：

1）客户端创建连接时
2）客户端 Client.Call() 整个过程导致的超时（包含发送报文，等待处理，接收报文所有阶段）
3）服务端处理报文，即 Server.handleRequest 超时。
*/

const DefaultMagicNumber = 0x8df2ce

// 通信伊始, 协商编码格式
/*
一般来说,设计协议协商的部分,需要设计固定长度的字节来传输.
这里为了简单, 使用json传输协议协商
*/
type Option struct {
	MagicNumber    int
	CodecType      codec.Type
	ConnectTimeout time.Duration // 0 means no limit
	HandleTimeout  time.Duration
}

// 提供的默认选项
var DefaultOption = &Option{ //? 所以这里使用指针的原因是?
	MagicNumber:    DefaultMagicNumber,
	CodecType:      codec.GobType,
	ConnectTimeout: time.Second * 10,
}

// 封装一次rpc调用, 固定参数服务名,方法名,错误封装在header里. 输入参数和返回参数在body
type request struct {
	Header          *codec.Header
	Argv, ReplyArgv reflect.Value // 这里是reflect.Value ,思考下为什么不能是Type //因为这里保存的是具体的值,后续要操作的,不是要使用类型
	mtype           *methodType   // 本次请求要调用的方法名
	svc             *service      // 本次请求要调用的服务名
}

type Server struct {
	serviceMap sync.Map
}

// 构造函数, go语言中的结构体没有构造函数, 需要自己实现
func NewServer() *Server {
	return &Server{}
}

var DefaultServer = NewServer()

// 不是for循环,不可以使用go ,否则父goroutine 退出了,子goroutine也会退出
func (s *Server) ServeConn(conn io.ReadWriteCloser) {
	defer func() { _ = conn.Close() }()
	// 读取option
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("Decode Json Opt Error: ", err.Error())
		return
	}

	if opt.MagicNumber != DefaultMagicNumber {
		log.Println("MagicNumber err: ", opt.MagicNumber)
		return
	}

	createCodecFunc := codec.NewCodecFuncMap[opt.CodecType]
	if createCodecFunc == nil {
		log.Println("Not Define CodecFuncType", opt.CodecType)
		return
	}
	log.Println("Server: Received Option! codec: ", opt.CodecType)
	s.serveCodec(createCodecFunc(conn), &opt) // 构造编码器,传入loop
}

var invalidRequest = struct{}{} // 初始化一个空结构体

type TestStruct struct {
	Num  uint64
	Name string
}

var testStruct = TestStruct{
	Num:  16,
	Name: "zhangsan",
}

// 三次握手, 发送opt选项之后, 在这里主循环, 接受 request && handle request
func (s *Server) serveCodec(cc codec.Codec, opt *Option) {
	// defer func() { _ = cc.Close() }() // 退出的时候关闭cc
	// 同一个conn 连接, 同一时间, 不同goroutine 只能有一个在写
	sending := &sync.Mutex{} // 每个conn 连接,对应一把锁
	wg := &sync.WaitGroup{}

	for {
		// 读取request
		req, err := s.readRequest(cc) // 当前协程只负责读区请求
		// test code
		// err = errors.New("test err")  // 打开这个注释, 会向客户端发送空结构体,客户端就不能再用string类型的变量去接收了
		if err != nil {
			// log.Println("Server: serveCodec: ", err, req)
			if req == nil { // header 解析失败,可以退出了 //! 为什么这里break, continue不行吗 //因为tcp是数据流,这里读取失败, 很可能已经发生了粘包,无法再找到下个请求的开始. 也可能是客户端退出了
				break
			}
			req.Header.Error = err.Error()
			s.sendResponse(cc, req.Header, invalidRequest, sending)
			continue
		}

		wg.Add(1)
		go s.handleRequest(cc, req, sending, wg, opt.HandleTimeout) // 处理请求和回复请求在其他协程, 所以每次在写数据的时候都需要加锁
	}
	wg.Wait() // 等所有的协程都处理完了,再关闭连接
	log.Println("conn close")
	cc.Close()

}

// 往cc 连接 发送header 和body, 发送前要申请 sengind mutex
func (s *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()

	if err := cc.Write(h, body); err != nil {
		log.Println("sendResponse: Write Error: ", err.Error())
		return
	}
}

func (s *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	// req.ReplyArgv = reflect.New(reflect.TypeOf(""))
	defer wg.Done()               // 走完整个处理流程后执行 wg.Done,defer是在本函数退出的时候才执行
	called := make(chan struct{}) // 防止在call之后,发送response的时候超时, 又发送了一次response
	sent := make(chan struct{})

	go func() {
		err := req.svc.call(req.mtype, req.Argv, req.ReplyArgv)
		called <- struct{}{} // 通知已经完成调用

		if err != nil {
			req.Header.Error = err.Error()
			s.sendResponse(cc, req.Header, invalidRequest, sending)
			sent <- struct{}{}
		}
		s.sendResponse(cc, req.Header, req.ReplyArgv.Interface(), sending)
		sent <- struct{}{}
	}()

	if timeout == 0 {
		<-called
		<-sent
		return
	}

	select {
	case <-time.After(timeout):
		req.Header.Error = fmt.Sprintf("rpc server: request handle timeout: expect within %s", timeout)
		s.sendResponse(cc, req.Header, invalidRequest, sending)
	case <-called:
		<-sent
	}

	/*
		// 直接调用对应服务的call 方法
		err := req.svc.call(req.mtype, req.Argv, req.ReplyArgv)
		if err != nil {
			req.Header.Error = err.Error()
			s.sendResponse(cc, req.Header, invalidRequest, sending)
			return
		}

		s.sendResponse(cc, req.Header, req.ReplyArgv.Interface(), sending)

		// req.ReplyArgv = reflect.ValueOf(fmt.Sprintf("TeaRpc Replay: %v", req.Header.Seq))
		// log.Printf("handleRequest: req.Seq=%d, argv=%v, Reply=%s", req.Header.Seq, req.Argv.Elem(), req.ReplyArgv)
		// // send response
		// s.sendResponse(cc, req.Header, req.ReplyArgv.Interface(), sending)
	*/
}

/*
读取请求
*/
func (s *Server) readRequest(cc codec.Codec) (*request, error) {
	// 读取head
	h, err := s.readRequsetHead(cc)
	if err != nil {
		return nil, err
	}
	req := &request{Header: h}
	req.svc, req.mtype, err = s.findServer(h.ServerMethod) //!这里如果出现问题了, body就不读了么??那下次再读的时候,不会粘包么
	if err != nil {
		return req, err
	}

	req.Argv = req.mtype.newArgv()
	req.ReplyArgv = req.mtype.newReplyv()

	argvi := req.Argv.Interface()
	// 确保argv是个指针, 如果不是, 则获取其指针形式
	if req.Argv.Type().Kind() != reflect.Ptr {
		argvi = req.Argv.Addr().Interface()
	}
	if err = cc.ReadBody(argvi); err != nil {
		log.Println("rpc server: read body err: ", err)
		return req, err
	}

	return req, nil

	// //! 这里的反射还得再学习
	// // 假设参数类型是字符串
	// req.Argv = reflect.New(reflect.TypeOf("")) // 假设是字符串类型的输入,创建一个string类型的
	// if err := cc.ReadBody(req.Argv.Interface()); err != nil {
	// 	log.Println("readRequest: ReadBody err: ", err.Error())
	// 	return req, err
	// }
	// // log.Println("Server receive argv:", req.Argv.Elem())
	// return req, nil
}

func (s *Server) readRequsetHead(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("ReadRequestHead : Read Head Error: ", err.Error(), h)
		}
		return nil, err
	}
	return &h, nil
}

// 服务器主循环, 不停的接收连接,并启动一个协程
func (s *Server) Accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		log.Println("Server: accpt a new connect: ", conn.RemoteAddr())

		if err != nil {
			log.Println("Accept Error: ", listener.Addr().String(), err.Error())
			continue
		}
		log.Println("Server: Accpt ", conn.RemoteAddr().String())
		go s.ServeConn(conn)
	}
}

func Accept(listener net.Listener) {
	DefaultServer.Accept(listener)
}

func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: serivce already defined: " + s.name)
	}
	return nil
}

func Register(rcvr interface{}) error { return DefaultServer.Register(rcvr) }

func (server *Server) findServer(serviceMthod string) (svc *service, mtype *methodType, err error) {
	dot := strings.LastIndex(serviceMthod, ".")
	if dot < 0 {
		err = errors.New("rpc service: service/method request ill-formed: " + serviceMthod)
		return
	}

	serviceName, methodName := serviceMthod[:dot], serviceMthod[dot+1:]
	svci, ok := server.serviceMap.Load(serviceName) // Load函数返回的是interface, 需要断言
	if !ok {
		err = errors.New("rpc service: can't find service " + serviceName)
		return
	}
	svc = svci.(*service) // 断言为对应的服务指针
	mtype = svc.method[methodName]
	if mtype == nil { // mtype是个指针类型
		err = errors.New("rpc service: can't find method " + methodName)
		return
	}
	return
}

// region 服务端新增支持HTTP协议 begion
const (
	connected        = "200 Connected to Gee RPC"
	defaultRPCPath   = "/_geerpc_"
	defaultDebugPath = "/debug/geerpc"
)

func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "CONNECT" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, "405 must CONNECT\n")
		return
	}

	conn, _, err := w.(http.Hijacker).Hijack() // 断言
	if err != nil {
		log.Print("rpc hijacking ", req.RemoteAddr, ": ", err.Error())
		return
	}

	_, _ = io.WriteString(conn, "HTTP/1.0"+connected+"\n\n")
	s.ServeConn(conn)
}

// 注册处理函数, s 需实现接口
//
//	type Handler interface {
//		ServeHTTP(ResponseWriter, *Request)
//	}
func (s *Server) handleHTTP() {
	http.Handle(defaultRPCPath, s)
}

func HandleHTTP() {
	DefaultServer.handleHTTP()
}

//#endregion  服务端新增支持HTTP协议
