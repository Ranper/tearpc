package codec

import "io"

type Header struct {
	ServerMethod string
	Seq          uint64
	Err          string
}

type Codec interface {
	io.Closer               // 必须包含Close方法
	ReadHead(*Header) error // 接口可以不写变量名字, 直接写类型
	ReadBody(interface{}) error
	Write(*Header, interface{}) error // 写入, header 和 body
}
type Type string

const (
	GobCodecType  Type = "gob"
	JsonCodecType Type = "json"
)

type NewCodecFunc func(io.ReadWriteCloser) Codec

// 将type 映射为对应的 New函数
var CodccMap map[Type]NewCodecFunc //这里是声明

func init() {
	CodccMap = make(map[Type]NewCodecFunc) // 这里是初始化
	CodccMap[GobCodecType] = NewGobCodec
}
