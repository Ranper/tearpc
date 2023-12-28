package tearpc

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"reflect"
	"sync"
	"tearpc/codec" // 以最后一个/后面的内容作为imported 的name
)

const DefaultMagicNumber = 0x8df2ce

// 通信伊始, 协商编码格式
/*
一般来说,设计协议协商的部分,需要设计固定长度的字节来传输.
这里为了简单, 使用json传输协议协商
*/
type Option struct {
	MagicNumber int
	CodecType   codec.Type
}

// 提供的默认选项
var DefaultOption = &Option{ //? 所以这里使用指针的原因是?
	MagicNumber: DefaultMagicNumber,
	CodecType:   codec.GobType,
}

// 封装一次rpc调用, 固定参数服务名,方法名,错误封装在header里. 输入参数和返回参数在body
type Request struct {
	Header          *codec.Header
	Argv, ReplyArgv reflect.Value // 这里是reflect.Value ,思考下为什么不能是Type
}

type Server struct{}

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
	s.serveCodec(createCodecFunc(conn)) // 构造编码器,传入loop
	// s.readRequest(createCodecFunc(conn))
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

// 三次握手, 发送opt选项之后, 在这里主循环, 接受 Request && handle request
func (s *Server) serveCodec(cc codec.Codec) {
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
		go s.handleRequest(cc, req, wg, sending) // 处理请求和回复请求在其他协程, 所以每次在写数据的时候都需要加锁
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

func (s *Server) handleRequest(cc codec.Codec, req *Request, wg *sync.WaitGroup, sending *sync.Mutex) {
	// req.ReplyArgv = reflect.New(reflect.TypeOf(""))
	defer wg.Done() // 走完整个处理流程后执行 wg.Done,defer是在本函数退出的时候才执行

	req.ReplyArgv = reflect.ValueOf(fmt.Sprintf("TeaRpc Replay: %v", req.Header.Seq))
	log.Printf("handleRequest: req.Seq=%d, argv=%v, Reply=%s", req.Header.Seq, req.Argv.Elem(), req.ReplyArgv)
	// send response
	s.sendResponse(cc, req.Header, req.ReplyArgv.Interface(), sending)
}

/*
读取请求
*/
func (s *Server) readRequest(cc codec.Codec) (*Request, error) {
	// 读取head
	h, err := s.readRequsetHead(cc)
	if err != nil {
		return nil, err
	}
	req := &Request{Header: h}
	//! 这里的反射还得再学习
	// 假设参数类型是字符串
	req.Argv = reflect.New(reflect.TypeOf("")) // 假设是字符串类型的输入,创建一个string类型的
	if err := cc.ReadBody(req.Argv.Interface()); err != nil {
		log.Println("readRequest: ReadBody err: ", err.Error())
		return req, err
	}
	// log.Println("Server receive argv:", req.Argv.Elem())
	return req, nil
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
