package rpc

const (
	CodeNotFound        = -31001
	CodeInvalidState    = -31002
	CodeConflict        = -31003
	CodeCapabilityGate  = -31004
	CodeApprovalTimeout = -31005
)

func ErrNotFound(msg string) error {
	return rpcError(CodeNotFound, msg)
}

func ErrInvalidState(msg string) error {
	return rpcError(CodeInvalidState, msg)
}

func ErrConflict(msg string) error {
	return rpcError(CodeConflict, msg)
}

func ErrCapabilityGate(msg string) error {
	return rpcError(CodeCapabilityGate, msg)
}

func ErrApprovalTimeout(msg string) error {
	return rpcError(CodeApprovalTimeout, msg)
}
