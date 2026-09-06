// Package issuer defines an isolated endpoint/process-receipt binding candidate.
//
// This package is a candidate-only seam. It does not issue bearer material,
// launch or stop a provider, open a database, or register an Fx component. A
// future owner may implement BindingCandidate only after a real managed-runtime
// supervisor and the provider-specific bridge have authoritative receipts.
package issuer

import (
	"context"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime"
)

const (
	SchemaVersion int64 = 1

	ProviderCodex       = "codex"
	ProviderClaude      = "claude"
	ProviderAntigravity = "agy"
	ProviderGrok        = "grok"

	StateCandidate      State = "candidate"
	StatePrepared       State = "prepared"
	StateBound          State = "bound"
	StateRevoked        State = "revoked"
	StateAborted        State = "aborted"
	StateOutcomeUnknown State = "outcome_unknown"

	TriggerPrepare Trigger = "prepare"
	TriggerBind    Trigger = "bind"
	TriggerRevoke  Trigger = "revoke"
	TriggerAbort   Trigger = "abort"
)

const (
	endpointDigestDomain        = "orch.managed-runtime.issuer.endpoint/v1"
	bindDigestDomain            = "orch.managed-runtime.issuer.bind/v1"
	bindingDigestDomain         = "orch.managed-runtime.issuer.binding/v1"
	providerProfileDigestDomain = "orch.managed-runtime.issuer.provider-profile/v1"
	abortDigestDomain           = "orch.managed-runtime.issuer.abort/v1"
	revokeDigestDomain          = "orch.managed-runtime.issuer.revoke/v1"
	closedDigestDomain          = "orch.managed-runtime.issuer.closed/v1"
	queryDigestDomain           = "orch.managed-runtime.issuer.query/v1"
)

var (
	ErrInvalidEndpoint            = errors.New("managed runtime endpoint binding is invalid")
	ErrInvalidBindRequest         = errors.New("managed runtime binding request is invalid")
	ErrInvalidBinding             = errors.New("managed runtime endpoint/process binding is invalid")
	ErrInvalidClosure             = errors.New("managed runtime binding closure is invalid")
	ErrInvalidQuery               = errors.New("managed runtime binding query is invalid")
	ErrInvalidTransition          = errors.New("managed runtime binding lifecycle transition is invalid")
	ErrOperationConflict          = errors.New("managed runtime binding operation conflicts with original digest")
	ErrReceiptVerifierUnavailable = errors.New("managed runtime process receipt verifier is unavailable")
	ErrReceiptVerificationFailed  = errors.New("managed runtime process receipt verification failed")
)

// State is the endpoint/process binding lifecycle. OutcomeUnknown is query-only.
// State 表示 endpoint/process binding 生命周期，OutcomeUnknown 仅允许 query 返回。
type State string

// Trigger identifies one explicit lifecycle transition.
// Trigger 表示一个显式生命周期迁移操作。
type Trigger string

// Endpoint is a public, token-free task endpoint identity.
type Endpoint struct {
	SchemaVersion int64  `json:"schema_version"`
	URL           string `json:"url"`
	InstanceID    string `json:"instance_id"`
	Digest        string `json:"digest"`
}

// BindRequest pairs one verified managed-runtime prepared receipt with an endpoint.
// CredentialDigest and CredentialLeaseID are non-secret references only.
type BindRequest struct {
	SchemaVersion                int64    `json:"schema_version"`
	OperationID                  string   `json:"operation_id"`
	PreparedOperationID          string   `json:"prepared_operation_id"`
	PreparedRequestDigest        string   `json:"prepared_request_digest"`
	CredentialLeaseID            string   `json:"credential_lease_id"`
	CredentialDigest             string   `json:"credential_digest"`
	ProviderProfileRef           string   `json:"provider_profile_ref"`
	ProviderProfileDigest        string   `json:"provider_profile_digest"`
	ParentExecutionIncarnationID string   `json:"parent_execution_incarnation_id"`
	ParentFenceDigest            string   `json:"parent_fence_digest"`
	ConnectionNonceDigest        string   `json:"connection_nonce_digest"`
	Endpoint                     Endpoint `json:"endpoint"`
	Provider                     string   `json:"provider"`
	RequestDigest                string   `json:"request_digest"`
}

// BindingReceipt records the exact process/endpoint pairing without raw handles or tokens.
type BindingReceipt struct {
	SchemaVersion                int64  `json:"schema_version"`
	OperationID                  string `json:"operation_id"`
	BindingRequestDigest         string `json:"binding_request_digest"`
	PreparedOperationID          string `json:"prepared_operation_id"`
	PreparedRequestDigest        string `json:"prepared_request_digest"`
	CredentialLeaseID            string `json:"credential_lease_id"`
	CredentialDigest             string `json:"credential_digest"`
	ProviderProfileRef           string `json:"provider_profile_ref"`
	ProviderProfileDigest        string `json:"provider_profile_digest"`
	ParentExecutionIncarnationID string `json:"parent_execution_incarnation_id"`
	ParentFenceDigest            string `json:"parent_fence_digest"`
	ConnectionNonceDigest        string `json:"connection_nonce_digest"`
	EndpointDigest               string `json:"endpoint_digest"`
	EndpointInstanceID           string `json:"endpoint_instance_id"`
	Provider                     string `json:"provider"`
	ProviderThreadID             string `json:"provider_thread_id"`
	ProviderSessionID            string `json:"provider_session_id"`
	RuntimeOwnerID               string `json:"runtime_owner_id"`
	RuntimeGeneration            int64  `json:"runtime_generation"`
	ProcessGeneration            int64  `json:"process_generation"`
	RuntimeWriterEpoch           string `json:"runtime_writer_epoch"`
	PreparedReceiptRef           string `json:"prepared_receipt_ref"`
	PreparedReceiptDigest        string `json:"prepared_receipt_digest"`
	State                        State  `json:"state"`
	ReceiptRef                   string `json:"receipt_ref"`
	ReceiptDigest                string `json:"receipt_digest"`
}

// AbortRequest closes a prepared runtime before endpoint binding is committed.
type AbortRequest struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	PreparedOperationID   string `json:"prepared_operation_id"`
	PreparedRequestDigest string `json:"prepared_request_digest"`
	PreparedReceiptRef    string `json:"prepared_receipt_ref"`
	PreparedReceiptDigest string `json:"prepared_receipt_digest"`
	CleanupEvidenceRef    string `json:"cleanup_evidence_ref"`
	CleanupEvidenceDigest string `json:"cleanup_evidence_digest"`
	RequestDigest         string `json:"request_digest"`
}

// RevokeRequest closes an already bound endpoint/process pairing.
type RevokeRequest struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	BindingOperationID    string `json:"binding_operation_id"`
	ExpectedReceiptRef    string `json:"expected_receipt_ref"`
	ExpectedReceiptDigest string `json:"expected_receipt_digest"`
	ClosureEvidenceRef    string `json:"closure_evidence_ref"`
	ClosureEvidenceDigest string `json:"closure_evidence_digest"`
	RequestDigest         string `json:"request_digest"`
}

// ClosedReceipt is a definitive abort or revoke receipt; unknown is query-only.
type ClosedReceipt struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	BindingOperationID    string `json:"binding_operation_id"`
	PreparedOperationID   string `json:"prepared_operation_id"`
	PreparedRequestDigest string `json:"prepared_request_digest"`
	ExpectedReceiptRef    string `json:"expected_receipt_ref"`
	ExpectedReceiptDigest string `json:"expected_receipt_digest"`
	State                 State  `json:"state"`
	OperationDigest       string `json:"operation_digest"`
	ClosureEvidenceRef    string `json:"closure_evidence_ref"`
	ClosureEvidenceDigest string `json:"closure_evidence_digest"`
	ReceiptRef            string `json:"receipt_ref"`
	ReceiptDigest         string `json:"receipt_digest"`
}

// QueryRequest addresses only the original binding operation and digest.
type QueryRequest struct {
	SchemaVersion   int64  `json:"schema_version"`
	OperationID     string `json:"operation_id"`
	OperationDigest string `json:"operation_digest"`
}

// QueryResult is the one-hot lifecycle projection; unknown is never a mutation result.
type QueryResult struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	OperationDigest       string `json:"operation_digest"`
	State                 State  `json:"state"`
	PreparedReceiptRef    string `json:"prepared_receipt_ref"`
	PreparedReceiptDigest string `json:"prepared_receipt_digest"`
	BindingReceiptRef     string `json:"binding_receipt_ref"`
	BindingReceiptDigest  string `json:"binding_receipt_digest"`
	ClosedReceiptRef      string `json:"closed_receipt_ref"`
	ClosedReceiptDigest   string `json:"closed_receipt_digest"`
	UncertaintyReceiptRef string `json:"uncertainty_receipt_ref"`
	UncertaintyDigest     string `json:"uncertainty_digest"`
	ResultDigest          string `json:"result_digest"`
}

// ProcessReceiptVerifier 是只读的 managedruntime process receipt 验证端口。
// 实现不得启动、停止或修改 provider；验证失败前不得创建 binding 副作用。
type ProcessReceiptVerifier interface {
	VerifyPrepared(context.Context, managedruntime.PrepareRequest, managedruntime.PreparedReceipt) error
}

var _ ProcessReceiptVerifier = (*managedruntime.ProcessReceiptVerifier)(nil)

// PreparedReceiptProof 是 VerifyPreparedReceipt 返回的不可伪造候选标记。
// 未通过 verifier 的零值 proof 不能通过 Validate，也不能进入绑定接口。
type PreparedReceiptProof struct {
	request  managedruntime.PrepareRequest
	receipt  managedruntime.PreparedReceipt
	verified bool
}

// String 返回脱敏 proof 摘要，避免默认格式输出进程路径或 opaque handle。
func (p PreparedReceiptProof) String() string { return "[VERIFIED_PREPARED_RECEIPT]" }

// GoString 保证 %#v 同样不展开 proof 内部进程身份。
func (p PreparedReceiptProof) GoString() string { return p.String() }

// BindingCandidate is the future endpoint/process binding owner seam. No implementation is provided here.
// BindingCandidate 是未来 endpoint/process binding owner 接缝；当前不提供实现。
type BindingCandidate interface {
	Bind(context.Context, BindRequest, PreparedReceiptProof) (BindingReceipt, error)
	Abort(context.Context, AbortRequest, PreparedReceiptProof) (ClosedReceipt, error)
	Revoke(context.Context, RevokeRequest, BindingReceipt) (ClosedReceipt, error)
	Query(context.Context, QueryRequest) (QueryResult, error)
}

// NewEndpoint validates and finalizes a public endpoint identity.
// NewEndpoint 校验并固定公开 endpoint 身份摘要。
func NewEndpoint(rawURL, instanceID string) (Endpoint, error) {
	endpoint := Endpoint{SchemaVersion: SchemaVersion, URL: rawURL, InstanceID: instanceID}
	endpoint.Digest = ""
	if err := endpoint.validateShape(); err != nil {
		return Endpoint{}, err
	}
	digest, err := ComputeEndpointDigest(endpoint)
	if err != nil {
		return Endpoint{}, err
	}
	endpoint.Digest = digest
	return endpoint, nil
}

// Validate checks the endpoint identity and its domain-separated digest.
// Validate 复核 endpoint 形状和 domain-separated digest。
func (e Endpoint) Validate() error {
	if err := e.validateShape(); err != nil {
		return err
	}
	want, err := ComputeEndpointDigest(e)
	if err != nil || want != e.Digest {
		return fmt.Errorf("%w: endpoint digest mismatch", ErrInvalidEndpoint)
	}
	return nil
}

// validateShape 检查 endpoint 的版本、地址和实例标识形状。
func (e Endpoint) validateShape() error {
	if e.SchemaVersion != SchemaVersion || !validLoopbackURL(e.URL) || !validInstanceID(e.InstanceID) {
		return ErrInvalidEndpoint
	}
	if !validDigest(e.Digest) && e.Digest != "" {
		return fmt.Errorf("%w: endpoint digest is malformed", ErrInvalidEndpoint)
	}
	return nil
}

// ComputeEndpointDigest binds URL and instance identity; Digest is excluded from itself.
// ComputeEndpointDigest 绑定 URL 与实例身份，并排除自身摘要字段。
func ComputeEndpointDigest(endpoint Endpoint) (string, error) {
	endpoint.Digest = ""
	return digestCanonical(endpointDigestDomain, endpoint)
}

// FinalizeBindRequestDigest returns a request with a canonical self digest.
// FinalizeBindRequestDigest 计算并写回 bind request 的自摘要。
func FinalizeBindRequestDigest(request BindRequest) (BindRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputeBindRequestDigest(request)
	if err != nil {
		return BindRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputeBindRequestDigest binds all endpoint, process and credential references.
// ComputeBindRequestDigest 绑定 endpoint、进程和 credential 引用。
func ComputeBindRequestDigest(request BindRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(bindDigestDomain, request)
}

// FinalizeBindingReceiptDigest returns a receipt with a canonical self digest.
// FinalizeBindingReceiptDigest 计算并写回 binding receipt 自摘要。
func FinalizeBindingReceiptDigest(receipt BindingReceipt) (BindingReceipt, error) {
	receipt.ReceiptDigest = ""
	digest, err := ComputeBindingReceiptDigest(receipt)
	if err != nil {
		return BindingReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

// ComputeBindingReceiptDigest binds the endpoint and exact prepared process receipt.
// ComputeBindingReceiptDigest 绑定 endpoint 与精确 prepared process receipt。
func ComputeBindingReceiptDigest(receipt BindingReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return digestCanonical(bindingDigestDomain, receipt)
}

// ComputeProviderProfileDigest 绑定 provider 名称与 prepared receipt 的外部身份字段。
func ComputeProviderProfileDigest(provider string, prepared managedruntime.PreparedReceipt) (string, error) {
	if !validProvider(provider) {
		return "", ErrInvalidBinding
	}
	if err := prepared.Validate(); err != nil {
		return "", ErrInvalidBinding
	}
	return digestCanonical(providerProfileDigestDomain, struct {
		Provider           string `json:"provider"`
		ProviderThreadID   string `json:"provider_thread_id"`
		ProviderSessionID  string `json:"provider_session_id"`
		ExternalEpoch      string `json:"external_epoch"`
		RuntimeOwnerID     string `json:"runtime_owner_id"`
		RuntimeGeneration  int64  `json:"runtime_generation"`
		ProcessGeneration  int64  `json:"process_generation"`
		RuntimeWriterEpoch string `json:"runtime_writer_epoch"`
	}{Provider: provider, ProviderThreadID: prepared.ProviderThreadID, ProviderSessionID: prepared.ProviderSessionID, ExternalEpoch: prepared.ExternalEpoch, RuntimeOwnerID: prepared.RuntimeOwnerID, RuntimeGeneration: prepared.RuntimeGeneration, ProcessGeneration: prepared.ProcessGeneration, RuntimeWriterEpoch: prepared.RuntimeWriterEpoch})
}

// FinalizeAbortRequestDigest returns an abort request with a canonical self digest.
// FinalizeAbortRequestDigest 计算并写回 abort request 自摘要。
func FinalizeAbortRequestDigest(request AbortRequest) (AbortRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputeAbortRequestDigest(request)
	if err != nil {
		return AbortRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputeAbortRequestDigest binds prepared receipt and cleanup evidence references.
// ComputeAbortRequestDigest 绑定 prepared receipt 与 cleanup evidence 引用。
func ComputeAbortRequestDigest(request AbortRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(abortDigestDomain, request)
}

// FinalizeRevokeRequestDigest returns a revoke request with a canonical self digest.
// FinalizeRevokeRequestDigest 计算并写回 revoke request 自摘要。
func FinalizeRevokeRequestDigest(request RevokeRequest) (RevokeRequest, error) {
	request.RequestDigest = ""
	digest, err := ComputeRevokeRequestDigest(request)
	if err != nil {
		return RevokeRequest{}, err
	}
	request.RequestDigest = digest
	return request, nil
}

// ComputeRevokeRequestDigest binds the expected binding receipt and close evidence.
// ComputeRevokeRequestDigest 绑定 expected binding receipt 与关闭证据。
func ComputeRevokeRequestDigest(request RevokeRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(revokeDigestDomain, request)
}

// FinalizeClosedReceiptDigest returns a definitive closed receipt with a self digest.
// FinalizeClosedReceiptDigest 计算并写回确定性关闭 receipt 摘要。
func FinalizeClosedReceiptDigest(receipt ClosedReceipt) (ClosedReceipt, error) {
	receipt.ReceiptDigest = ""
	digest, err := ComputeClosedReceiptDigest(receipt)
	if err != nil {
		return ClosedReceipt{}, err
	}
	receipt.ReceiptDigest = digest
	return receipt, nil
}

// ComputeClosedReceiptDigest binds closure evidence and terminal state.
// ComputeClosedReceiptDigest 绑定关闭证据与终态。
func ComputeClosedReceiptDigest(receipt ClosedReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return digestCanonical(closedDigestDomain, receipt)
}

// FinalizeQueryResultDigest returns a query projection with a canonical self digest.
// FinalizeQueryResultDigest 计算并写回查询投影自摘要。
func FinalizeQueryResultDigest(result QueryResult) (QueryResult, error) {
	result.ResultDigest = ""
	digest, err := ComputeQueryResultDigest(result)
	if err != nil {
		return QueryResult{}, err
	}
	result.ResultDigest = digest
	return result, nil
}

// ComputeQueryResultDigest binds operation and exactly one lifecycle branch.
// ComputeQueryResultDigest 绑定操作标识与唯一生命周期分支。
func ComputeQueryResultDigest(result QueryResult) (string, error) {
	result.ResultDigest = ""
	return digestCanonical(queryDigestDomain, result)
}

// ValidateBind verifies managedruntime's process receipt before accepting endpoint pairing.
// ValidateBind 在接受 endpoint 配对前验证 managedruntime process receipt。
func ValidateBind(ctx context.Context, verifier ProcessReceiptVerifier, request BindRequest, prepare managedruntime.PrepareRequest, prepared managedruntime.PreparedReceipt, receipt BindingReceipt) (PreparedReceiptProof, error) {
	if err := request.Validate(); err != nil {
		return PreparedReceiptProof{}, err
	}
	proof, err := VerifyPreparedReceipt(ctx, verifier, prepare, prepared)
	if err != nil {
		return PreparedReceiptProof{}, err
	}
	if request.PreparedOperationID != prepared.OperationID || request.PreparedRequestDigest != prepared.RequestDigest {
		return PreparedReceiptProof{}, fmt.Errorf("%w: prepared operation mapping mismatch", ErrInvalidBinding)
	}
	profileDigest, err := ComputeProviderProfileDigest(request.Provider, prepared)
	if err != nil || request.ProviderProfileDigest != profileDigest {
		return PreparedReceiptProof{}, fmt.Errorf("%w: provider profile is not bound to prepared identity", ErrInvalidBinding)
	}
	if err := receipt.Validate(); err != nil {
		return PreparedReceiptProof{}, err
	}
	if err := receiptMatchesRequest(receipt, request, prepared); err != nil {
		return PreparedReceiptProof{}, err
	}
	return proof, nil
}

// VerifyPreparedReceipt 在任何 binding 投影前调用只读 verifier，并返回 verified marker。
func VerifyPreparedReceipt(ctx context.Context, verifier ProcessReceiptVerifier, prepare managedruntime.PrepareRequest, prepared managedruntime.PreparedReceipt) (PreparedReceiptProof, error) {
	if ctx == nil || ctx.Err() != nil || isNilVerifier(verifier) {
		return PreparedReceiptProof{}, ErrReceiptVerifierUnavailable
	}
	if err := prepare.Validate(); err != nil {
		return PreparedReceiptProof{}, ErrReceiptVerifierUnavailable
	}
	if err := prepared.Validate(); err != nil {
		return PreparedReceiptProof{}, ErrReceiptVerifierUnavailable
	}
	if err := managedruntime.MatchPrepare(prepare, prepared); err != nil {
		return PreparedReceiptProof{}, ErrReceiptVerifierUnavailable
	}
	if err := verifier.VerifyPrepared(ctx, prepare, prepared); err != nil {
		return PreparedReceiptProof{}, ErrReceiptVerificationFailed
	}
	return PreparedReceiptProof{request: prepare, receipt: prepared, verified: true}, nil
}

// Validate 检查 proof 的 verifier 标记和完整 managedruntime receipt 绑定。
func (p PreparedReceiptProof) Validate() error {
	if !p.verified {
		return ErrReceiptVerifierUnavailable
	}
	if err := managedruntime.MatchPrepare(p.request, p.receipt); err != nil {
		return ErrReceiptVerifierUnavailable
	}
	return nil
}

// PrepareRequest 返回已验证 proof 的 request 副本；未验证 proof 只能失败。
func (p PreparedReceiptProof) PrepareRequest() (managedruntime.PrepareRequest, error) {
	if err := p.Validate(); err != nil {
		return managedruntime.PrepareRequest{}, err
	}
	return p.request, nil
}

// PreparedReceipt 返回已验证 proof 的 receipt 副本；不会返回未验证的外部进程事实。
func (p PreparedReceiptProof) PreparedReceipt() (managedruntime.PreparedReceipt, error) {
	if err := p.Validate(); err != nil {
		return managedruntime.PreparedReceipt{}, err
	}
	return p.receipt, nil
}

// ValidateAbort verifies a pre-bind cleanup receipt against the original prepared process.
// ValidateAbort 将 pre-bind cleanup receipt 与原 prepared process 精确绑定。
func ValidateAbort(request AbortRequest, proof PreparedReceiptProof, closed ClosedReceipt) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := proof.Validate(); err != nil {
		return fmt.Errorf("%w: prepared proof: %v", ErrInvalidClosure, err)
	}
	prepared := proof.receipt
	if request.PreparedOperationID != prepared.OperationID || request.PreparedRequestDigest != prepared.RequestDigest ||
		request.PreparedReceiptRef != prepared.ReceiptRef || request.PreparedReceiptDigest != prepared.ReceiptDigest {
		return fmt.Errorf("%w: abort prepared receipt mapping mismatch", ErrInvalidClosure)
	}
	return validateClosedReceipt(closed, request.OperationID, "", &prepared, StateAborted, request.RequestDigest,
		request.PreparedReceiptRef, request.PreparedReceiptDigest, request.CleanupEvidenceRef, request.CleanupEvidenceDigest)
}

// ValidateRevoke verifies a post-bind close receipt against the exact binding receipt.
// ValidateRevoke 将 post-bind 关闭 receipt 与原 binding receipt 精确绑定。
func ValidateRevoke(request RevokeRequest, binding BindingReceipt, closed ClosedReceipt) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := binding.Validate(); err != nil {
		return fmt.Errorf("%w: binding receipt: %v", ErrInvalidClosure, err)
	}
	if request.BindingOperationID != binding.OperationID || request.ExpectedReceiptRef != binding.ReceiptRef ||
		request.ExpectedReceiptDigest != binding.ReceiptDigest {
		return fmt.Errorf("%w: revoke binding receipt mapping mismatch", ErrInvalidClosure)
	}
	if closed.PreparedOperationID != binding.PreparedOperationID || closed.PreparedRequestDigest != binding.PreparedRequestDigest {
		return fmt.Errorf("%w: revoke prepared mapping mismatch", ErrInvalidClosure)
	}
	return validateClosedReceipt(closed, request.OperationID, binding.OperationID, nil, StateRevoked, request.RequestDigest,
		request.ExpectedReceiptRef, request.ExpectedReceiptDigest, request.ClosureEvidenceRef, request.ClosureEvidenceDigest)
}

// ValidateQuery checks operation identity and one-hot branch presence.
// ValidateQuery 校验 operation 身份和 one-hot 查询分支。
func ValidateQuery(request QueryRequest, result QueryResult) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.OperationID != request.OperationID || result.OperationDigest != request.OperationDigest {
		return ErrOperationConflict
	}
	return nil
}

// Validate checks a bind request's fixed schema and endpoint/process references.
// Validate 校验 bind request 的固定 schema 与 endpoint/进程引用。
func (r BindRequest) Validate() error {
	if err := validateBindShape(r); err != nil {
		return err
	}
	if err := r.Endpoint.Validate(); err != nil {
		return fmt.Errorf("%w: endpoint: %v", ErrInvalidBindRequest, err)
	}
	if err := validateSelfDigest(ErrInvalidBindRequest, bindDigestDomain, r.RequestDigest, r); err != nil {
		return fmt.Errorf("%w: request digest mismatch", ErrInvalidBindRequest)
	}
	return nil
}

// validateBindShape 校验 bind request 的字段形状和 provider 枚举。
func validateBindShape(r BindRequest) error {
	if !validBindIdentity(r) || !validBindCredential(r) || !validBindProvider(r) || !validBindFence(r) || !validDigest(r.RequestDigest) {
		return ErrInvalidBindRequest
	}
	return nil
}

// validBindIdentity 校验 bind request 的版本和 operation 身份字段。
func validBindIdentity(r BindRequest) bool {
	return r.SchemaVersion == SchemaVersion && validText(r.OperationID, 160) && validText(r.PreparedOperationID, 160) &&
		validDigest(r.PreparedRequestDigest)
}

// validBindCredential 校验 credential 仅以 lease 和不可逆摘要出现。
func validBindCredential(r BindRequest) bool {
	return validText(r.CredentialLeaseID, 256) && validDigest(r.CredentialDigest)
}

// validBindProvider 校验 provider 与已声明 profile 的引用摘要。
func validBindProvider(r BindRequest) bool {
	return validProvider(r.Provider) && validText(r.ProviderProfileRef, 256) && validDigest(r.ProviderProfileDigest)
}

// validBindFence 校验 parent incarnation、fence 和 connection nonce 摘要。
func validBindFence(r BindRequest) bool {
	return validText(r.ParentExecutionIncarnationID, 256) && validDigest(r.ParentFenceDigest) && validDigest(r.ConnectionNonceDigest)
}

// Validate checks a bound receipt's shape and self digest, not external process truth.
// Validate 校验 binding receipt 形状和自摘要，不冒充外部进程真值。
func (r BindingReceipt) Validate() error {
	if err := validateBindingShape(r); err != nil {
		return ErrInvalidBinding
	}
	if err := validateSelfDigest(ErrInvalidBinding, bindingDigestDomain, r.ReceiptDigest, r); err != nil {
		return fmt.Errorf("%w: receipt digest mismatch", ErrInvalidBinding)
	}
	return nil
}

// validateBindingShape 校验 binding receipt 的稳定引用、代际和终态字段。
func validateBindingShape(r BindingReceipt) error {
	if !validBindingIdentity(r) || !validBindingCredentials(r) || !validBindingProvider(r) || !validBindingProcess(r) ||
		!validBindingReceiptRefs(r) {
		return ErrInvalidBinding
	}
	return nil
}

// validBindingIdentity 校验 binding receipt 的操作与 prepared 摘要。
func validBindingIdentity(r BindingReceipt) bool {
	return r.SchemaVersion == SchemaVersion && validText(r.OperationID, 160) && validDigest(r.BindingRequestDigest) &&
		validText(r.PreparedOperationID, 160) && validDigest(r.PreparedRequestDigest)
}

// validBindingCredentials 校验 binding receipt 的 credential lease 和摘要。
func validBindingCredentials(r BindingReceipt) bool {
	return validText(r.CredentialLeaseID, 256) && validDigest(r.CredentialDigest)
}

// validBindingProvider 校验 provider profile 与 parent/connection fence 摘要。
func validBindingProvider(r BindingReceipt) bool {
	return validProvider(r.Provider) && validText(r.ProviderProfileRef, 256) && validDigest(r.ProviderProfileDigest) &&
		validText(r.ParentExecutionIncarnationID, 256) && validDigest(r.ParentFenceDigest) && validDigest(r.ConnectionNonceDigest)
}

// validBindingProcess 校验 provider session、runtime owner 和代际。
func validBindingProcess(r BindingReceipt) bool {
	return validDigest(r.EndpointDigest) && validText(r.EndpointInstanceID, 256) && validOptionalText(r.ProviderThreadID, 256) &&
		validOptionalText(r.ProviderSessionID, 256) && (r.ProviderThreadID != "" || r.ProviderSessionID != "") &&
		validText(r.RuntimeOwnerID, 256) && r.RuntimeGeneration > 0 && r.ProcessGeneration > 0 && validText(r.RuntimeWriterEpoch, 256)
}

// validBindingReceiptRefs 校验 prepared receipt、终态和 receipt 自身引用。
func validBindingReceiptRefs(r BindingReceipt) bool {
	return validText(r.PreparedReceiptRef, 512) && validDigest(r.PreparedReceiptDigest) && r.State == StateBound &&
		validText(r.ReceiptRef, 512) && validDigest(r.ReceiptDigest)
}

// Validate checks an abort request's prepared receipt and cleanup evidence references.
// Validate 校验 abort request 的 prepared receipt 与 cleanup 证据引用。
func (r AbortRequest) Validate() error {
	if err := validateAbortShape(r); err != nil {
		return ErrInvalidClosure
	}
	if err := validateSelfDigest(ErrInvalidClosure, abortDigestDomain, r.RequestDigest, r); err != nil {
		return fmt.Errorf("%w: abort request digest mismatch", ErrInvalidClosure)
	}
	return nil
}

// validateAbortShape 校验 abort request 的 prepared receipt 与 cleanup 引用。
func validateAbortShape(r AbortRequest) error {
	if r.SchemaVersion != SchemaVersion || !validText(r.OperationID, 160) || !validText(r.PreparedOperationID, 160) ||
		!validDigest(r.PreparedRequestDigest) || !validText(r.PreparedReceiptRef, 512) || !validDigest(r.PreparedReceiptDigest) ||
		!validText(r.CleanupEvidenceRef, 512) || !validDigest(r.CleanupEvidenceDigest) || !validDigest(r.RequestDigest) {
		return ErrInvalidClosure
	}
	return nil
}

// Validate checks a revoke request's expected binding receipt and closure evidence.
// Validate 校验 revoke request 的 expected binding receipt 与关闭证据。
func (r RevokeRequest) Validate() error {
	if err := validateRevokeShape(r); err != nil {
		return ErrInvalidClosure
	}
	if err := validateSelfDigest(ErrInvalidClosure, revokeDigestDomain, r.RequestDigest, r); err != nil {
		return fmt.Errorf("%w: revoke request digest mismatch", ErrInvalidClosure)
	}
	return nil
}

// validateRevokeShape 校验 revoke request 的 binding receipt 与关闭证据。
func validateRevokeShape(r RevokeRequest) error {
	if r.SchemaVersion != SchemaVersion || !validText(r.OperationID, 160) || !validText(r.BindingOperationID, 160) ||
		!validText(r.ExpectedReceiptRef, 512) || !validDigest(r.ExpectedReceiptDigest) || !validText(r.ClosureEvidenceRef, 512) ||
		!validDigest(r.ClosureEvidenceDigest) || !validDigest(r.RequestDigest) {
		return ErrInvalidClosure
	}
	return nil
}

// Validate checks a definitive closed receipt and excludes outcome_unknown.
// Validate 校验确定性 closed receipt，并排除 outcome_unknown。
func (r ClosedReceipt) Validate() error {
	if err := validateClosedShape(r); err != nil {
		return ErrInvalidClosure
	}
	if r.State == StateAborted {
		if r.BindingOperationID != "" {
			return ErrInvalidClosure
		}
	} else if !validText(r.BindingOperationID, 160) {
		return ErrInvalidClosure
	}
	if err := validateSelfDigest(ErrInvalidClosure, closedDigestDomain, r.ReceiptDigest, r); err != nil {
		return fmt.Errorf("%w: closed receipt digest mismatch", ErrInvalidClosure)
	}
	return nil
}

// validateClosedShape 校验 closed receipt 的终态、引用和关闭证据。
func validateClosedShape(r ClosedReceipt) error {
	if !validClosedIdentity(r) || !validClosedEvidence(r) || !validDigest(r.ReceiptDigest) {
		return ErrInvalidClosure
	}
	return nil
}

// validClosedIdentity 校验 closed receipt 的操作、prepared 引用和终态。
func validClosedIdentity(r ClosedReceipt) bool {
	return r.SchemaVersion == SchemaVersion && validText(r.OperationID, 160) && validText(r.PreparedOperationID, 160) &&
		validDigest(r.PreparedRequestDigest) && validText(r.ExpectedReceiptRef, 512) && validDigest(r.ExpectedReceiptDigest) &&
		(r.State == StateAborted || r.State == StateRevoked) && validDigest(r.OperationDigest)
}

// validClosedEvidence 校验 closed receipt 的关闭证据和输出引用。
func validClosedEvidence(r ClosedReceipt) bool {
	return validText(r.ClosureEvidenceRef, 512) && validDigest(r.ClosureEvidenceDigest) && validText(r.ReceiptRef, 512)
}

// Validate checks query input and one-hot state result.
// Validate 校验 query 输入与 one-hot 状态结果。
func (r QueryRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion || !validText(r.OperationID, 160) || !validDigest(r.OperationDigest) {
		return ErrInvalidQuery
	}
	return nil
}

// Validate checks query branch presence and self digest; unknown remains query-only.
// Validate 校验查询分支存在性和自摘要，unknown 只允许出现在 query。
func (r QueryResult) Validate() error {
	if err := validateQueryShape(r); err != nil {
		return ErrInvalidQuery
	}
	if err := validateQueryPresence(r); err != nil {
		return err
	}
	if err := validateQueryRefs(r); err != nil {
		return err
	}
	if err := validateSelfDigest(ErrInvalidQuery, queryDigestDomain, r.ResultDigest, r); err != nil {
		return fmt.Errorf("%w: query result digest mismatch", ErrInvalidQuery)
	}
	return nil
}

// validateQueryShape 校验 query result 的基础字段和 receipt 成对关系。
func validateQueryShape(r QueryResult) error {
	if r.SchemaVersion != SchemaVersion || !validText(r.OperationID, 160) || !validDigest(r.OperationDigest) || !validState(r.State) ||
		!validDigest(r.ResultDigest) || hasPartial(r.PreparedReceiptRef, r.PreparedReceiptDigest) || hasPartial(r.BindingReceiptRef, r.BindingReceiptDigest) ||
		hasPartial(r.ClosedReceiptRef, r.ClosedReceiptDigest) || hasPartial(r.UncertaintyReceiptRef, r.UncertaintyDigest) {
		return ErrInvalidQuery
	}
	return nil
}

// Transition enforces the candidate-only lifecycle matrix; unknown has no mutation edge.
// Transition 执行 candidate-only 生命周期矩阵，unknown 不存在 mutation 边。
func Transition(from State, trigger Trigger) (State, error) {
	switch {
	case from == StateCandidate && trigger == TriggerPrepare:
		return StatePrepared, nil
	case from == StatePrepared && trigger == TriggerBind:
		return StateBound, nil
	case from == StatePrepared && trigger == TriggerAbort:
		return StateAborted, nil
	case from == StateBound && trigger == TriggerRevoke:
		return StateRevoked, nil
	default:
		return "", fmt.Errorf("%w: %s + %s", ErrInvalidTransition, from, trigger)
	}
}
