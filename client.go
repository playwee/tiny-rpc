package geerpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"geerpc/codec"
	"io"
	"log"
	"net"
	"sync"
)

/**
客户端
*/

/*
调用封装：代表一个活跃的RPC
*/
type Call struct {
	Seq           uint64
	ServiceMethod string      //如Service.<Method>
	Args          interface{} //请求参数
	Reply         interface{} //函数响应
	Error         error       // 错误处理设置
	Done          chan *Call  //完整被调用时Done,用于通知调用方
}

/*
异步调用，调用结束后，调用call.Done去通知调用方
*/
func (call *Call) done() {
	call.Done <- call //传入call本身
}

/*
核心部分：一个Client可以有多个调用，也可以同时被多个goroutine使用
*/
type Client struct {
	cc      codec.Codec      //编解码器
	opt     *Option          //协商协议
	sending sync.Mutex       //保护上下文，保证请求的有序发送（多个请求时），方式多个请求报文混淆
	header  codec.Header     //请求头，发送请求时才需要，每个客户端只需要一个，可以复用
	mu      sync.Mutex       //保护上下文
	seq     uint64           //请求编号
	pending map[uint64]*Call //存储未进行调用的Call，键是编号，值是 Call 实例
	//任意一个为true，标识客户端不可用
	closing  bool //主动关闭，调用Close方法
	shutdown bool //有错误发生
}

/*
*
全局变量,检查接口是否实现
*/
var _ io.Closer = (*Client)(nil)

var ErrShutdown = errors.New("connection is shut down")

/*
*
Close接口的具体实现，用户主动调用Close函数
*/
func (client *Client) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing {
		return ErrShutdown
	}
	//主动调用的修改
	client.closing = true
	return client.cc.Close() //调用编解码器的Close，一般就是连接关闭
}

/*
检验客户端是否工作
*/
func (client *Client) IsAvailable() bool {
	client.mu.Lock()
	defer client.mu.Unlock()
	return !client.shutdown && !client.closing
}

/*
调用注册,设置根据机器设置seq到Call结构,将参数call添到client.pending,并同时更新seq作为下一个新请求的编号
*/
func (client *Client) registerCall(call *Call) (uint64, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closing || client.shutdown {
		return 0, ErrShutdown
	}
	//rpc调用
	call.Seq = client.seq
	client.pending[call.Seq] = call //添加至调用map
	client.seq++                    //下一个使用
	return call.Seq, nil
}

/*
移除调用从pending移除对应的call并返回
*/
func (client *Client) removeCall(seq uint64) *Call {
	client.mu.Lock()
	defer client.mu.Unlock()
	//根据seq移除调用
	call := client.pending[seq] //当没有要处理的Call请求
	delete(client.pending, seq) //将map中key为seq从pending删除
	return call                 //返回对应调用call
}

/*
*
CS发生错误时调用，shutdown改成true，并将错误信息通知所有pending状态的call
保证数据传输时CS都正常
*/
func (client *Client) terminateCalls(err error) {
	//触发即认为后面的rpc请求都不处理，以错误进行处理
	client.sending.Lock()
	defer client.sending.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	client.shutdown = true
	//遍历pending，一个map，不要索引
	for _, call := range client.pending {
		call.Error = err //若为空，则设置为nil
		call.done()      //通知，结束client
	}
}

/*
客户端，接收响应，发送请求最重要
接收的响应：
1.call不存在，请求没有完整发送，或者其他原因被取消，但是服务端仍处理
2.call存在，服务端处理出错，h.Error不为空
3.call存在，服务端处理正常，需要从body读Reply值
*/
func (client *Client) receive() {
	var err error
	for err == nil {
		var h codec.Header //局部变量
		//前面检验完Codec，读请求头，出错就break
		if err = client.cc.ReadHeader(&h); err != nil {
			break
		}
		//正在处理这个Call调用，需要先从将执行的Call map中移除
		call := client.removeCall(h.Seq)
		switch {
		case call == nil:
			//常用于Write函数部分错误，call已经被移除
			//不存在map中
			err = client.cc.ReadBody(nil) //call为空说明，没有待rpc调用的请求
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = client.cc.ReadBody(nil)
			call.done() //用于调用下一个Call
		default:
			//读响应体，放在调用call的Reply结构
			err = client.cc.ReadBody(call.Reply)
			//解码读请求体出错
			if err != nil {
				call.Error = errors.New("reading body " + err.Error())
			}
			call.done()
		}
	}
	log.Println("me!")
	//服务端或客户端错误发生了，被动关闭RPC相关调用
	client.terminateCalls(err)
}

/*
新建客户端，前面Dial检验了Option，地址，然后通过Option找编解码器，如果合适，就进行编码opt
*/
func NewClient(conn net.Conn, opt *Option) (*Client, error) {
	f := codec.NewCodecFuncMap[opt.CodecType] //协商协议找对应编解码器的具体实现
	//不存在对应编解码器
	if f == nil {
		err := fmt.Errorf("invalid codec type %s", opt.CodecType)
		log.Println("rpc client:codec error: ", err)
		return nil, err
	}
	//发送options
	if err := json.NewEncoder(conn).Encode(opt); err != nil {
		log.Println("rpc client:options error: ", err)
		_ = conn.Close()
		return nil, err
	}
	//f为需要的编解码器构造函数
	return newClientCodec(f(conn), opt), nil
}

func newClientCodec(cc codec.Codec, opt *Option) *Client {
	client := &Client{
		seq:     1, //从1开始，0意味着invalid call
		cc:      cc,
		opt:     opt,
		pending: make(map[uint64]*Call),
	}
	go client.receive() //协程调用接收响应
	return client
}

// 解析Options，通过...*Option 实现可选参数,其实就是[]*Option,变成slice
func parseOptions(opts ...*Option) (*Option, error) {
	//opts is nil 或者让nil作为参数,opt一般是第一个信息
	if len(opts) == 0 || opts[0] == nil {
		return DefaultOption, nil
	}
	if len(opts) != 1 {
		return nil, errors.New("number of options is more than 1")
	}
	opt := opts[0]                              //第一个值
	opt.MagicNumber = DefaultOption.MagicNumber //是否是RPC调用
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType //若类型为空则以默认调用
	}
	return opt, nil
}

/*
用户传入服务端地址，创建Client实例，简化调用，创建完整的连接，调用接收响应
*/
func Dial(network, address string, opts ...*Option) (client *Client, err error) {
	opt, err := parseOptions(opts...)
	if err != nil {
		return nil, err //opt错误
	}
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err //来凝结错误
	}
	//出现错误，关闭连接
	defer func() {
		if err != nil {
			_ = conn.Close() //服务器不存在，当然断开连接
		}
	}()
	return NewClient(conn, opt)
}

/*
*
发送请求
*/
func (client *Client) send(call *Call) {
	//发送完整的请求，需要利用到互斥锁
	client.sending.Lock()
	defer client.sending.Unlock()

	//注册call
	seq, err := client.registerCall(call) //call放在map，且通过函数获得seq
	if err != nil {
		call.Error = err
		call.done() //能到这
		return
	}

	/**
	当前的服务器相关信息设置，当前处理rpc调用编号seq，发给发header.Service
	*/
	client.header.ServiceMethod = call.ServiceMethod
	client.header.Seq = seq
	client.header.Error = "" //默认错误为空字符串

	/**
	编码和发送请求
	*/
	if err := client.cc.Write(&client.header, call.Args); err != nil {
		call := client.removeCall(seq) //没有错误，不调用
		//call可能是nil，如果发生写错误
		//客户端还是需要接收响应并处理
		if call != nil {
			call.Error = err
			call.done() //通知调用方
		}
	}
}

/*
Go和Call是暴露给user的两个RPC服务调用接口，Go异步接口，返回call实例
*/

func (client *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	//异步rpc调用函数，它返回调用Call指针，代表它的invocation调用
	//异步接口
	if done == nil {
		//初始化，并发为10
		done = make(chan *Call, 10)
	} else if cap(done) == 0 {
		//done通道无缓存
		log.Panic("rpc client:done channel is unbuffered")
	}

	call := &Call{
		ServiceMethod: serviceMethod,
		Args:          args,
		Reply:         reply,
		Done:          done,
	}
	//根据call去send
	client.send(call)
	return call
}

func (client *Client) Call(serviceMethod string, args, reply interface{}) error {
	//调用有名函数，等到他完成，并返回它的错误状态，是对Go的封装，阻塞call.Done，等待响应返回，一个同步接口
	//call := <-client.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done //先处理内部再处理外部
	call := <-client.Go(serviceMethod, args, reply, nil).Done
	return call.Error
}
