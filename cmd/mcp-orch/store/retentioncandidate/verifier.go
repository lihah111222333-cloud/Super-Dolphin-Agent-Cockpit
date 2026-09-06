// Package retentioncandidate provides the local, candidate-only retention pin verifier.
package retentioncandidate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

const (
	// MaxPins bounds the number of retention facts inspected in one verification.
	MaxPins    int64 = 32
	queryLimit       = MaxPins + 1
)

var errInvalidCandidate = errors.New("retention pin candidate is invalid")

// Verifier checks only the transaction supplied by EventStore. It is a local
// candidate and is not wired into Fx or any public transport surface.
type Verifier struct{}

// New 创建无状态的留存候选验证器。
func New() *Verifier { return &Verifier{} }

// NewVerifier 为接缝调用方提供显式的留存候选验证器构造函数。
func NewVerifier() *Verifier { return New() }

// VerifyRetentionPinTx 在事务内校验候选生命周期，并阻止仍位于 dedup floor 的 tombstone 推进。
func (v *Verifier) VerifyRetentionPinTx(ctx context.Context, dbtx storedb.DBTX, req eventstore.TombstoneRequest) error {
	return v.verifySnapshot(ctx, dbtx, req)
}

// validateVerificationInputs 校验验证器、上下文、事务和 tombstone 请求的边界。
func validateVerificationInputs(v *Verifier, ctx context.Context, dbtx storedb.DBTX, req eventstore.TombstoneRequest) error {
	if v == nil {
		return fmt.Errorf("%w: verifier dependencies are missing", errInvalidCandidate)
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is required", errInvalidCandidate)
	}
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return fmt.Errorf("%w: transaction-bound DBTX: %v", errInvalidCandidate, err)
	}
	if err := req.Validate(); err != nil {
		return fmt.Errorf("%w: tombstone request: %v", errInvalidCandidate, err)
	}
	return nil
}

// loadCandidates 在给定事务中读取待检查的留存候选事实。
func loadCandidates(ctx context.Context, dbtx storedb.DBTX, req eventstore.TombstoneRequest) ([]storedb.ListOrchestrationRetentionPinCandidatesRow, error) {
	rows, err := storedb.New(dbtx).ListOrchestrationRetentionPinCandidates(ctx, storedb.ListOrchestrationRetentionPinCandidatesParams{
		SourceID: req.SourceID, SourceEpoch: req.SourceEpoch, Lane: string(req.Lane),
		ExpectedFloor: req.ExpectedFloor, Limit: queryLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: query retention pin candidates: %v", errInvalidCandidate, err)
	}
	return rows, nil
}

// validateCandidateCount 拒绝超过单次验证上限的候选结果，避免截断事实。
func validateCandidateCount(rows []storedb.ListOrchestrationRetentionPinCandidatesRow) error {
	if int64(len(rows)) > MaxPins {
		return fmt.Errorf("%w: candidate count exceeds bound %d", errInvalidCandidate, MaxPins)
	}
	return nil
}

// validateCandidate 校验候选身份、生命周期和规范化键，并判断是否钉住当前 floor。
func validateCandidate(row storedb.ListOrchestrationRetentionPinCandidatesRow, req eventstore.TombstoneRequest) (bool, error) {
	if err := validateCandidateIdentity(row, req); err != nil {
		return false, err
	}
	if err := validateCandidateLifecycle(row); err != nil {
		return false, err
	}
	if err := validateCandidateKey(row.PinKey); err != nil {
		return false, err
	}
	return row.AfterSourceSequence == req.ExpectedFloor && candidateNonterminal(row), nil
}

// validateCandidateIdentity 校验候选键、来源绑定、序列和 schema 版本。
func validateCandidateIdentity(row storedb.ListOrchestrationRetentionPinCandidatesRow, req eventstore.TombstoneRequest) error {
	if row.PinKey == "" {
		return fmt.Errorf("%w: candidate identity or sequence is invalid", errInvalidCandidate)
	}
	if row.SourceID != req.SourceID {
		return fmt.Errorf("%w: candidate identity or sequence is invalid", errInvalidCandidate)
	}
	if row.SourceEpoch != req.SourceEpoch {
		return fmt.Errorf("%w: candidate identity or sequence is invalid", errInvalidCandidate)
	}
	if row.Lane != string(req.Lane) {
		return fmt.Errorf("%w: candidate identity or sequence is invalid", errInvalidCandidate)
	}
	if row.AfterSourceSequence < 0 {
		return fmt.Errorf("%w: candidate identity or sequence is invalid", errInvalidCandidate)
	}
	if row.SchemaVersion != 1 {
		return fmt.Errorf("%w: candidate identity or sequence is invalid", errInvalidCandidate)
	}
	return nil
}

// validateCandidateLifecycle 校验 access wait 与 wait operation 的生命周期组合。
func validateCandidateLifecycle(row storedb.ListOrchestrationRetentionPinCandidatesRow) error {
	switch row.PinKind {
	case "access_wait":
		if row.OperationStatus != "" {
			return fmt.Errorf("%w: access wait %q has invalid lifecycle", errInvalidCandidate, row.PinKey)
		}
		if !accessWaitStatus(row.LifecycleStatus) {
			return fmt.Errorf("%w: access wait %q has invalid lifecycle", errInvalidCandidate, row.PinKey)
		}
	case "wait_operation":
		if !targetPhase(row.LifecycleStatus) {
			return fmt.Errorf("%w: wait operation target %q has invalid lifecycle", errInvalidCandidate, row.PinKey)
		}
		if !operationStatus(row.OperationStatus) {
			return fmt.Errorf("%w: wait operation target %q has invalid lifecycle", errInvalidCandidate, row.PinKey)
		}
		if nonterminalOperationCleared(row) {
			return fmt.Errorf("%w: nonterminal target %q has cleared operation", errInvalidCandidate, row.PinKey)
		}
	default:
		return fmt.Errorf("%w: unknown candidate kind %q", errInvalidCandidate, row.PinKind)
	}
	return nil
}

// nonterminalOperationCleared 拒绝非终态目标引用已清理的 operation。
func nonterminalOperationCleared(row storedb.ListOrchestrationRetentionPinCandidatesRow) bool {
	if !candidateNonterminal(row) {
		return false
	}
	return row.OperationStatus == "cleanup_cleared"
}

// validateCandidateKey 校验候选键保持规范化的无空白形式。
func validateCandidateKey(pinKey string) error {
	if strings.TrimSpace(pinKey) != pinKey {
		return fmt.Errorf("%w: candidate key is not canonical", errInvalidCandidate)
	}
	return nil
}

func accessWaitStatus(status string) bool {
	switch status {
	case "pending", "satisfied", "timed_out", "cancelled", "revoked", "expired":
		return true
	default:
		return false
	}
}

func targetPhase(phase string) bool {
	switch phase {
	case "planned", "grant_pending", "wait_pending", "waiting", "outcome_unknown", "cleanup_pending", "satisfied", "timed_out", "cancelled", "failed", "cleanup_cleared":
		return true
	default:
		return false
	}
}

func operationStatus(status string) bool {
	switch status {
	case "provisioning", "ready", "partial", "outcome_unknown", "cleanup_pending", "cleanup_cleared":
		return true
	default:
		return false
	}
}

func candidateNonterminal(row storedb.ListOrchestrationRetentionPinCandidatesRow) bool {
	if row.PinKind == "access_wait" {
		return row.LifecycleStatus == "pending"
	}
	switch row.LifecycleStatus {
	case "planned", "grant_pending", "wait_pending", "waiting", "outcome_unknown", "cleanup_pending":
		return true
	default:
		return false
	}
}

var _ eventstore.RetentionPinVerifier = (*Verifier)(nil)
