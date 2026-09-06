// Package managedruntime 定义未来独立 managed runtime supervisor 的 transport-neutral 合同，
// 并提供一个明确标注为 candidate-only 的内存状态机用于验证该合同。
//
// 本包只定义 PREPARE、COMMIT、ABORT、QUERY 的值对象与关闭语义，不启动 helper，
// 不持有进程、不连接 provider、不装配 Fx，也不读写数据库。
package managedruntime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// SchemaVersion 是当前 managed runtime supervisor 合同版本。
	SchemaVersion int64 = 1

	// StateAbsent 表示原 operation 没有任何已确认的 supervisor 记录。
	StateAbsent State = "absent"
	// StatePrepared 表示 helper 已停驻且 prepared receipt 已确认。
	StatePrepared State = "prepared"
	// StateCommitted 表示原 prepared runtime 已完成激活。
	StateCommitted State = "committed"
	// StateAborted 表示原 prepared runtime 已按 cleanup evidence 关闭。
	StateAborted State = "aborted"
	// StateOutcomeUnknown 表示副作用可能发生但权威结果暂不可判定；该状态只由 Query 返回。
	StateOutcomeUnknown State = "outcome_unknown"
)

var (
	// ErrInvalidPrepareRequest 表示 PREPARE 输入不完整、非 canonical 或摘要不匹配。
	ErrInvalidPrepareRequest = errors.New("managed runtime prepare request is invalid")
	// ErrInvalidPreparedReceipt 表示 prepared receipt 缺稳定进程身份或已被篡改。
	ErrInvalidPreparedReceipt = errors.New("managed runtime prepared receipt is invalid")
	// ErrInvalidCommitRequest 表示 COMMIT 没有绑定原 prepared receipt 或激活摘要。
	ErrInvalidCommitRequest = errors.New("managed runtime commit request is invalid")
	// ErrInvalidAbortRequest 表示 ABORT 没有绑定原 prepared receipt 或 cleanup evidence。
	ErrInvalidAbortRequest = errors.New("managed runtime abort request is invalid")
	// ErrInvalidClosedReceipt 表示 COMMIT/ABORT receipt 的终态或摘要非法。
	ErrInvalidClosedReceipt = errors.New("managed runtime closed receipt is invalid")
	// ErrInvalidQuery 表示 QUERY 输入或返回的关闭状态非法。
	ErrInvalidQuery = errors.New("managed runtime query contract is invalid")
	// ErrOperationConflict 表示同一 operation ID 被另一 request digest 重用。
	ErrOperationConflict = errors.New("managed runtime operation conflicts with the original digest")
	// ErrOperationNotFound 表示 receipt 不属于被查询的 operation ID。
	ErrOperationNotFound = errors.New("managed runtime operation was not found")
	// ErrProductionNoGo 表示独立 supervisor 的强制生产依赖尚未全部具备。
	ErrProductionNoGo = errors.New("managed runtime production prerequisites are not satisfied")
)

// State 是 QUERY 返回的关闭状态集合。
type State string

// PrepareRequest 只绑定 managed-launch bootstrap operation 与真实启动面的 canonical 摘要。
//
// Provider、executable、cwd、argv 和 environment 的原文不得进入本合同。
type PrepareRequest struct {
	SchemaVersion              int64  `json:"schema_version"`
	OperationID                string `json:"operation_id"`
	BootstrapOperationID       string `json:"bootstrap_operation_id"`
	BootstrapOperationDigest   string `json:"bootstrap_operation_digest"`
	ProviderDigest             string `json:"provider_digest"`
	ExecutableDigest           string `json:"executable_digest"`
	WorkingDirectoryDigest     string `json:"working_directory_digest"`
	ArgumentVectorDigest       string `json:"argument_vector_digest"`
	EnvironmentDigest          string `json:"environment_digest"`
	ExpectedRuntimeWriterEpoch string `json:"expected_runtime_writer_epoch"`
	RequestDigest              string `json:"request_digest"`
}

// StableProcessIdentity 是 prepared receipt 所需的最小 PID-reuse-safe 身份。
//
// 本类型有意不复用 pidregistry.StableProcessIdentity：后者还包含协作终止 endpoint
// 与 raw termination token；ChildInfo 还包含 mutable metadata。二者都不适合跨 transport
// receipt。PID 绝不能脱离 start token、executable identity 和 digest 单独授权。
type StableProcessIdentity struct {
	PID                int64  `json:"pid"`
	ProcessStartToken  string `json:"process_start_token"`
	ExecutableIdentity string `json:"executable_identity"`
	ExecutableDigest   string `json:"executable_digest"`
}

// PreparedReceipt 证明真实 helper 已停驻，并绑定可重连 runtime 与 provider 身份。
//
// OpaqueHandleRef 不是进程 handle、credential 或授权 fence；持久化和日志只能使用其摘要。
type PreparedReceipt struct {
	SchemaVersion      int64                 `json:"schema_version"`
	OperationID        string                `json:"operation_id"`
	RequestDigest      string                `json:"request_digest"`
	State              State                 `json:"state"`
	OpaqueHandleRef    string                `json:"opaque_handle_ref"`
	OpaqueHandleDigest string                `json:"opaque_handle_digest"`
	Process            StableProcessIdentity `json:"process"`
	ProviderThreadID   string                `json:"provider_thread_id"`
	ProviderSessionID  string                `json:"provider_session_id"`
	ExternalEpoch      string                `json:"external_epoch"`
	RuntimeOwnerID     string                `json:"runtime_owner_id"`
	RuntimeGeneration  int64                 `json:"runtime_generation"`
	ProcessGeneration  int64                 `json:"process_generation"`
	RuntimeWriterEpoch string                `json:"runtime_writer_epoch"`
	ReceiptRef         string                `json:"receipt_ref"`
	ReceiptDigest      string                `json:"receipt_digest"`
}

// CommitRequest 只允许激活原 prepared receipt，并绑定 active binding 与 credential 摘要。
type CommitRequest struct {
	SchemaVersion                 int64  `json:"schema_version"`
	OperationID                   string `json:"operation_id"`
	PreparedOperationID           string `json:"prepared_operation_id"`
	PreparedRequestDigest         string `json:"prepared_request_digest"`
	ExpectedPreparedReceiptRef    string `json:"expected_prepared_receipt_ref"`
	ExpectedPreparedReceiptDigest string `json:"expected_prepared_receipt_digest"`
	ActiveBindingDigest           string `json:"active_binding_digest"`
	CredentialDigest              string `json:"credential_digest"`
	RequestDigest                 string `json:"request_digest"`
}

// AbortRequest 只允许关闭原 prepared receipt，并绑定可验证的 cleanup evidence。
type AbortRequest struct {
	SchemaVersion                 int64  `json:"schema_version"`
	OperationID                   string `json:"operation_id"`
	PreparedOperationID           string `json:"prepared_operation_id"`
	PreparedRequestDigest         string `json:"prepared_request_digest"`
	ExpectedPreparedReceiptRef    string `json:"expected_prepared_receipt_ref"`
	ExpectedPreparedReceiptDigest string `json:"expected_prepared_receipt_digest"`
	CleanupEvidenceRef            string `json:"cleanup_evidence_ref"`
	CleanupEvidenceDigest         string `json:"cleanup_evidence_digest"`
	RequestDigest                 string `json:"request_digest"`
}

// ClosedReceipt 是 COMMIT 或 ABORT 的确定终态 receipt；它永远不表示 outcome_unknown。
type ClosedReceipt struct {
	SchemaVersion         int64  `json:"schema_version"`
	PreparedOperationID   string `json:"prepared_operation_id"`
	PreparedRequestDigest string `json:"prepared_request_digest"`
	OperationID           string `json:"operation_id"`
	OperationDigest       string `json:"operation_digest"`
	State                 State  `json:"state"`
	PreparedReceiptRef    string `json:"prepared_receipt_ref"`
	PreparedReceiptDigest string `json:"prepared_receipt_digest"`
	ClosureEvidenceRef    string `json:"closure_evidence_ref"`
	ClosureEvidenceDigest string `json:"closure_evidence_digest"`
	ReceiptRef            string `json:"receipt_ref"`
	ReceiptDigest         string `json:"receipt_digest"`
}

// QueryRequest 只查询原 PREPARE operation ID 与 request digest，不创建第二个副作用 operation。
type QueryRequest struct {
	SchemaVersion   int64  `json:"schema_version"`
	OperationID     string `json:"operation_id"`
	OperationDigest string `json:"operation_digest"`
}

// QueryResult 是 absent、prepared、committed、aborted、outcome_unknown 的关闭并集。
//
// 每个 state 只允许自己分支的 receipt 字段出现；unknown 只能通过原 operation 查询收口。
type QueryResult struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	OperationDigest       string `json:"operation_digest"`
	State                 State  `json:"state"`
	PreparedReceiptRef    string `json:"prepared_receipt_ref"`
	PreparedReceiptDigest string `json:"prepared_receipt_digest"`
	ClosedReceiptRef      string `json:"closed_receipt_ref"`
	ClosedReceiptDigest   string `json:"closed_receipt_digest"`
	UncertaintyReceiptRef string `json:"uncertainty_receipt_ref"`
	UncertaintyDigest     string `json:"uncertainty_digest"`
	ResultDigest          string `json:"result_digest"`
}

// DependencyEvidence 是 production prerequisite 的不可逆证明引用。
type DependencyEvidence struct {
	Ref    string `json:"ref"`
	Digest string `json:"digest"`
}

// ProductionPrerequisites 固定独立 supervisor 上线前不能缺少的四个真实依赖。
//
// 仅有本包合同不代表 production ready。没有真实 helper、可重连 endpoint、durable
// store 或 receipt signature verifier 中任一项时必须返回 ErrProductionNoGo，不能以内存
// fake、IsRunning、PID 存活探测或 unsigned receipt 代替。
type ProductionPrerequisites struct {
	SchemaVersion         int64              `json:"schema_version"`
	RealHelper            DependencyEvidence `json:"real_helper"`
	ReconnectableEndpoint DependencyEvidence `json:"reconnectable_endpoint"`
	DurableStore          DependencyEvidence `json:"durable_store"`
	SignatureVerifier     DependencyEvidence `json:"signature_verifier"`
}

// Supervisor 是未来独立 owner 的窄端口。CandidateSupervisor 仅用于本地合同验证，
// 不构成真实 supervisor：它不启动 helper、不连接 provider、不持久化 receipt，也不具备
// production prerequisites 的真实性证明。
//
// 每个 mutation 必须按 operation ID + request digest 幂等：同 key 同 digest 重放原
// receipt，同 key 异 digest 返回 ErrOperationConflict。mutation 返回不确定时不得换 key
// 重试；StateOutcomeUnknown 只可由 Query 原 operation 得到。
type Supervisor interface {
	Prepare(context.Context, PrepareRequest) (PreparedReceipt, error)
	Commit(context.Context, CommitRequest) (ClosedReceipt, error)
	Abort(context.Context, AbortRequest) (ClosedReceipt, error)
	Query(context.Context, QueryRequest) (QueryResult, error)
}

// Validate 验证 PREPARE 的固定版本、摘要输入和自摘要。
func (r PrepareRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return invalid(ErrInvalidPrepareRequest, "schema_version must equal 1")
	}
	if err := validateTexts(ErrInvalidPrepareRequest,
		textField{"operation_id", r.OperationID, 128},
		textField{"bootstrap_operation_id", r.BootstrapOperationID, 128},
		textField{"expected_runtime_writer_epoch", r.ExpectedRuntimeWriterEpoch, 128},
	); err != nil {
		return err
	}
	if err := validateDigests(ErrInvalidPrepareRequest,
		digestField{"bootstrap_operation_digest", r.BootstrapOperationDigest},
		digestField{"provider_digest", r.ProviderDigest},
		digestField{"executable_digest", r.ExecutableDigest},
		digestField{"working_directory_digest", r.WorkingDirectoryDigest},
		digestField{"argument_vector_digest", r.ArgumentVectorDigest},
		digestField{"environment_digest", r.EnvironmentDigest},
		digestField{"request_digest", r.RequestDigest},
	); err != nil {
		return err
	}
	want, err := ComputePrepareRequestDigest(r)
	return validateComputedDigest(ErrInvalidPrepareRequest, r.RequestDigest, want, err)
}

// Validate 验证 prepared receipt 绑定完整稳定身份、owner generations 与自摘要。
func (r PreparedReceipt) Validate() error {
	if err := r.validateHeader(); err != nil {
		return err
	}
	if err := validateTexts(ErrInvalidPreparedReceipt,
		textField{"operation_id", r.OperationID, 128}, textField{"opaque_handle_ref", r.OpaqueHandleRef, 512},
		textField{"external_epoch", r.ExternalEpoch, 128}, textField{"runtime_owner_id", r.RuntimeOwnerID, 128},
		textField{"runtime_writer_epoch", r.RuntimeWriterEpoch, 128}, textField{"receipt_ref", r.ReceiptRef, 512},
	); err != nil {
		return err
	}
	if r.ProviderThreadID == "" && r.ProviderSessionID == "" {
		return invalid(ErrInvalidPreparedReceipt, "provider thread or session identity is required")
	}
	if err := validateOptionalTexts(ErrInvalidPreparedReceipt,
		textField{"provider_thread_id", r.ProviderThreadID, 256},
		textField{"provider_session_id", r.ProviderSessionID, 256},
	); err != nil {
		return err
	}
	if err := r.Process.validate(); err != nil {
		return err
	}
	if err := validateDigests(ErrInvalidPreparedReceipt,
		digestField{"request_digest", r.RequestDigest}, digestField{"opaque_handle_digest", r.OpaqueHandleDigest},
		digestField{"receipt_digest", r.ReceiptDigest},
	); err != nil {
		return err
	}
	want, err := ComputePreparedReceiptDigest(r)
	return validateComputedDigest(ErrInvalidPreparedReceipt, r.ReceiptDigest, want, err)
}

// validateHeader 验证 prepared receipt 的版本、状态与 generation 基本约束。
func (r PreparedReceipt) validateHeader() error {
	if r.SchemaVersion != SchemaVersion || r.State != StatePrepared || r.RuntimeGeneration <= 0 || r.ProcessGeneration <= 0 {
		return invalid(ErrInvalidPreparedReceipt, "version, state, and generations must be valid")
	}
	return nil
}

func (i StableProcessIdentity) validate() error {
	if i.PID <= 1 {
		return invalid(ErrInvalidPreparedReceipt, "pid must identify a child process")
	}
	if err := validateTexts(ErrInvalidPreparedReceipt,
		textField{"process_start_token", i.ProcessStartToken, 512},
		textField{"executable_identity", i.ExecutableIdentity, 1024},
	); err != nil {
		return err
	}
	return validateDigest(ErrInvalidPreparedReceipt, digestField{"process.executable_digest", i.ExecutableDigest})
}

// MatchPrepare 确认 receipt 属于同一 operation + digest，并复核 writer/executable fence。
func MatchPrepare(request PrepareRequest, receipt PreparedReceipt) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	if request.OperationID != receipt.OperationID {
		return ErrOperationNotFound
	}
	if request.RequestDigest != receipt.RequestDigest {
		return ErrOperationConflict
	}
	if request.ExecutableDigest != receipt.Process.ExecutableDigest ||
		request.ExpectedRuntimeWriterEpoch != receipt.RuntimeWriterEpoch {
		return ErrOperationConflict
	}
	return nil
}

// Validate 验证 COMMIT 只引用 expected prepared receipt 与激活摘要。
func (r CommitRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return invalid(ErrInvalidCommitRequest, "schema_version must equal 1")
	}
	if err := validateTexts(ErrInvalidCommitRequest,
		textField{"operation_id", r.OperationID, 128}, textField{"prepared_operation_id", r.PreparedOperationID, 128},
		textField{"expected_prepared_receipt_ref", r.ExpectedPreparedReceiptRef, 512},
	); err != nil {
		return err
	}
	if err := validateDigests(ErrInvalidCommitRequest,
		digestField{"prepared_request_digest", r.PreparedRequestDigest},
		digestField{"expected_prepared_receipt_digest", r.ExpectedPreparedReceiptDigest},
		digestField{"active_binding_digest", r.ActiveBindingDigest}, digestField{"credential_digest", r.CredentialDigest},
		digestField{"request_digest", r.RequestDigest},
	); err != nil {
		return err
	}
	want, err := ComputeCommitRequestDigest(r)
	return validateComputedDigest(ErrInvalidCommitRequest, r.RequestDigest, want, err)
}

// Validate 验证 ABORT 只引用 expected prepared receipt 与 cleanup evidence。
func (r AbortRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return invalid(ErrInvalidAbortRequest, "schema_version must equal 1")
	}
	if err := validateTexts(ErrInvalidAbortRequest,
		textField{"operation_id", r.OperationID, 128}, textField{"prepared_operation_id", r.PreparedOperationID, 128},
		textField{"expected_prepared_receipt_ref", r.ExpectedPreparedReceiptRef, 512},
		textField{"cleanup_evidence_ref", r.CleanupEvidenceRef, 512},
	); err != nil {
		return err
	}
	if err := validateDigests(ErrInvalidAbortRequest,
		digestField{"prepared_request_digest", r.PreparedRequestDigest},
		digestField{"expected_prepared_receipt_digest", r.ExpectedPreparedReceiptDigest},
		digestField{"cleanup_evidence_digest", r.CleanupEvidenceDigest}, digestField{"request_digest", r.RequestDigest},
	); err != nil {
		return err
	}
	want, err := ComputeAbortRequestDigest(r)
	return validateComputedDigest(ErrInvalidAbortRequest, r.RequestDigest, want, err)
}

// Validate 验证 closed receipt 只能是 committed 或 aborted，且绑定全部 closure evidence。
func (r ClosedReceipt) Validate() error {
	if r.SchemaVersion != SchemaVersion || (r.State != StateCommitted && r.State != StateAborted) {
		return invalid(ErrInvalidClosedReceipt, "closed state must be committed or aborted")
	}
	if err := validateTexts(ErrInvalidClosedReceipt,
		textField{"prepared_operation_id", r.PreparedOperationID, 128}, textField{"operation_id", r.OperationID, 128},
		textField{"prepared_receipt_ref", r.PreparedReceiptRef, 512}, textField{"closure_evidence_ref", r.ClosureEvidenceRef, 512},
		textField{"receipt_ref", r.ReceiptRef, 512},
	); err != nil {
		return err
	}
	if err := validateDigests(ErrInvalidClosedReceipt,
		digestField{"prepared_request_digest", r.PreparedRequestDigest}, digestField{"operation_digest", r.OperationDigest},
		digestField{"prepared_receipt_digest", r.PreparedReceiptDigest}, digestField{"closure_evidence_digest", r.ClosureEvidenceDigest},
		digestField{"receipt_digest", r.ReceiptDigest},
	); err != nil {
		return err
	}
	want, err := ComputeClosedReceiptDigest(r)
	return validateComputedDigest(ErrInvalidClosedReceipt, r.ReceiptDigest, want, err)
}

// Validate 验证 QUERY 始终绑定原 mutation operation + digest。
func (r QueryRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return invalid(ErrInvalidQuery, "schema_version must equal 1")
	}
	if err := validateCanonicalText(ErrInvalidQuery, textField{"operation_id", r.OperationID, 128}); err != nil {
		return err
	}
	return validateDigest(ErrInvalidQuery, digestField{"operation_digest", r.OperationDigest})
}

// Validate 验证 QUERY 的 state-specific presence、operation binding 与自摘要。
func (r QueryResult) Validate() error {
	if err := (QueryRequest{SchemaVersion: r.SchemaVersion, OperationID: r.OperationID, OperationDigest: r.OperationDigest}).Validate(); err != nil {
		return err
	}
	if err := r.validatePresence(); err != nil {
		return err
	}
	if err := validateOptionalTexts(ErrInvalidQuery,
		textField{"prepared_receipt_ref", r.PreparedReceiptRef, 512},
		textField{"closed_receipt_ref", r.ClosedReceiptRef, 512},
		textField{"uncertainty_receipt_ref", r.UncertaintyReceiptRef, 512},
	); err != nil {
		return err
	}
	if err := validateOptionalDigests(ErrInvalidQuery,
		digestField{"prepared_receipt_digest", r.PreparedReceiptDigest},
		digestField{"closed_receipt_digest", r.ClosedReceiptDigest},
		digestField{"uncertainty_digest", r.UncertaintyDigest},
		digestField{"result_digest", r.ResultDigest},
	); err != nil {
		return err
	}
	want, err := ComputeQueryResultDigest(r)
	return validateComputedDigest(ErrInvalidQuery, r.ResultDigest, want, err)
}

type queryPresence struct {
	prepared bool
	closed   bool
	unknown  bool
}

// validatePresence 验证 QUERY result 的状态与 receipt 分支一一对应。
func (r QueryResult) validatePresence() error {
	if hasPartialPair(r.PreparedReceiptRef, r.PreparedReceiptDigest) ||
		hasPartialPair(r.ClosedReceiptRef, r.ClosedReceiptDigest) ||
		hasPartialPair(r.UncertaintyReceiptRef, r.UncertaintyDigest) {
		return invalid(ErrInvalidQuery, "receipt ref and digest must appear together")
	}
	got := queryPresence{
		prepared: completePair(r.PreparedReceiptRef, r.PreparedReceiptDigest),
		closed:   completePair(r.ClosedReceiptRef, r.ClosedReceiptDigest),
		unknown:  completePair(r.UncertaintyReceiptRef, r.UncertaintyDigest),
	}
	want, ok := map[State]queryPresence{
		StateAbsent:         {},
		StatePrepared:       {prepared: true},
		StateCommitted:      {prepared: true, closed: true},
		StateAborted:        {prepared: true, closed: true},
		StateOutcomeUnknown: {unknown: true},
	}[r.State]
	if !ok || got != want {
		return invalid(ErrInvalidQuery, "state-specific receipt presence is invalid")
	}
	return nil
}

// Validate 强制四类 production evidence 全部存在；本函数不把 evidence ref 冒充实时探测。
func (p ProductionPrerequisites) Validate() error {
	if p.SchemaVersion != SchemaVersion {
		return invalid(ErrProductionNoGo, "schema_version must equal 1")
	}
	for name, evidence := range map[string]DependencyEvidence{
		"real_helper": p.RealHelper, "reconnectable_endpoint": p.ReconnectableEndpoint,
		"durable_store": p.DurableStore, "signature_verifier": p.SignatureVerifier,
	} {
		if err := evidence.validate(name); err != nil {
			return err
		}
	}
	return nil
}

func (e DependencyEvidence) validate(name string) error {
	if err := validateCanonicalText(ErrProductionNoGo, textField{name + ".ref", e.Ref, 512}); err != nil {
		return err
	}
	return validateDigest(ErrProductionNoGo, digestField{name + ".digest", e.Digest})
}

type textField struct {
	name  string
	value string
	limit int
}

type digestField struct {
	name  string
	value string
}

func validateTexts(base error, fields ...textField) error {
	for _, field := range fields {
		if err := validateCanonicalText(base, field); err != nil {
			return err
		}
	}
	return nil
}

// validateCanonicalText 验证文本字段非空、UTF-8 合法且不含控制字符。
func validateCanonicalText(base error, field textField) error {
	if field.value == "" || len(field.value) > field.limit || !utf8.ValidString(field.value) || strings.TrimSpace(field.value) != field.value {
		return invalid(base, field.name+" must be bounded canonical text")
	}
	for _, char := range field.value {
		if unicode.IsControl(char) {
			return invalid(base, field.name+" must be bounded canonical text")
		}
	}
	return nil
}

func validateOptionalTexts(base error, fields ...textField) error {
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		if err := validateCanonicalText(base, field); err != nil {
			return err
		}
	}
	return nil
}

func validateDigests(base error, fields ...digestField) error {
	for _, field := range fields {
		if err := validateDigest(base, field); err != nil {
			return err
		}
	}
	return nil
}

// validateDigest 验证字段是小写 SHA-256 十六进制摘要。
func validateDigest(base error, field digestField) error {
	if len(field.value) != 64 {
		return invalid(base, field.name+" must be lowercase sha256 hex")
	}
	for _, char := range field.value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return invalid(base, field.name+" must be lowercase sha256 hex")
		}
	}
	return nil
}

func validateOptionalDigests(base error, fields ...digestField) error {
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		if err := validateDigest(base, field); err != nil {
			return err
		}
	}
	return nil
}

func validateComputedDigest(base error, got string, want string, digestErr error) error {
	if digestErr != nil || got != want {
		return invalid(base, "domain-separated digest mismatch")
	}
	return nil
}

func completePair(ref, digest string) bool { return ref != "" && digest != "" }

func hasPartialPair(ref, digest string) bool { return (ref == "") != (digest == "") }

func invalid(base error, reason string) error { return fmt.Errorf("%w: %s", base, reason) }
