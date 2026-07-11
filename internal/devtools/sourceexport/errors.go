package sourceexport

import "fmt"

// Code 是开源导出失败的稳定机器错误码。
type Code string

const (
	CodePolicyInvalid         Code = "POLICY_INVALID"
	CodeUnclassifiedPath      Code = "UNCLASSIFIED_PATH"
	CodeForbiddenPath         Code = "FORBIDDEN_PATH"
	CodeForbiddenIdentity     Code = "FORBIDDEN_IDENTITY"
	CodeLicenseMismatch       Code = "LICENSE_MISMATCH"
	CodeModulePathMismatch    Code = "MODULE_PATH_MISMATCH"
	CodeSymlinkRejected       Code = "SYMLINK_REJECTED"
	CodeCaseCollision         Code = "CASE_COLLISION"
	CodeOutputNotEmpty        Code = "OUTPUT_NOT_EMPTY"
	CodeSourceDirty           Code = "SOURCE_DIRTY"
	CodeSecretScanFailed      Code = "SECRET_SCAN_FAILED"
	CodeExportReceiptMismatch Code = "EXPORT_RECEIPT_MISMATCH"
)

// Error 为开源导出错误附加稳定错误码和可选路径。
type Error struct {
	Code Code
	Path string
	Err  error
}

// Error 返回适合 CLI 输出的稳定错误文本。
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Path != "" {
		return fmt.Sprintf("%s %s: %v", e.Code, e.Path, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Err)
}

// Unwrap 返回底层错误，供 errors.Is 和 errors.As 使用。
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
