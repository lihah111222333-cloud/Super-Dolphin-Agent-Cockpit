package rpcwire

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/recoverycustody"
)

// FailureCode 是可跨进程传播的关闭失败码集合。
type FailureCode string

const (
	FailureInvalidRequest       FailureCode = "invalid_request"
	FailureInvalidHandle        FailureCode = "invalid_handle"
	FailureOperationConflict    FailureCode = "operation_conflict"
	FailureReceiptRejected      FailureCode = "receipt_rejected"
	FailureResolutionConflict   FailureCode = "resolution_conflict"
	FailureOperationNotFound    FailureCode = "operation_not_found"
	FailureAuthorityUnavailable FailureCode = "authority_unavailable"
	FailureAuthorityReadOnly    FailureCode = "authority_read_only"
	FailureLeaseRejected        FailureCode = "lease_rejected"
	FailureCommitOutcomeUnknown FailureCode = "commit_outcome_unknown"
)

// Failure 只暴露稳定 code，禁止携带底层错误、路径、凭据或任意消息。
type Failure struct {
	Code FailureCode `json:"code"`
}

// Validate 验证 failure code 属于冻结的 v1 枚举。
func (f Failure) Validate() error {
	switch f.Code {
	case FailureInvalidRequest, FailureInvalidHandle, FailureOperationConflict, FailureReceiptRejected,
		FailureResolutionConflict, FailureOperationNotFound, FailureAuthorityUnavailable,
		FailureAuthorityReadOnly, FailureLeaseRejected, FailureCommitOutcomeUnknown:
		return nil
	default:
		return fmt.Errorf("%w: unsupported failure code", ErrInvalidWire)
	}
}

// FailureFromError 把已知领域错误映射为安全失败码，未知错误拒绝映射。
func FailureFromError(err error) (Failure, bool) {
	for _, mapping := range failureMappings() {
		if errors.Is(err, mapping.target) {
			return Failure{Code: mapping.code}, true
		}
	}
	return Failure{}, false
}

func failureMappings() []struct {
	target error
	code   FailureCode
} {
	return []struct {
		target error
		code   FailureCode
	}{
		{recoverycustody.ErrInvalidRequest, FailureInvalidRequest},
		{recoverycustody.ErrInvalidHandle, FailureInvalidHandle},
		{recoverycustody.ErrReceiptRejected, FailureReceiptRejected},
		{recoverycustody.ErrResolutionConflict, FailureResolutionConflict},
		{recoverycustody.ErrCustodyConflict, FailureOperationConflict},
		{recoverycustody.ErrOperationNotFound, FailureOperationNotFound},
		{recoverycustody.ErrCustodyUnavailable, FailureAuthorityUnavailable},
	}
}

// validateOutcomeFailureCombination 只允许可查询的安全失败携带 indeterminate 或已验证结构的终态。
func validateOutcomeFailureCombination(outcome *recoverycustody.Outcome, failure *Failure) error {
	if outcome == nil || failure == nil {
		return nil
	}
	switch failure.Code {
	case FailureAuthorityUnavailable:
		if outcome.Status == recoverycustody.StatusIndeterminate {
			return nil
		}
	case FailureReceiptRejected, FailureAuthorityReadOnly, FailureCommitOutcomeUnknown:
		if outcome.Status == recoverycustody.StatusIndeterminate ||
			outcome.Status == recoverycustody.StatusAvailable || outcome.Status == recoverycustody.StatusAborted {
			return nil
		}
	}
	return fmt.Errorf("%w: failure code cannot accompany this outcome", ErrInvalidWire)
}
