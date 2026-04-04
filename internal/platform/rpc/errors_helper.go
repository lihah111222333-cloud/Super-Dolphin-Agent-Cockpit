package rpc

const CodeNotImplemented = -31006

func ErrNotImplemented(msg string) error {
	return rpcError(CodeNotImplemented, msg)
}
