package main

import (
	"encoding/json"
	"fmt"
	"geerpc"
	"geerpc/codec"
	"log"
	"net"
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
	geerpc.Accpet(l)
	return
}

func main() {
	addr := make(chan string)
	//启动服务
	go startService(addr)

	conn, _ := net.Dial("tcp", <-addr)
	defer func() { _ = conn.Close() }()

	time.Sleep(time.Second)
	//send Option，协商协议
	_ = json.NewEncoder(conn).Encode(geerpc.DefaultOption)
	//定义编解码器
	cc := codec.NewGobCodec(conn)
	//发送请求，接收响应
	for i := 0; i < 5; i++ {
		h := &codec.Header{
			ServiceMethod: "User.Sum",
			Seq:           uint64(i),
		}
		_ = cc.Write(h, fmt.Sprintf("rpc req %d", h.Seq))
		_ = cc.ReadHeader(h) //传cc指针，读到这个结构体中完善conn，buffer，dec，enc
		var reply string
		_ = cc.ReadBody(&reply) //读body内容到reply
		log.Println("reply: ", reply)
	}
}
