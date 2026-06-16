package rpc

import (
	"context"
	"fmt"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

// StrictHandler wraps a typed handler with object-only strict decoding.
// StrictHandler 处理strict处理器。
func StrictHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error)) handler.Func {
	info, err := handler.Check(fn)
	if err != nil {
		return handler.Func(func(context.Context, *jrpc2.Request) (any, error) {
			return nil, fmt.Errorf("invalid RPC handler signature: %w", err)
		})
	}
	return InvalidParamsMapper()(info.AllowArray(false).SetStrict(true).Wrap())
}

// RawHandler passes the raw request through unchanged.
// RawHandler 处理原始处理器。
func RawHandler(fn func(context.Context, *jrpc2.Request) (any, error)) handler.Func {
	return handler.Func(fn)
}
