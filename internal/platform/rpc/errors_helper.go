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
func MapCapabilityError(err error) *jrpc2.Error {
	var capErr *contract.CapabilityError
	if errors.As(err, &capErr) {
		return jrpc2.Errorf(jrpc2.Code(CodeCapabilityGate), "%s", capErr.Error())
	}
	return nil
}
