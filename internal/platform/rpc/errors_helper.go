package rpc

import (
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/creachadair/jrpc2"
)

const CodeNotImplemented = -31006

func ErrNotImplemented(msg string) error {
	return rpcError(CodeNotImplemented, msg)
}

// MapCapabilityError maps a contract.CapabilityError to the standard RPC gate
// code (-31004). Returns nil if err does not contain a CapabilityError,
// allowing callers to use it as a filter:
//
//	if rpcErr := rpc.MapCapabilityError(err); rpcErr != nil {
//	    return rpcErr
//	}
//
// The RPC error message uses err.Error() (not the unwrapped CapabilityError)
// to preserve user-friendly messages from wrapper types.
func MapCapabilityError(err error) *jrpc2.Error {
	var capErr *contract.CapabilityError
	if errors.As(err, &capErr) {
		return jrpc2.Errorf(jrpc2.Code(CodeCapabilityGate), "%s", err.Error())
	}
	return nil
}

// MapInvalidParamsError maps a jrpc2.InvalidParams error to the standard RPC gate
// code CodeInvalidParams. Returns nil if err does not contain a jrpc2.InvalidParams error.
func MapInvalidParamsError(err error) *jrpc2.Error {
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == jrpc2.InvalidParams {
		return jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "%s", rpcErr.Message)
	}
	return nil
}
