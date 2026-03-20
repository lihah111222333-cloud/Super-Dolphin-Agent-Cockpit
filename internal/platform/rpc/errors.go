package rpc

import "github.com/creachadair/jrpc2"

const (
	CodeNotFound        = -31001
	CodeInvalidState    = -31002
	CodeConflict        = -31003
	CodeCapabilityGate  = -31004
	CodeApprovalTimeout = -31005
)

func ErrNotFound(msg string) error {
	return jrpc2.Errorf(jrpc2.Code(CodeNotFound), "%s", msg)
}

func ErrInvalidState(msg string) error {
	return jrpc2.Errorf(jrpc2.Code(CodeInvalidState), "%s", msg)
}

func ErrConflict(msg string) error {
	return jrpc2.Errorf(jrpc2.Code(CodeConflict), "%s", msg)
}

func ErrCapabilityGate(msg string) error {
	return jrpc2.Errorf(jrpc2.Code(CodeCapabilityGate), "%s", msg)
}

func ErrApprovalTimeout(msg string) error {
	return jrpc2.Errorf(jrpc2.Code(CodeApprovalTimeout), "%s", msg)
}
