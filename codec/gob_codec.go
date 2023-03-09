package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

type GobCodec struct {
	conn io.ReadWriteCloser
	buf  *bufio.Writer
	enc  *gob.Encoder
	dec  *gob.Decoder
}

var _ Codec = (*GobCodec)(nil) // 检查GobCodec是否完全实现了接口 Codec

func NewGobCodec(conn io.ReadWriteCloser) Codec { // 这里是接口,竟然也可以返回地址
	buf := bufio.NewWriter(conn) // 返回的是指针
	return &GobCodec{
		conn: conn,
		buf:  buf,
		enc:  gob.NewEncoder(buf),
		dec:  gob.NewDecoder(conn),
	}
}

func (g *GobCodec) ReadHead(head *Header) error {
	return g.dec.Decode(head)
}

func (g *GobCodec) ReadBody(body interface{}) error {
	return g.dec.Decode(body)
}

func (g *GobCodec) Close() error {
	return g.conn.Close()
}

func (g *GobCodec) Write(head *Header, body interface{}) (err error) {
	/**
	// 自己的版本
	defer func() {
		if err := g.buf.Flush(); err != nil { // 这里如果flush失败,要关闭conn; 作者的意思是,这里要处理的是encode的失败
			conn.Close()
		}
	}()
	*/

	defer func() {
		_ = g.buf.Flush()
		if err != nil { // 如果此函数的处理中有发生错误, 这里直接关闭conn,避免在每次错误处理的时候都关闭,收敛错误处理代码
			g.conn.Close()
		}
	}()

	if err = g.enc.Encode(head); err != nil {
		log.Println("ReadBody: Encode Head Err: ", err.Error())
		return
	}
	// log.Printf("head send {%d} head=%v", head.Seq, head)

	if err = g.enc.Encode(body); err != nil {
		log.Println("ReadBody: Encode Body Err: ", err.Error())
		return
	}
	// log.Printf("body send {%d} body=%v", head.Seq, body)

	return
}
