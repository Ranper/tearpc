package codec

import "io"

/*
err = client.Call("Arith.Multiply", args, &reply)
五个元素:
客户端三个: 服务名,方法名, 参数(args)
服务端两个: 错误(err), 返回值(replay)
将服务名, 方法名, 错误信息封装到header. 这部分格式较为固定
将参数和返回值封装到body里面
*/

type Header struct {
	ServerMethod string // format "Service.Method"
	Seq          uint64 // sequence number chosen by client
	Error        string //
}

// 对消息体进行辩解吗的接口Codec, 抽象出接口是为了实现不同的Codec实例 比如 gob, json
// 主要包括关闭、读head, 读body, 写(head,body)
type Codec interface {
	io.Closer                 // 必须包含Close方法
	ReadHeader(*Header) error // 接口可以不写变量名字, 直接写类型
	ReadBody(interface{}) error
	Write(*Header, interface{}) error // 写入, header 和 body
}

// 抽象出codec的构造函数, 指定一个编码类型, 返回其对应的构造函数,跟工厂模式类似,只不过是返回构造函数,而不是具体实例
type Type string

const (
	GobType  Type = "application/gob"
	JsonType Type = "application/json"
)

type NewCodecFunc func(io.ReadWriteCloser) Codec

// 将type 映射为对应的 New函数
var NewCodecFuncMap map[Type]NewCodecFunc //这里是声明

// 自动执行一次
func init() {
	NewCodecFuncMap = make(map[Type]NewCodecFunc) // 这里是初始化
	NewCodecFuncMap[GobType] = NewGobCodec        // 定义在其他文件中, 需导出(首字母大写)
}
