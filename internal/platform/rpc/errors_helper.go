package rpc

import "github.com/creachadair/jrpc2"

const CodeNotImplemented = -31006

func ErrNotImplemented(msg string) error {
	return jrpc2.Errorf(jrpc2.Code(CodeNotImplemented), "%s", msg)
}
