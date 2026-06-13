package rpc

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Error code constants — delegate to contract.
const (
	CodeNotFound        = contract.CodeNotFound
	CodeInvalidState    = contract.CodeInvalidState
	CodeConflict        = contract.CodeConflict
	CodeCapabilityGate  = contract.CodeCapabilityGate
	CodeApprovalTimeout = contract.CodeApprovalTimeout
	CodeInvalidParams   = contract.CodeInvalidParams
	CodeMethodNotFound  = contract.CodeMethodNotFound
)

// RPC error constructors.
// ErrNotFound 处理errnotfound。
func ErrNotFound(msg string) error { return rpcError(CodeNotFound, msg) }

// ErrInvalidState 处理errinvalid状态。
func ErrInvalidState(msg string) error { return rpcError(CodeInvalidState, msg) }

// ErrConflict 处理errconflict。
func ErrConflict(msg string) error { return rpcError(CodeConflict, msg) }

// ErrCapabilityGate 处理errcapabilitygate。
func ErrCapabilityGate(msg string) error { return rpcError(CodeCapabilityGate, msg) }

// ErrApprovalTimeout 处理err审批超时。
func ErrApprovalTimeout(msg string) error { return rpcError(CodeApprovalTimeout, msg) }

// ErrInvalidParams 处理errinvalidparams。
func ErrInvalidParams(msg string) error { return rpcError(CodeInvalidParams, msg) }

// ErrMethodNotFound 处理errmethodnotfound。
func ErrMethodNotFound(msg string) error { return rpcError(CodeMethodNotFound, msg) }
