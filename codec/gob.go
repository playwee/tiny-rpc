package codec

import (
	"bufio"
	"encoding/gob"
	"io"
	"log"
)

/**
 * Codec接口的具体接口，需要对Codec抽象接口相关函数进行具体实现
 */
type GobCodec struct {
	conn io.ReadWriteCloser //包括了io.Closer
	//buf 是为了防止阻塞而创建的带缓冲的 Writer，一般这么做能提升性能
	buf *bufio.Writer
	//dec 和 enc 对应 gob 的 Decoder 和 Encoder
	dec *gob.Decoder
	enc *gob.Encoder
}

var _ Codec = (*GobCodec)(nil)

// conn 是由构建函数传入，通常是通过 TCP 或者 Unix 建立 socket 时得到的链接实例
func NewGobCodec(conn io.ReadWriteCloser) Codec {
	buf := bufio.NewWriter(conn) //buf 是为了防止阻塞而创建的带缓冲的 Writer
	return &GobCodec{
		conn: conn,
		buf:  buf,
		//dec 和 enc 对应 gob 的 Decoder 和 Encoder
		dec: gob.NewDecoder(conn), //根据请求连接信息解码创建解码器
		enc: gob.NewEncoder(buf),  //根据响应信息编码创建编码器
	}
}

/**
 * 抽象接口所有函数都得实现
 */
func (c *GobCodec) Close() error {
	return c.conn.Close()
}

func (c *GobCodec) ReadHeader(h *Header) error {
	return c.dec.Decode(h)
}
func (c *GobCodec) ReadBody(body interface{}) error {
	return c.dec.Decode(body)
}
func (c *GobCodec) Write(h *Header, body interface{}) (err error) {
	defer func() {
		_ = c.buf.Flush()
		//出现错误才关闭连接
		if err != nil {
			_ = c.Close()
		}
	}()
	if err := c.enc.Encode(h); err != nil {
		log.Println("rpc codec:gob error encoding header:", err) //编码错误
		return err
	}
	if err := c.enc.Encode(body); err != nil {
		log.Println("rpc codec:gob error encoding body:", err) //编码错误
		return err
	}
	return nil
}
