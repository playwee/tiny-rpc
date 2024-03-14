package geerpc

import (
	"encoding/json"
	"fmt"
	"geerpc/codec"
	"io"
	"log"
	"net"
	"reflect"
	"sync"
)

/**
 * 通信过程
 *
 * HTTP报文中需要根据请求中的Content-Type和Content-Length指定，从中知道用什么方式解码body信息
 * 为了提升性能，一般在报文的最开始会规划固定的字节，来协商相关的信息。
 * 比如，第1个字节用来表示序列化方式，第2个字节表示压缩方式
 * 第3-6字节表示 header 的长度，7-10 字节表示 body 的长度
 *
 * RPC目前需要知道消息的编解码方式，这部分放在Option承载
 */

const MagicNumber = 0x34252 //魔数标识rpc请求

type Option struct {
	MagicNumber int        //这个值标识为rpc请求
	CodecType   codec.Type //客户端会选择不同的Codec去编码body
}

/**
 * 默认Option对象
 */
var DefaultOption = &Option{
	MagicNumber: MagicNumber,
	CodecType:   codec.GobType,
}

/**
* 服务器实现
*
* 协议协商固定字节 JSON编码Option，header和body编码方式通过Option中的CodeType指定
* 服务端先JSON解码Option，通过Option的CodeType解码剩余的内容
* 一次连接，Header和Body可以有多个，报文可能：
  | Option | Header1 | Body1 | Header2 | Body2 | ...
*/

// 一个RPC服务器结构体
type Server struct {
}

// 创建RPC服务器
func NewServer() *Server {
	return &Server{}
}

// rpc包下的全局公共变量：默认服务器实例
var DefaultServer = NewServer()

/**
 * Accept功能：接受来自监听器的连接请求，并为这些新的连接处理相关的请求
 */
func (server *Server) Accept(listener net.Listener) {
	//死循环
	for {
		conn, err := listener.Accept() //等待下一个连接
		if err != nil {
			log.Println("rpc server:accept error:", err)
		}
		//与通信过程相关,conn连接是一个有可读可写可关闭的具体连接接口
		go server.ServeConn(conn) //TODO 具体连接
	}
}

func Accpet(listener net.Listener) {
	DefaultServer.Accept(listener) //调用连接
}

//若想启动服务，很简单，传入 listener 即可，listener通过net.Listen(协议，端口)，tcp 协议和 unix 协议都支持，然后传入Accept

/**
 * 处理（服务）连接
 *
 * 首先使用 json.NewDecoder 反序列化(常见json转结构体)得到 Option 实例，检查 MagicNumber 和 CodeType 的值是否正确
 * 然后根据 CodeType 得到对应的消息编解码器，接下来的处理交给 serverCodec
 */
func (server *Server) ServeConn(conn io.ReadWriteCloser) {
	defer func() { _ = conn.Close() }() //关闭连接

	var opt Option //Option 协议协商结构体

	//先使用 json.NewDecoder创建从连接读的解码器，，解码需要的参数（编码类型）到opt中
	if err := json.NewDecoder(conn).Decode(&opt); err != nil {
		log.Println("rpc server:options error:", err)
		return
	}
	//检查是否为rpc连接
	if opt.MagicNumber != MagicNumber {
		log.Printf("rpc server:invalid magic number %x", opt.MagicNumber)
		return
	}
	//得到一个对应的反序列化函数，看是否存在这个编解码器类型的接口，即codec的具体实现
	f := codec.NewCodecFuncMap[opt.CodecType]
	if f == nil {
		log.Printf("rpc server:invalid codec type %s", opt.CodecType)
		return
	}
	//对后续数据进行解码
	server.serveCodec(f(conn))
}

/**
 * rpc连接后的请求结构体
 */
type request struct {
	h            *codec.Header //请求头
	argv, replyv reflect.Value //请求的argvv 和 replyv
}

/**
 * 读取请求头,根据具体实现解码读请求头
 */
func (server *Server) readRequestHeader(cc codec.Codec) (*codec.Header, error) {
	var h codec.Header
	if err := cc.ReadHeader(&h); err != nil {
		if err != io.EOF && err != io.ErrUnexpectedEOF {
			log.Println("rpc server:read header error:", err)
		}
		//若是出现没有更多输入的错误，返回nil即可
		return nil, err
	}
	return &h, nil
}

/**
 * 读取请求 readRequest
 */
func (server *Server) readRequest(cc codec.Codec) (*request, error) {
	h, err := server.readRequestHeader(cc)
	if err != nil {
		return nil, err //读取头时候出现错误，均关闭连接
	}
	req := &request{h: h}
	//TODO 不知道请求argv，先认为是string
	req.argv = reflect.New(reflect.TypeOf(""))
	//.Interface() 以interface{}方式返回参数当前值
	if err = cc.ReadBody(req.argv.Interface()); err != nil {
		log.Println("rpc server: read argv err: ", err)
	}
	return req, nil //返回请求信息（头和参数体应答体）
}

/**
 * 回复请求 sendResponse
 */
func (server *Server) sendResponse(cc codec.Codec, h *codec.Header, body interface{}, sending *sync.Mutex) {
	sending.Lock()
	defer sending.Unlock()
	//写入，进行响应信息编码
	if err := cc.Write(h, body); err != nil {
		log.Println("rpc server: write response error: ", err)
	}
}

/**
 * 处理请求 handleRequest 协程并发执行请求（go）
 */
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	//TODO 应调用已注册的rpc方法去获得正确的replv
	// 先打印argv和发送hello message
	defer wg.Done() //自减1
	//Elem 返回接口包括或者指针指向的值
	log.Println(req.h, req.argv.Elem()) //打印header和请求参数
	req.replyv = reflect.ValueOf(fmt.Sprintf("rpc resp %d", req.h.Seq))
	//需要Interface()对reflect.Value进行转换
	server.sendResponse(cc, req.h, req.replyv.Interface(), sending)
	return
}

// 这是一个当错误发生后对响应参数的占位符，一个空结构体
var invalidRequest = struct{}{}

// Codec:编解码器
func (server *Server) serveCodec(cc codec.Codec) {
	//defer func(){
	//	_=cc.Close()
	//}()
	sending := new(sync.Mutex) //保证发送一个完整的响应
	wg := new(sync.WaitGroup)  //等待所有请求被处理

	/**
	 * 在一次连接中，允许接收多个请求，即多个 request header 和 request body，因此这里使用了 for 无限制地等待请求的到来，直到发生错误（例如连接被关闭，接收到的报文有问题等）
	 */
	for {
		req, err := server.readRequest(cc)
		if err != nil {
			if req == nil {
				break //该错误不可能恢复，所以关闭这个连接
			}
			//非请求体为空的错误，可以服务器处理
			req.h.Error = err.Error()
			//invalid空结构体
			server.sendResponse(cc, req.h, invalidRequest, sending)
			continue
		}
		//需要让handleRequest完全处理，内部加wg锁响应
		wg.Add(1)
		//得到请求信息后可以处理请求并返回
		go server.handleRequest(cc, req, sending, wg)
	}
	wg.Wait()
	_ = cc.Close()
}
