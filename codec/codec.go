package codec

/**
 *  定义头部，编解码（需要Header 结构体）
 */
import "io"

type Header struct {
	ServiceMethod string //服务名和方法名，通常与 Go 语言中的结构体和方法相映射
	Seq           uint64 //用于区分不同的请求序号，可以认为是一个64位的请求ID，区分不同请求
	Error         string //请求失败，错误信息
}

// Codec 接口：对消息体进行编解码的抽象
type Codec interface {
	io.Closer                   //关闭器
	ReadHeader(*Header) error   //出错返回error
	ReadBody(interface{}) error //interface{} 可传入任意信息结构，有点像Object
	Write(*Header, interface{}) error
}

// 定义一个匿名函数的类型
type NewCodecFunc func(conn io.ReadWriteCloser) Codec

type Type string

const (
	GobType  Type = "application/gob"
	JsonType Type = "application/json" //没实现
)

/**
 *  全局变量，根据Type找对消息体进行编解码的具体实现
 */
var NewCodecFuncMap map[Type]NewCodecFunc

func init() {
	//返回是构造函数而不是实例，像工厂模式（返回实例）但不是
	NewCodecFuncMap = make(map[Type]NewCodecFunc) //初始化全局变量，分配内存空间
	//CS可以通过Codec的Type得到构造函数，从而创建Codec实例
	NewCodecFuncMap[GobType] = NewGobCodec //一个包下，直接调用
}
