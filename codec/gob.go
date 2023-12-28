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

// 定义gob编码的构造函数
func NewGobCodec(conn io.ReadWriteCloser) Codec { // 这里是接口,竟然也可以返回地址 //?谁tm跟你说这是接口,看清楚了,这是个函数
	// 创建编码缓冲区, 临时存储数据, 避免每次写数据都走系统调用
	/*
		带缓冲区编码的好处:
			1、减少系统调用
			2、提高数据传输效率: 暂存起来, 一次性读写
			3、平衡数据生产和消费速度
			4、提供临时存储空间, 方便操作数据
	*/
	buf := bufio.NewWriter(conn) // 返回的是指针
	return &GobCodec{
		conn: conn,                 // socket连接
		buf:  buf,                  // 编解码用到的缓冲区
		enc:  gob.NewEncoder(buf),  // 编码器, 使用自己定义的缓冲区
		dec:  gob.NewDecoder(conn), // 解码器, 数据来源是socket等网络连接
	}
}

// 读取head大小的数据,并写入给定的地址
func (c *GobCodec) ReadHeader(head *Header) error {
	return c.dec.Decode(head)
}

func (c *GobCodec) ReadBody(body interface{}) error {
	return c.dec.Decode(body)
}

// 编码器写数据, 相当于要直接写到socket
func (c *GobCodec) Write(head *Header, body interface{}) (err error) {
	/**
	// 自己的版本
	defer func() {
		if err := c.buf.Flush(); err != nil { // 这里如果flush失败,要关闭conn; 作者的意思是,这里要处理的是encode的失败
			conn.Close()
		}
	}()
	*/

	defer func() {
		_ = c.buf.Flush()
		if err != nil { // 如果此函数的处理中有发生错误, 这里直接关闭conn,避免在每次错误处理的时候都关闭,收敛错误处理代码
			log.Println("gob Write buf.Flush err", err)
			c.Close()
		}
	}()

	if err = c.enc.Encode(head); err != nil {
		log.Println("rpc codec: gob error encoding header:", err)
		return err
	}
	// log.Printf("head send {%d} head=%v", head.Seq, head)

	if err = c.enc.Encode(body); err != nil {
		log.Println("rpc codec: gob error encoding body:", err)
		return err
	}
	// log.Printf("body send {%d} body=%v", head.Seq, body)

	return nil
}

// 关闭网络连接
func (c *GobCodec) Close() error {
	return c.conn.Close()
}
