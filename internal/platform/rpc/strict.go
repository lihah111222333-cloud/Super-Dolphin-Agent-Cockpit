package rpc

import (
	"context"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

// StrictHandler 把强类型函数包装为只接受 object params 的严格 RPC handler。
// handler 签名非法属于注册期编程错误，直接 panic 暴露装配问题。
func StrictHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error)) handler.Func {
	info, err := handler.Check(fn)
	if err != nil {
		// archguard:ignore panic_count -- RPC handler 签名非法属于注册期编程错误，必须立即暴露。
		panic(err)
	}
	return InvalidParamsMapper()(info.AllowArray(false).SetStrict(true).Wrap())
}

// RawHandler 透传原始 jrpc2.Request，供需要自定义解码的少数方法使用。
func RawHandler(fn func(context.Context, *jrpc2.Request) (any, error)) handler.Func {
	return handler.Func(fn)
}
