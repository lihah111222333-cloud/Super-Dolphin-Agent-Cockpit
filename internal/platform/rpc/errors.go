package rpc

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

// RPC 错误码常量直接复用 contract 层定义，避免跨层 code 漂移。
const (
	CodeNotFound        = contract.CodeNotFound
	CodeInvalidState    = contract.CodeInvalidState
	CodeConflict        = contract.CodeConflict
	CodeCapabilityGate  = contract.CodeCapabilityGate
	CodeApprovalTimeout = contract.CodeApprovalTimeout
	CodeInvalidParams   = contract.CodeInvalidParams
	CodeMethodNotFound  = contract.CodeMethodNotFound
)

// ErrNotFound 构造资源不存在 RPC 错误。
func ErrNotFound(msg string) error { return rpcError(CodeNotFound, msg) }

// ErrInvalidState 构造状态不允许 RPC 错误。
func ErrInvalidState(msg string) error { return rpcError(CodeInvalidState, msg) }

// ErrConflict 构造冲突 RPC 错误。
func ErrConflict(msg string) error { return rpcError(CodeConflict, msg) }

// ErrCapabilityGate 构造能力门禁 RPC 错误。
func ErrCapabilityGate(msg string) error { return rpcError(CodeCapabilityGate, msg) }

// ErrApprovalTimeout 构造审批超时 RPC 错误。
func ErrApprovalTimeout(msg string) error { return rpcError(CodeApprovalTimeout, msg) }

// ErrInvalidParams 构造参数非法 RPC 错误。
func ErrInvalidParams(msg string) error { return rpcError(CodeInvalidParams, msg) }

// ErrMethodNotFound 构造方法不存在 RPC 错误。
func ErrMethodNotFound(msg string) error { return rpcError(CodeMethodNotFound, msg) }
