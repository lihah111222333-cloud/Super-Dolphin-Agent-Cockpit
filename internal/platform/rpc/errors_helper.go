package rpc

import (
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/creachadair/jrpc2"
)

const CodeNotImplemented = contract.CodeNotImplemented

// ErrNotImplemented 构造未实现 RPC 错误。
func ErrNotImplemented(msg string) error {
	return rpcError(CodeNotImplemented, msg)
}

// MapCapabilityError 将 contract.CapabilityError 映射为标准能力门禁 RPC 错误。
// err 不包含 CapabilityError 时返回 nil，调用方可把它作为错误过滤器使用：
//
//	if rpcErr := rpc.MapCapabilityError(err); rpcErr != nil {
//	    return rpcErr
//	}
//
// RPC 错误消息使用完整 err.Error()，保留外层包装提供的用户友好说明。
func MapCapabilityError(err error) *jrpc2.Error {
	var capErr *contract.CapabilityError
	if errors.As(err, &capErr) {
		return jrpc2.Errorf(jrpc2.Code(CodeCapabilityGate), "%s", err.Error())
	}
	return nil
}

// MapInvalidParamsError 将 jrpc2.InvalidParams 映射为平台统一参数错误码。
// err 不包含 InvalidParams 时返回 nil，便于中间件继续透传原错误。
func MapInvalidParamsError(err error) *jrpc2.Error {
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == jrpc2.InvalidParams {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "%s", rpcErr.Message)
	}
	return nil
}
