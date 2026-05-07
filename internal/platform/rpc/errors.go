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
func ErrNotFound(msg string) error        { return rpcError(CodeNotFound, msg) }
func ErrInvalidState(msg string) error    { return rpcError(CodeInvalidState, msg) }
func ErrConflict(msg string) error        { return rpcError(CodeConflict, msg) }
func ErrCapabilityGate(msg string) error  { return rpcError(CodeCapabilityGate, msg) }
func ErrApprovalTimeout(msg string) error { return rpcError(CodeApprovalTimeout, msg) }
func ErrInvalidParams(msg string) error   { return rpcError(CodeInvalidParams, msg) }
func ErrMethodNotFound(msg string) error  { return rpcError(CodeMethodNotFound, msg) }
