// Package managedlaunch 冻结 managed-runtime bootstrap 的 transport-neutral 合同。
//
// 本包只描述 receipt owner 的声明形状，不实现进程启动、provider 调用、
// active-current binding 写入或 receipt 验真。在真实 managed-runtime process receipt
// verifier/helper 完成并经独立验证前，禁止把 Backend 装配到 production wiring。
package managedlaunch

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

const (
	SchemaVersion int64 = 1

	StatusPrepared       = "prepared"
	StatusCommitted      = "committed"
	StatusAborted        = "aborted"
	StatusOutcomeUnknown = "outcome_unknown"
)

var (
	ErrInvalidOperationKey = errors.New("managed launch operation key is invalid")
	ErrInvalidBootstrap    = errors.New("managed launch bootstrap request is invalid")
	ErrInvalidResult       = errors.New("managed launch bootstrap result is invalid")
	ErrInvalidCommit       = errors.New("managed launch commit request is invalid")
	ErrInvalidAbort        = errors.New("managed launch abort request is invalid")
	ErrInvalidOutcome      = errors.New("managed launch outcome is invalid")
	ErrOperationConflict   = errors.New("managed launch operation conflicts with the original operation")
	ErrBackendUnavailable  = errors.New("managed launch backend is unavailable")
)

// OperationKey 是 bootstrap、commit、abort 和 query 共用的唯一幂等键。
// 相同键必须回放同一已存 outcome，键相同但任一摘要不同必须冲突。
type OperationKey struct {
	SchemaVersion          int64  `json:"schema_version"`
	CommandOperationID     string `json:"command_operation_id"`
	CommandOperationDigest string `json:"command_operation_digest"`
	AttemptOperationID     string `json:"attempt_operation_id"`
	AttemptOperationDigest string `json:"attempt_operation_digest"`
	IdempotencyKeyDigest   string `json:"idempotency_key_digest"`
	BootstrapRequestDigest string `json:"bootstrap_request_digest"`
}

// BootstrapRequest 绑定 canonical command/attempt、身份、资源、fence 与凭据 lease 摘要。
// CredentialLeaseID 只是 lease 标识，本合同禁止携带 token 或 credential bytes。
type BootstrapRequest struct {
	SchemaVersion          int64  `json:"schema_version"`
	CommandOperationID     string `json:"command_operation_id"`
	CommandOperationDigest string `json:"command_operation_digest"`
	AttemptOperationID     string `json:"attempt_operation_id"`
	AttemptOperationDigest string `json:"attempt_operation_digest"`
	AgentID                string `json:"agent_id"`
	AgentDigest            string `json:"agent_digest"`
	ProviderID             string `json:"provider_id"`
	ProviderDigest         string `json:"provider_digest"`
	ModelDigest            string `json:"model_digest"`
	CWDDigest              string `json:"cwd_digest"`
	ReservationID          string `json:"reservation_id"`
	ReservationGeneration  int64  `json:"reservation_generation"`
	SourceFenceRef         string `json:"source_fence_ref"`
	SourceFenceDigest      string `json:"source_fence_digest"`
	TargetFenceRef         string `json:"target_fence_ref"`
	TargetFenceDigest      string `json:"target_fence_digest"`
	CredentialLeaseID      string `json:"credential_lease_id"`
	CredentialLeaseDigest  string `json:"credential_lease_digest"`
	IdempotencyKeyDigest   string `json:"idempotency_key_digest"`
	BootstrapRequestDigest string `json:"bootstrap_request_digest"`
}

// BootstrapResult 只表示 prepared；其 receipt ref/digest 仅是 owner 声明，不是验真结论。
// 本包只验证形状与摘要自一致，不验证进程存活、owner 权限或 external epoch。
type BootstrapResult struct {
	SchemaVersion              int64        `json:"schema_version"`
	Operation                  OperationKey `json:"operation"`
	Status                     string       `json:"status"`
	OpaqueHandle               string       `json:"opaque_handle"`
	ProviderThreadIdentity     string       `json:"provider_thread_identity"`
	ProviderSessionIdentity    string       `json:"provider_session_identity"`
	RuntimeEpoch               string       `json:"runtime_epoch"`
	ExternalEpoch              string       `json:"external_epoch"`
	ProcessGeneration          int64        `json:"process_generation"`
	ActiveProcessReceiptRef    string       `json:"active_process_receipt_ref"`
	ActiveProcessReceiptDigest string       `json:"active_process_receipt_digest"`
	RuntimeOwnerID             string       `json:"runtime_owner_id"`
	RuntimeOwnerGeneration     int64        `json:"runtime_owner_generation"`
	ExecutableDigest           string       `json:"executable_digest"`
	ArgvDigest                 string       `json:"argv_digest"`
	EnvDigest                  string       `json:"env_digest"`
	CWDDigest                  string       `json:"cwd_digest"`
	ResultDigest               string       `json:"result_digest"`
}

// CommitRequest 只允许消费原 prepared result，并绑定原 prepared request、active-current CAS 与已激活凭据摘要。
type CommitRequest struct {
	SchemaVersion                     int64        `json:"schema_version"`
	Operation                         OperationKey `json:"operation"`
	PreparedRequestDigest             string       `json:"prepared_request_digest"`
	PreparedResultDigest              string       `json:"prepared_result_digest"`
	ActiveCurrentBindingDigest        string       `json:"active_current_binding_digest"`
	ActiveCurrentBindingVersion       int64        `json:"active_current_binding_version"`
	ActiveCurrentBindingIncarnationID string       `json:"active_current_binding_incarnation_id"`
	ActivatedCredentialDigest         string       `json:"activated_credential_digest"`
	CommitOperationDigest             string       `json:"commit_operation_digest"`
}

// AbortRequest 只绑定原 prepared request/result 与 cleanup operation/evidence，不接受本地超时推测。
type AbortRequest struct {
	SchemaVersion          int64        `json:"schema_version"`
	Operation              OperationKey `json:"operation"`
	PreparedRequestDigest  string       `json:"prepared_request_digest"`
	PreparedResultDigest   string       `json:"prepared_result_digest"`
	CleanupOperationID     string       `json:"cleanup_operation_id"`
	CleanupOperationDigest string       `json:"cleanup_operation_digest"`
	CleanupEvidenceRef     string       `json:"cleanup_evidence_ref"`
	CleanupEvidenceDigest  string       `json:"cleanup_evidence_digest"`
	AbortOperationDigest   string       `json:"abort_operation_digest"`
}

// CommitResult 是 query 可返回的 committed 分支，所有字段都来自已验证的 CommitRequest。
type CommitResult struct {
	SchemaVersion                     int64        `json:"schema_version"`
	Operation                         OperationKey `json:"operation"`
	PreparedRequestDigest             string       `json:"prepared_request_digest"`
	PreparedResultDigest              string       `json:"prepared_result_digest"`
	ActiveCurrentBindingDigest        string       `json:"active_current_binding_digest"`
	ActiveCurrentBindingVersion       int64        `json:"active_current_binding_version"`
	ActiveCurrentBindingIncarnationID string       `json:"active_current_binding_incarnation_id"`
	ActivatedCredentialDigest         string       `json:"activated_credential_digest"`
	CommitOperationDigest             string       `json:"commit_operation_digest"`
}

// AbortResult 是 query 可返回的 aborted 分支，它不将 evidence ref 提升为验真。
type AbortResult struct {
	SchemaVersion          int64        `json:"schema_version"`
	Operation              OperationKey `json:"operation"`
	PreparedRequestDigest  string       `json:"prepared_request_digest"`
	PreparedResultDigest   string       `json:"prepared_result_digest"`
	CleanupOperationID     string       `json:"cleanup_operation_id"`
	CleanupOperationDigest string       `json:"cleanup_operation_digest"`
	CleanupEvidenceRef     string       `json:"cleanup_evidence_ref"`
	CleanupEvidenceDigest  string       `json:"cleanup_evidence_digest"`
	AbortOperationDigest   string       `json:"abort_operation_digest"`
}

// OutcomeUnknownResult 只能由 QueryOperation 返回，表示 backend 不能裁决原 operation outcome。
type OutcomeUnknownResult struct {
	SchemaVersion  int64        `json:"schema_version"`
	Operation      OperationKey `json:"operation"`
	ReasonCode     string       `json:"reason_code"`
	EvidenceRef    string       `json:"evidence_ref"`
	EvidenceDigest string       `json:"evidence_digest"`
}

// KnownOutcome 是所有 mutation 方法的返回类型；类型层面刻意不包含 outcome_unknown。
type KnownOutcome struct {
	SchemaVersion int64            `json:"schema_version"`
	Operation     OperationKey     `json:"operation"`
	Status        string           `json:"status"`
	Prepared      *BootstrapResult `json:"prepared,omitempty"`
	Committed     *CommitResult    `json:"committed,omitempty"`
	Aborted       *AbortResult     `json:"aborted,omitempty"`
}

// QueryOutcome 是查询结果；status 与四个 presence 分支必须严格 one-hot。
type QueryOutcome struct {
	SchemaVersion  int64                 `json:"schema_version"`
	Operation      OperationKey          `json:"operation"`
	Status         string                `json:"status"`
	Prepared       *BootstrapResult      `json:"prepared,omitempty"`
	Committed      *CommitResult         `json:"committed,omitempty"`
	Aborted        *AbortResult          `json:"aborted,omitempty"`
	OutcomeUnknown *OutcomeUnknownResult `json:"outcome_unknown,omitempty"`
}

// Backend 是未实现的 managed-runtime receipt owner seam。
//
// 四个方法必须共用 OperationKey；同键重试只能回放已存结果，同键异摘要必须返回
// ErrOperationConflict。Bootstrap/CommitBootstrap/AbortBootstrap 不得返回 outcome_unknown；
// 只有 QueryOperation 可以返回该分支。本接口存在不表示已具备 production 实现。
type Backend interface {
	Bootstrap(context.Context, BootstrapRequest) (KnownOutcome, error)
	CommitBootstrap(context.Context, CommitRequest) (KnownOutcome, error)
	AbortBootstrap(context.Context, AbortRequest) (KnownOutcome, error)
	QueryOperation(context.Context, OperationKey) (QueryOutcome, error)
}

// RequireBackend 仅做 nil/typed-nil 组装守卫；它不解除 production NO-GO。
func RequireBackend(backend Backend) error {
	if backend == nil {
		return ErrBackendUnavailable
	}
	value := reflect.ValueOf(backend)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		if value.IsNil() {
			return ErrBackendUnavailable
		}
	}
	return nil
}

// OperationKey 从已验证 bootstrap request 派生全生命期幂等键。
func (r BootstrapRequest) OperationKey() (OperationKey, error) {
	if err := r.Validate(); err != nil {
		return OperationKey{}, err
	}
	return OperationKey{
		SchemaVersion: r.SchemaVersion, CommandOperationID: r.CommandOperationID,
		CommandOperationDigest: r.CommandOperationDigest, AttemptOperationID: r.AttemptOperationID,
		AttemptOperationDigest: r.AttemptOperationDigest, IdempotencyKeyDigest: r.IdempotencyKeyDigest,
		BootstrapRequestDigest: r.BootstrapRequestDigest,
	}, nil
}

// Validate 验证 operation key 的完整性，不把 key 当作授权凭证。
func (k OperationKey) Validate() error {
	if k.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema version", ErrInvalidOperationKey)
	}
	for _, item := range []struct{ name, value string }{
		{"command_operation_id", k.CommandOperationID}, {"attempt_operation_id", k.AttemptOperationID},
	} {
		if err := validateCanonicalText(ErrInvalidOperationKey, item.name, item.value, 160); err != nil {
			return err
		}
	}
	for _, item := range []struct{ name, value string }{
		{"command_operation_digest", k.CommandOperationDigest}, {"attempt_operation_digest", k.AttemptOperationDigest},
		{"idempotency_key_digest", k.IdempotencyKeyDigest}, {"bootstrap_request_digest", k.BootstrapRequestDigest},
	} {
		if err := validateDigest(ErrInvalidOperationKey, item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

// MatchOperationKey 强制 bootstrap、commit、abort 与 query 使用完全相同的幂等键。
func MatchOperationKey(expected, actual OperationKey) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if expected != actual {
		return ErrOperationConflict
	}
	return nil
}

// Validate 验证 bootstrap 所有 owner 绑定和 domain-separated request digest。
func (r BootstrapRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.ReservationGeneration <= 0 {
		return fmt.Errorf("%w: unsupported schema or reservation generation", ErrInvalidBootstrap)
	}
	for _, item := range []struct{ name, value string }{
		{"command_operation_id", r.CommandOperationID}, {"attempt_operation_id", r.AttemptOperationID},
		{"agent_id", r.AgentID}, {"provider_id", r.ProviderID}, {"reservation_id", r.ReservationID},
		{"source_fence_ref", r.SourceFenceRef}, {"target_fence_ref", r.TargetFenceRef},
		{"credential_lease_id", r.CredentialLeaseID},
	} {
		if err := validateCanonicalText(ErrInvalidBootstrap, item.name, item.value, 256); err != nil {
			return err
		}
	}
	for _, item := range bootstrapRequestDigests(r) {
		if err := validateDigest(ErrInvalidBootstrap, item.name, item.value); err != nil {
			return err
		}
	}
	want, err := ComputeBootstrapRequestDigest(r)
	if err != nil || want != r.BootstrapRequestDigest {
		return fmt.Errorf("%w: bootstrap request digest mismatch", ErrInvalidBootstrap)
	}
	return nil
}

// Validate 验证 prepared result 的形状和摘要，但不验证 receipt 声明是否真实。
func (r BootstrapResult) Validate() error {
	if err := validateBootstrapResultHeader(r); err != nil {
		return err
	}
	if err := validateBootstrapResultText(r); err != nil {
		return err
	}
	if err := validateBootstrapResultDigests(r); err != nil {
		return err
	}
	want, err := ComputeBootstrapResultDigest(r)
	if err != nil || want != r.ResultDigest {
		return fmt.Errorf("%w: bootstrap result digest mismatch", ErrInvalidResult)
	}
	return nil
}

// Validate 验证 commit 的 prepared request/result、CAS/credential 绑定与自摘要。
func (r CommitRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion || r.ActiveCurrentBindingVersion <= 0 {
		return fmt.Errorf("%w: unsupported schema or binding version", ErrInvalidCommit)
	}
	if err := r.Operation.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCommit, err)
	}
	if err := validateCanonicalText(ErrInvalidCommit, "active_current_binding_incarnation_id", r.ActiveCurrentBindingIncarnationID, 256); err != nil {
		return err
	}
	if r.PreparedRequestDigest != r.Operation.BootstrapRequestDigest {
		return fmt.Errorf("%w: prepared request digest does not match operation bootstrap request digest", ErrInvalidCommit)
	}
	for _, item := range []struct{ name, value string }{
		{"prepared_request_digest", r.PreparedRequestDigest}, {"prepared_result_digest", r.PreparedResultDigest},
		{"active_current_binding_digest", r.ActiveCurrentBindingDigest},
		{"activated_credential_digest", r.ActivatedCredentialDigest}, {"commit_operation_digest", r.CommitOperationDigest},
	} {
		if err := validateDigest(ErrInvalidCommit, item.name, item.value); err != nil {
			return err
		}
	}
	want, err := ComputeCommitOperationDigest(r)
	if err != nil || want != r.CommitOperationDigest {
		return fmt.Errorf("%w: commit operation digest mismatch", ErrInvalidCommit)
	}
	return nil
}

// Validate 验证 abort 的 prepared request/result、cleanup/evidence 绑定与自摘要。
func (r AbortRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema version", ErrInvalidAbort)
	}
	if err := r.Operation.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAbort, err)
	}
	if r.PreparedRequestDigest != r.Operation.BootstrapRequestDigest {
		return fmt.Errorf("%w: prepared request digest does not match operation bootstrap request digest", ErrInvalidAbort)
	}
	for _, item := range []struct{ name, value string }{
		{"cleanup_operation_id", r.CleanupOperationID}, {"cleanup_evidence_ref", r.CleanupEvidenceRef},
	} {
		if err := validateCanonicalText(ErrInvalidAbort, item.name, item.value, 512); err != nil {
			return err
		}
	}
	for _, item := range []struct{ name, value string }{
		{"prepared_request_digest", r.PreparedRequestDigest}, {"prepared_result_digest", r.PreparedResultDigest},
		{"cleanup_operation_digest", r.CleanupOperationDigest},
		{"cleanup_evidence_digest", r.CleanupEvidenceDigest}, {"abort_operation_digest", r.AbortOperationDigest},
	} {
		if err := validateDigest(ErrInvalidAbort, item.name, item.value); err != nil {
			return err
		}
	}
	want, err := ComputeAbortOperationDigest(r)
	if err != nil || want != r.AbortOperationDigest {
		return fmt.Errorf("%w: abort operation digest mismatch", ErrInvalidAbort)
	}
	return nil
}

// Validate 验证 committed query 分支完整复制了原 commit operation。
func (r CommitResult) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: invalid committed result schema", ErrInvalidOutcome)
	}
	request := CommitRequest{
		SchemaVersion: SchemaVersion, Operation: r.Operation, PreparedRequestDigest: r.PreparedRequestDigest,
		PreparedResultDigest:              r.PreparedResultDigest,
		ActiveCurrentBindingDigest:        r.ActiveCurrentBindingDigest,
		ActiveCurrentBindingVersion:       r.ActiveCurrentBindingVersion,
		ActiveCurrentBindingIncarnationID: r.ActiveCurrentBindingIncarnationID,
		ActivatedCredentialDigest:         r.ActivatedCredentialDigest, CommitOperationDigest: r.CommitOperationDigest,
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: invalid committed result: %v", ErrInvalidOutcome, err)
	}
	return nil
}

// Validate 验证 aborted query 分支完整复制了原 abort operation。
func (r AbortResult) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: invalid aborted result schema", ErrInvalidOutcome)
	}
	request := AbortRequest{
		SchemaVersion: SchemaVersion, Operation: r.Operation, PreparedRequestDigest: r.PreparedRequestDigest,
		PreparedResultDigest: r.PreparedResultDigest,
		CleanupOperationID:   r.CleanupOperationID, CleanupOperationDigest: r.CleanupOperationDigest,
		CleanupEvidenceRef: r.CleanupEvidenceRef, CleanupEvidenceDigest: r.CleanupEvidenceDigest,
		AbortOperationDigest: r.AbortOperationDigest,
	}
	if err := request.Validate(); err != nil {
		return fmt.Errorf("%w: invalid aborted result: %v", ErrInvalidOutcome, err)
	}
	return nil
}

// Validate 验证 unknown 分支的原 operation 键和非授权 evidence 引用。
func (r OutcomeUnknownResult) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: invalid unknown result schema", ErrInvalidOutcome)
	}
	if err := r.Operation.Validate(); err != nil {
		return fmt.Errorf("%w: invalid unknown operation: %v", ErrInvalidOutcome, err)
	}
	if err := validateCanonicalText(ErrInvalidOutcome, "reason_code", r.ReasonCode, 128); err != nil {
		return err
	}
	if err := validateCanonicalText(ErrInvalidOutcome, "evidence_ref", r.EvidenceRef, 512); err != nil {
		return err
	}
	return validateDigest(ErrInvalidOutcome, "evidence_digest", r.EvidenceDigest)
}

// Validate 验证 mutation outcome 只含 prepared/committed/aborted 中与 status 匹配的一个分支。
func (o KnownOutcome) Validate() error {
	if o.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema version", ErrInvalidOutcome)
	}
	if err := o.Operation.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidOutcome, err)
	}
	return validateOutcomeBranches(o.Operation, o.Status, o.Prepared, o.Committed, o.Aborted, nil)
}

// Validate 验证 query outcome 的四分支 one-hot 与 operation key 一致性。
func (o QueryOutcome) Validate() error {
	if o.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema version", ErrInvalidOutcome)
	}
	if err := o.Operation.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidOutcome, err)
	}
	return validateOutcomeBranches(o.Operation, o.Status, o.Prepared, o.Committed, o.Aborted, o.OutcomeUnknown)
}

// validateOutcomeBranches 先锁定 presence one-hot，再把单一 status 分派交给局部验证。
func validateOutcomeBranches(key OperationKey, status string, prepared *BootstrapResult, committed *CommitResult, aborted *AbortResult, unknown *OutcomeUnknownResult) error {
	if countOutcomeBranches(prepared, committed, aborted, unknown) != 1 {
		return fmt.Errorf("%w: outcome presence must be one-hot", ErrInvalidOutcome)
	}
	switch status {
	case StatusPrepared:
		return validatePreparedOutcome(key, prepared)
	case StatusCommitted:
		return validateCommittedOutcome(key, committed)
	case StatusAborted:
		return validateAbortedOutcome(key, aborted)
	case StatusOutcomeUnknown:
		return validateUnknownOutcome(key, unknown)
	default:
		return fmt.Errorf("%w: unsupported status %q", ErrInvalidOutcome, status)
	}
}

// countOutcomeBranches 统计四个可选 outcome 分支的实际 presence。
func countOutcomeBranches(prepared *BootstrapResult, committed *CommitResult, aborted *AbortResult, unknown *OutcomeUnknownResult) int {
	present := 0
	for _, branch := range []any{prepared, committed, aborted, unknown} {
		value := reflect.ValueOf(branch)
		if value.IsValid() && !value.IsNil() {
			present++
		}
	}
	return present
}

// validatePreparedOutcome 验证 prepared 分支和 envelope 使用同一 operation key。
func validatePreparedOutcome(key OperationKey, result *BootstrapResult) error {
	if result == nil || result.Operation != key {
		return fmt.Errorf("%w: prepared status/branch mismatch", ErrInvalidOutcome)
	}
	if err := result.Validate(); err != nil {
		return fmt.Errorf("%w: invalid prepared branch", ErrInvalidOutcome)
	}
	return nil
}

// validateCommittedOutcome 验证 committed 分支和 envelope 使用同一 operation key。
func validateCommittedOutcome(key OperationKey, result *CommitResult) error {
	if result == nil || result.Operation != key {
		return fmt.Errorf("%w: committed status/branch mismatch", ErrInvalidOutcome)
	}
	return result.Validate()
}

// validateAbortedOutcome 验证 aborted 分支和 envelope 使用同一 operation key。
func validateAbortedOutcome(key OperationKey, result *AbortResult) error {
	if result == nil || result.Operation != key {
		return fmt.Errorf("%w: aborted status/branch mismatch", ErrInvalidOutcome)
	}
	return result.Validate()
}

// validateUnknownOutcome 验证 query-only unknown 分支和 envelope 使用同一 operation key。
func validateUnknownOutcome(key OperationKey, result *OutcomeUnknownResult) error {
	if result == nil || result.Operation != key {
		return fmt.Errorf("%w: unknown status/branch mismatch", ErrInvalidOutcome)
	}
	return result.Validate()
}

// validateBootstrapResultHeader 验证 prepared discriminator、operation 和 owner generation。
func validateBootstrapResultHeader(result BootstrapResult) error {
	if result.SchemaVersion != SchemaVersion || result.Status != StatusPrepared {
		return fmt.Errorf("%w: result must use prepared schema", ErrInvalidResult)
	}
	if result.ProcessGeneration <= 0 || result.RuntimeOwnerGeneration <= 0 {
		return fmt.Errorf("%w: result generations must be positive", ErrInvalidResult)
	}
	if err := result.Operation.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidResult, err)
	}
	return nil
}

// validateBootstrapResultText 验证 result 中的 opaque identity/ref 都是有界 canonical text。
func validateBootstrapResultText(result BootstrapResult) error {
	for _, item := range []struct{ name, value string }{
		{"opaque_handle", result.OpaqueHandle}, {"provider_thread_identity", result.ProviderThreadIdentity},
		{"provider_session_identity", result.ProviderSessionIdentity}, {"runtime_epoch", result.RuntimeEpoch},
		{"external_epoch", result.ExternalEpoch}, {"active_process_receipt_ref", result.ActiveProcessReceiptRef},
		{"runtime_owner_id", result.RuntimeOwnerID},
	} {
		if err := validateCanonicalText(ErrInvalidResult, item.name, item.value, 512); err != nil {
			return err
		}
	}
	return nil
}

// validateBootstrapResultDigests 验证 result 中所有 owner/launch 摘要形状。
func validateBootstrapResultDigests(result BootstrapResult) error {
	for _, item := range []struct{ name, value string }{
		{"active_process_receipt_digest", result.ActiveProcessReceiptDigest}, {"executable_digest", result.ExecutableDigest},
		{"argv_digest", result.ArgvDigest}, {"env_digest", result.EnvDigest}, {"cwd_digest", result.CWDDigest},
		{"result_digest", result.ResultDigest},
	} {
		if err := validateDigest(ErrInvalidResult, item.name, item.value); err != nil {
			return err
		}
	}
	return nil
}

func bootstrapRequestDigests(r BootstrapRequest) []struct{ name, value string } {
	return []struct{ name, value string }{
		{"command_operation_digest", r.CommandOperationDigest}, {"attempt_operation_digest", r.AttemptOperationDigest},
		{"agent_digest", r.AgentDigest}, {"provider_digest", r.ProviderDigest}, {"model_digest", r.ModelDigest},
		{"cwd_digest", r.CWDDigest}, {"source_fence_digest", r.SourceFenceDigest},
		{"target_fence_digest", r.TargetFenceDigest}, {"credential_lease_digest", r.CredentialLeaseDigest},
		{"idempotency_key_digest", r.IdempotencyKeyDigest}, {"bootstrap_request_digest", r.BootstrapRequestDigest},
	}
}

// validateCanonicalText 禁止空值、边缘空白、NUL、非 UTF-8 或超界合同文本。
func validateCanonicalText(base error, name, value string, limit int) error {
	if value == "" || strings.TrimSpace(value) != value || len(value) > limit || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("%w: %s must be bounded canonical text", base, name)
	}
	return nil
}

// validateDigest 验证小写十六进制 SHA-256 摘要形状。
func validateDigest(base error, name, value string) error {
	if len(value) != 64 || strings.ToLower(value) != value {
		return fmt.Errorf("%w: %s must be a lowercase 256-bit digest", base, name)
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return fmt.Errorf("%w: %s is not hexadecimal", base, name)
		}
	}
	return nil
}
