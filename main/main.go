package main

import (
	"fmt"
	"geerpc"
	"log"
	"net"
	"sync"
	"time"
)

func startService(addr chan string) {
	//pick一个空闲的接口
	l, err := net.Listen("tcp", ":10010")
	if err != nil {
		log.Fatal("network error:", err)
	}
	log.Println("start rpc service on", l.Addr())
	addr <- l.Addr().String()
	geerpc.Accept(l)
	return
}

func main() {
	log.SetFlags(0) //0什么都没有
	addr := make(chan string)
	//启动服务，服务端无变化
	go startService(addr)
	//conn, _ := net.Dial("tcp", <-addr)
	client, _ := geerpc.Dial("tcp", <-addr)
	defer func() { _ = client.Close() }()

	time.Sleep(time.Second)

	////send Option，协商协议
	//_ = json.NewEncoder(conn).Encode(geerpc.DefaultOption)
	////定义编解码器
	//cc := codec.NewGobCodec(conn)
	var wg sync.WaitGroup
	//发送请求，接收响应
	for i := 0; i < 5; i++ {
		//h := &codec.Header{
		//	ServiceMethod: "User.Sum",
		//	Seq:           uint64(i),
		//}
		//_ = cc.Write(h, fmt.Sprintf("rpc req %d", h.Seq))
		//_ = cc.ReadHeader(h) //传cc指针，读到这个结构体中完善conn，buffer，dec，enc
		//var reply string
		//_ = cc.ReadBody(&reply) //读body内容到reply
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf("req go协程编号(0-4)%d", i)
			var reply string
			//调用封装Go的Call
			if err := client.Call("User.Sum", args, &reply); err != nil {
				log.Fatal("call request ", i, " User.Sum error:", err)
			}
			log.Println("reply : ", reply)
		}(i)

	}
	wg.Wait() //保证主线程在协程没有处理完的情况下不结束
}
