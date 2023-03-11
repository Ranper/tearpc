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

/*
TODO
1、需要增加sync.Wait, 在连接发生错误的时候,要等所有协程处理完成后再关闭连接
2、sending的时候增加mutex保护
*/
type Option struct {
	MagicNumber uint32
	CodecType   codec.Type
}

var DefaultMagicNumber uint32 = 0x12345678

// 提供的默认选项
var DefaultOption = Option{
	MagicNumber: DefaultMagicNumber,
	CodecType:   codec.GobCodecType,
}

type Request struct {
	Header          *codec.Header
	Argv, ReplyArgv reflect.Value // 这里是reflect.Value ,思考下为什么不能是Type
}

type Server struct{}

// 构造函数
func NewServer() *Server {
	return &Server{}
}

var DefaultServer = NewServer()

// 不是for循环,不可以使用go ,否则父goroutine 退出了,子goroutine也会退出
func (s *Server) NewConnAccepted(conn io.ReadWriteCloser) {
	var opt Option
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("Decode Json Opt Err: ", err.Error())
		return
	}

	if opt.MagicNumber != DefaultMagicNumber {
		log.Println("MagicNumber err: ", opt.MagicNumber)
		return
	}

	createCodecFunc := codec.CodccMap[opt.CodecType]
	if createCodecFunc == nil {
		log.Println("Not Define CodecFuncType", opt.CodecType)
		return
	}
	log.Println("Server: Received Option!")
	s.ServerConnLoop(createCodecFunc(conn))
	// s.ReadRequest(createCodecFunc(conn))
}

var invalidRequest = struct{}{} // 初始化一个空结构体

// 三次握手, 发送opt选项之后, 在这里主循环, 接受 Request && handle request
func (s *Server) ServerConnLoop(cc codec.Codec) {
	// defer func() { _ = cc.Close() }() // 退出的时候关闭cc
	// 同一个conn 连接, 同一时间, 不同goroutine 只能有一个在写
	sending := &sync.Mutex{}
	wg := &sync.WaitGroup{}
	for {
		req, err := s.ReadRequest(cc)
		if err != nil {
			if req == nil { // header 解析失败,可以退出了 //! 为什么这里break, continue不行吗
				break
			}
			req.Header.Err = err.Error()
			s.SendResponse(cc, req.Header, invalidRequest, sending)
			continue
		}
		wg.Add(1)
		go s.HandleRequest(cc, req, wg, sending)
	}
	wg.Wait() // 等所有的协程都处理完了,再关闭连接
	cc.Close()

}

// 往cc 连接 发送header 和body, 发送前要申请 sengind mutex
func (s *Server) SendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()

	if err := cc.Write(h, body); err != nil {
		log.Println("SendResponse: Write Err: ", err.Error())
		return
	}
}

func (s *Server) HandleRequest(cc codec.Codec, req *Request, wg *sync.WaitGroup, sending *sync.Mutex) {
	// req.ReplyArgv = reflect.New(reflect.TypeOf(""))
	defer wg.Done() // 走完整个处理流程后执行 wg.Done,defer是在本函数退出的时候才执行

	req.ReplyArgv = reflect.ValueOf(fmt.Sprintf("TeaRpc Replay: %v", req.Header.Seq))
	log.Printf("HandleRequest: req.Seq=%d, Reply=%s", req.Header.Seq, req.ReplyArgv)
	// send response
	s.SendResponse(cc, req.Header, req.ReplyArgv.Interface(), sending)
}

/*
读取请求
*/
func (s *Server) ReadRequest(cc codec.Codec) (*Request, error) {
	h, err := s.ReadRequsetHead(cc)
	if err != nil {
		return nil, err
	}
	req := &Request{Header: h}
	// req.Argv = reflect.TypeOf("") // 假设是字符串类型的输入
	req.Argv = reflect.New(reflect.TypeOf("")) // 假设是字符串类型的输入,创建一个string类型的

	// 这里也对readbody进行封装  //? 作者没封装,那我也不封装了
	if err := cc.ReadBody(req.Argv.Interface()); err != nil {
		log.Println("ReadRequest: ReadBody err: ", err.Error())
		return req, err
	}

	return req, nil
}

func (s *Server) ReadRequsetHead(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHead(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("ReadRequestHead : Read Head Err: ", err.Error())
		}
		return nil, err
	}
	return &h, nil
}

func (s *Server) Accept(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		log.Println("Server: accpt a new connect: ", conn.RemoteAddr())

		if err != nil {
			log.Println("Accept Err: ", listener.Addr().String(), err.Error())
			continue
		}
		log.Println("Server: Accpt ", conn.RemoteAddr().String())
		go s.NewConnAccepted(conn)
	}
}

func Accept(listener net.Listener) {
	DefaultServer.Accept(listener)
}
