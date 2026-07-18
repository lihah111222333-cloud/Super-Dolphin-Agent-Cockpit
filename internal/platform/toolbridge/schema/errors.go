package schema

import (
	"errors"
	"fmt"
)

// InitializationFailureClass 控制 lazy schema 初始化失败是否可缓存。
type InitializationFailureClass string

const (
	InitializationFailureStable    InitializationFailureClass = "stable"
	InitializationFailureTransient InitializationFailureClass = "transient"
)

type initializationFailure struct {
	class InitializationFailureClass
	cause error
}

// Error 返回被分类初始化错误的原始文本。
func (e *initializationFailure) Error() string {
	if e == nil || e.cause == nil {
		return ""
	}
	return e.cause.Error()
}

// Unwrap 保留被分类初始化错误的根因链。
func (e *initializationFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// StableInitializationError 将确定性初始化失败标记为可缓存。
func StableInitializationError(err error) error {
	return classifyInitializationError(err, InitializationFailureStable)
}

// TransientInitializationError 将系统或资源失败标记为可重试。
func TransientInitializationError(err error) error {
	return classifyInitializationError(err, InitializationFailureTransient)
}

func classifyInitializationError(err error, class InitializationFailureClass) error {
	if err == nil {
		return nil
	}
	return &initializationFailure{class: class, cause: err}
}

// InitializationFailureClassOf 返回错误链中最近的初始化分类。
func InitializationFailureClassOf(err error) (InitializationFailureClass, bool) {
	var failure *initializationFailure
	if !errors.As(err, &failure) {
		return "", false
	}
	return failure.class, true
}

// Code 是 schema execution boundary 的稳定诊断码。
type Code string

const (
	CodeInvalidEnvelope      Code = "MCP_SCHEMA_INVALID_ENVELOPE"
	CodeInputTooLarge        Code = "MCP_SCHEMA_INPUT_TOO_LARGE"
	CodeOutputTooLarge       Code = "MCP_SCHEMA_OUTPUT_TOO_LARGE"
	CodeBudgetExceeded       Code = "MCP_SCHEMA_BUDGET_EXCEEDED"
	CodeExternalRefForbidden Code = "MCP_SCHEMA_EXTERNAL_REF_FORBIDDEN"
	CodeDraftUnsupported     Code = "MCP_SCHEMA_DRAFT_UNSUPPORTED"
	CodeRootNotObject        Code = "MCP_SCHEMA_ROOT_NOT_OBJECT"
	CodeCompileFailed        Code = "MCP_SCHEMA_COMPILE_FAILED"
	CodeArgumentInvalid      Code = "MCP_SCHEMA_ARGUMENT_INVALID"
	CodeCapacityExhausted    Code = "MCP_SCHEMA_CAPACITY_EXHAUSTED"
	CodeProcessStartFailed   Code = "MCP_SCHEMA_PROCESS_START_FAILED"
	CodeTimeout              Code = "MCP_SCHEMA_TIMEOUT"
	CodeCancelled            Code = "MCP_SCHEMA_CANCELLED"
	CodeProcessExited        Code = "MCP_SCHEMA_PROCESS_EXITED"
	CodeProtocolViolation    Code = "MCP_SCHEMA_PROTOCOL_VIOLATION"
	CodeGenerationStale      Code = "MCP_SCHEMA_GENERATION_STALE"
	CodeDigestMismatch       Code = "MCP_SCHEMA_DIGEST_MISMATCH"
	CodeReapFailed           Code = "MCP_SCHEMA_REAP_FAILED"
)

// Diagnostic 保留稳定 code、受限 message 和内部根因。
type Diagnostic struct {
	Code    Code
	Message string
	cause   error
}

// Error 返回稳定诊断码和有界诊断消息。
func (e *Diagnostic) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap 暴露仅供内部错误链检查的根因。
func (e *Diagnostic) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newDiagnostic(code Code, message string, cause error) error {
	return &Diagnostic{Code: code, Message: message, cause: cause}
}

// ErrorCode 从错误链提取稳定诊断码。
func ErrorCode(err error) Code {
	var diagnostic *Diagnostic
	if errors.As(err, &diagnostic) {
		return diagnostic.Code
	}
	return ""
}

// errorTreeContainsCode 递归检查单链包装与 errors.Join 多分支中的目标诊断码。
func errorTreeContainsCode(err error, code Code) bool {
	if err == nil {
		return false
	}
	if diagnostic, ok := err.(*Diagnostic); ok && diagnostic != nil && diagnostic.Code == code {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if errorTreeContainsCode(child, code) {
				return true
			}
		}
		return false
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return errorTreeContainsCode(wrapped.Unwrap(), code)
	}
	return false
}

func isKnownCode(code Code) bool {
	switch code {
	case CodeInvalidEnvelope, CodeInputTooLarge, CodeOutputTooLarge, CodeBudgetExceeded,
		CodeExternalRefForbidden, CodeDraftUnsupported, CodeRootNotObject, CodeCompileFailed,
		CodeArgumentInvalid, CodeCapacityExhausted, CodeProcessStartFailed, CodeTimeout,
		CodeCancelled, CodeProcessExited, CodeProtocolViolation, CodeGenerationStale,
		CodeDigestMismatch, CodeReapFailed:
		return true
	default:
		return false
	}
}
