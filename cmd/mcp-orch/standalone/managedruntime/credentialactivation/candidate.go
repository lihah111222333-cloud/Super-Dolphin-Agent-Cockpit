// Package credentialactivation defines a candidate-only seam for activating a
// prepared managed-runtime credential. It never creates or exposes credential
// material and has no production wiring.
package credentialactivation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const SchemaVersion int64 = 1

const (
	StatePrepared  State = "prepared"
	StateActive    State = "active"
	StateCandidate State = "candidate"

	receiptDigestDomain     = "orch.managed-runtime.credential-active-receipt/v1"
	requestDigestDomain     = "orch.managed-runtime.credential-activation-request/v1"
	queryDigestDomain       = "orch.managed-runtime.credential-security-time-query/v1"
	observationDigestDomain = "orch.managed-runtime.credential-security-time-observation/v1"
	nonceDigestDomain       = "orch.managed-runtime.credential-anti-replay/v1"
	maxText                 = 512
)

var (
	ErrActivationUnavailable = errors.New("credential activation owner is unavailable")
	ErrInvalidActivation     = errors.New("credential activation request is invalid")
	ErrInvalidActiveReceipt  = errors.New("active credential receipt is invalid")
	ErrInvalidBinding        = errors.New("current credential binding is invalid")
	ErrInvalidSecurityTime   = errors.New("security-time observation is invalid")
	ErrInvalidAntiReplay     = errors.New("anti-replay receipt is invalid")
	ErrActivationRejected    = errors.New("credential activation was rejected")
)

// State 表示凭据激活引用在候选流程中的受限生命周期状态。
type State string

// PreparedCredential 表示已准备但尚未授权的凭据外部引用与绑定 fence。
type PreparedCredential struct {
	SchemaVersion           int64  `json:"schema_version"`
	State                   State  `json:"state"`
	CredentialLeaseID       string `json:"credential_lease_id"`
	CredentialDigest        string `json:"credential_digest"`
	AgentID                 string `json:"agent_id"`
	IncarnationID           string `json:"incarnation_id"`
	PreparationRef          string `json:"preparation_ref"`
	PreparationDigest       string `json:"preparation_digest"`
	BindingVersion          int64  `json:"binding_version"`
	BindingDigest           string `json:"binding_digest"`
	ActiveProcessReceiptRef string `json:"active_process_receipt_ref"`
	ConnectionNonceDigest   string `json:"connection_nonce_digest"`
}

// ActiveCredentialReceipt 表示由 active receipt owner 签发的凭据激活证明。
type ActiveCredentialReceipt struct {
	SchemaVersion             int64  `json:"schema_version"`
	State                     State  `json:"state"`
	ReceiptRef                string `json:"receipt_ref"`
	ReceiptDigest             string `json:"receipt_digest"`
	CredentialLeaseID         string `json:"credential_lease_id"`
	CredentialDigest          string `json:"credential_digest"`
	PreparationRef            string `json:"preparation_ref"`
	PreparationDigest         string `json:"preparation_digest"`
	AgentID                   string `json:"agent_id"`
	IncarnationID             string `json:"incarnation_id"`
	BindingVersion            int64  `json:"binding_version"`
	BindingDigest             string `json:"binding_digest"`
	ActiveProcessReceiptRef   string `json:"active_process_receipt_ref"`
	ConnectionNonceDigest     string `json:"connection_nonce_digest"`
	NonceSlot                 string `json:"nonce_slot"`
	IssuedAt                  string `json:"issued_at"`
	ExpiresAt                 string `json:"expires_at"`
	SecurityTimeLineageDigest string `json:"security_time_lineage_digest"`
	SecurityTimeGeneration    int64  `json:"security_time_generation"`
	SecurityTimeRevision      int64  `json:"security_time_revision"`
}

// CurrentBinding 表示当前 agent incarnation 的 active credential 绑定快照。
type CurrentBinding struct {
	SchemaVersion           int64  `json:"schema_version"`
	State                   State  `json:"state"`
	AgentID                 string `json:"agent_id"`
	IncarnationID           string `json:"incarnation_id"`
	BindingVersion          int64  `json:"binding_version"`
	BindingDigest           string `json:"binding_digest"`
	ActiveProcessReceiptRef string `json:"active_process_receipt_ref"`
}

// SecurityTimeQuery 表示校验安全时钟代际与修订号所需的查询绑定。
type SecurityTimeQuery struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	RequestDigest         string `json:"request_digest"`
	ExpectedLineageDigest string `json:"expected_lineage_digest"`
	ExpectedGeneration    int64  `json:"expected_generation"`
	ExpectedRevision      int64  `json:"expected_revision"`
}

// SecurityTimeObservation 表示 security-time owner 返回的带 receipt 时间观察。
type SecurityTimeObservation struct {
	SchemaVersion int64  `json:"schema_version"`
	LineageDigest string `json:"lineage_digest"`
	Generation    int64  `json:"generation"`
	Revision      int64  `json:"revision"`
	ObservedAt    string `json:"observed_at"`
	ReceiptRef    string `json:"receipt_ref"`
	ReceiptDigest string `json:"receipt_digest"`
}

// AntiReplayRequest 表示在线性化点消费一次性连接 nonce 的请求。
type AntiReplayRequest struct {
	SchemaVersion         int64  `json:"schema_version"`
	OperationID           string `json:"operation_id"`
	RequestDigest         string `json:"request_digest"`
	CredentialLeaseID     string `json:"credential_lease_id"`
	CredentialDigest      string `json:"credential_digest"`
	ConnectionNonceDigest string `json:"connection_nonce_digest"`
	NonceSlot             string `json:"nonce_slot"`
}

// AntiReplayReceipt 表示 anti-replay owner 已消费 nonce 的不可变收据。
type AntiReplayReceipt struct {
	SchemaVersion         int64  `json:"schema_version"`
	ReceiptRef            string `json:"receipt_ref"`
	ReceiptDigest         string `json:"receipt_digest"`
	OperationID           string `json:"operation_id"`
	RequestDigest         string `json:"request_digest"`
	CredentialLeaseID     string `json:"credential_lease_id"`
	CredentialDigest      string `json:"credential_digest"`
	ConnectionNonceDigest string `json:"connection_nonce_digest"`
	NonceSlot             string `json:"nonce_slot"`
	ConsumedAt            string `json:"consumed_at"`
}

// ActivationRequest 汇总候选激活所需的 prepared 引用与外部 owner 证明。
type ActivationRequest struct {
	SchemaVersion              int64                   `json:"schema_version"`
	OperationID                string                  `json:"operation_id"`
	Prepared                   PreparedCredential      `json:"prepared"`
	ActiveReceipt              ActiveCredentialReceipt `json:"active_receipt"`
	ExpectedBindingDigest      string                  `json:"expected_binding_digest"`
	ExpectedBindingVersion     int64                   `json:"expected_binding_version"`
	ExpectedLineageDigest      string                  `json:"expected_lineage_digest"`
	ExpectedSecurityGeneration int64                   `json:"expected_security_generation"`
	ExpectedSecurityRevision   int64                   `json:"expected_security_revision"`
	NonceSlot                  string                  `json:"nonce_slot"`
	RequestDigest              string                  `json:"request_digest"`
}

// Candidate 表示仅含引用和证明摘要、尚未接入生产图的激活候选。
type Candidate struct {
	SchemaVersion           int64  `json:"schema_version"`
	State                   State  `json:"state"`
	OperationID             string `json:"operation_id"`
	RequestDigest           string `json:"request_digest"`
	CredentialLeaseID       string `json:"credential_lease_id"`
	CredentialDigest        string `json:"credential_digest"`
	AgentID                 string `json:"agent_id"`
	IncarnationID           string `json:"incarnation_id"`
	BindingDigest           string `json:"binding_digest"`
	ActiveReceiptRef        string `json:"active_receipt_ref"`
	ActiveReceiptDigest     string `json:"active_receipt_digest"`
	SecurityLineageDigest   string `json:"security_lineage_digest"`
	SecurityGeneration      int64  `json:"security_generation"`
	SecurityRevision        int64  `json:"security_revision"`
	AntiReplayReceiptRef    string `json:"anti_replay_receipt_ref"`
	AntiReplayReceiptDigest string `json:"anti_replay_receipt_digest"`
}

// ActiveReceiptOwner 负责验证外部 active credential receipt 的 owner。
type ActiveReceiptOwner interface {
	VerifyActiveReceipt(context.Context, ActivationRequest, ActiveCredentialReceipt) error
}

// CurrentBindingReader 负责读取指定 agent incarnation 的当前绑定。
type CurrentBindingReader interface {
	ReadCurrentBinding(context.Context, string, string) (CurrentBinding, error)
}

// SecurityTimeOwner 负责观察并返回绑定代际的安全时间快照。
type SecurityTimeOwner interface {
	ObserveSecurityTime(context.Context, SecurityTimeQuery) (SecurityTimeObservation, error)
}

// AntiReplayOwner 负责在线性化点消费连接 nonce 并签发收据。
type AntiReplayOwner interface {
	ConsumeNonce(context.Context, AntiReplayRequest) (AntiReplayReceipt, error)
}

// CandidateActivator 协调外部 owner 校验并生成候选激活结果。
type CandidateActivator struct {
	receipts ActiveReceiptOwner
	binding  CurrentBindingReader
	clock    SecurityTimeOwner
	replay   AntiReplayOwner
}

// NewCandidateActivator 构造未接入生产图的外部 owner 候选。
func NewCandidateActivator(receipts ActiveReceiptOwner, binding CurrentBindingReader, clock SecurityTimeOwner, replay AntiReplayOwner) (*CandidateActivator, error) {
	if interfaceIsNil(receipts) || interfaceIsNil(binding) || interfaceIsNil(clock) || interfaceIsNil(replay) {
		return nil, ErrActivationUnavailable
	}
	return &CandidateActivator{receipts: receipts, binding: binding, clock: clock, replay: replay}, nil
}

// Activate 只验证外部 active receipt、current binding、安全时钟与防重放收据。
func (a *CandidateActivator) Activate(ctx context.Context, request ActivationRequest) (Candidate, error) {
	if err := a.ready(ctx); err != nil {
		return Candidate{}, err
	}
	if err := request.Validate(); err != nil {
		return Candidate{}, err
	}
	if err := a.receipts.VerifyActiveReceipt(ctx, request, request.ActiveReceipt); err != nil {
		return Candidate{}, errors.Join(ErrActivationRejected, err)
	}
	current, err := a.readBinding(ctx, request)
	if err != nil {
		return Candidate{}, err
	}
	observation, err := a.readSecurityTime(ctx, request)
	if err != nil {
		return Candidate{}, err
	}
	replayReceipt, err := a.consumeReplay(ctx, request)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{SchemaVersion: SchemaVersion, State: StateCandidate, OperationID: request.OperationID, RequestDigest: request.RequestDigest, CredentialLeaseID: request.Prepared.CredentialLeaseID, CredentialDigest: request.Prepared.CredentialDigest, AgentID: request.Prepared.AgentID, IncarnationID: request.Prepared.IncarnationID, BindingDigest: current.BindingDigest, ActiveReceiptRef: request.ActiveReceipt.ReceiptRef, ActiveReceiptDigest: request.ActiveReceipt.ReceiptDigest, SecurityLineageDigest: observation.LineageDigest, SecurityGeneration: observation.Generation, SecurityRevision: observation.Revision, AntiReplayReceiptRef: replayReceipt.ReceiptRef, AntiReplayReceiptDigest: replayReceipt.ReceiptDigest}, nil
}

// ready 验证四个外部 owner 与调用 context 均可用。
func (a *CandidateActivator) ready(ctx context.Context) error {
	if a == nil || interfaceIsNil(a.receipts) || interfaceIsNil(a.binding) || interfaceIsNil(a.clock) || interfaceIsNil(a.replay) || interfaceIsNil(ctx) || ctx.Err() != nil {
		return ErrActivationUnavailable
	}
	return nil
}

// readBinding 从独立 current-binding owner 读取并逐字段比较 active fence。
func (a *CandidateActivator) readBinding(ctx context.Context, request ActivationRequest) (CurrentBinding, error) {
	current, err := a.binding.ReadCurrentBinding(ctx, request.Prepared.AgentID, request.Prepared.IncarnationID)
	if err != nil {
		return CurrentBinding{}, errors.Join(ErrActivationRejected, err)
	}
	if err := matchCurrentBinding(request, current); err != nil {
		return CurrentBinding{}, err
	}
	return current, nil
}

// readSecurityTime 从独立 security-time owner 读取同一代际的时间观察。
func (a *CandidateActivator) readSecurityTime(ctx context.Context, request ActivationRequest) (SecurityTimeObservation, error) {
	query := SecurityTimeQuery{SchemaVersion: SchemaVersion, OperationID: request.OperationID, RequestDigest: request.RequestDigest, ExpectedLineageDigest: request.ExpectedLineageDigest, ExpectedGeneration: request.ExpectedSecurityGeneration, ExpectedRevision: request.ExpectedSecurityRevision}
	if err := query.Validate(); err != nil {
		return SecurityTimeObservation{}, err
	}
	observation, err := a.clock.ObserveSecurityTime(ctx, query)
	if err != nil {
		return SecurityTimeObservation{}, errors.Join(ErrActivationRejected, err)
	}
	if err := matchSecurityTime(request, observation); err != nil {
		return SecurityTimeObservation{}, err
	}
	return observation, nil
}

// consumeReplay 让独立 anti-replay owner 在线性化点消费 nonce。
func (a *CandidateActivator) consumeReplay(ctx context.Context, request ActivationRequest) (AntiReplayReceipt, error) {
	replayRequest := AntiReplayRequest{SchemaVersion: SchemaVersion, OperationID: request.OperationID, RequestDigest: request.RequestDigest, CredentialLeaseID: request.Prepared.CredentialLeaseID, CredentialDigest: request.Prepared.CredentialDigest, ConnectionNonceDigest: request.Prepared.ConnectionNonceDigest, NonceSlot: request.NonceSlot}
	if err := replayRequest.Validate(); err != nil {
		return AntiReplayReceipt{}, err
	}
	replayReceipt, err := a.replay.ConsumeNonce(ctx, replayRequest)
	if err != nil {
		return AntiReplayReceipt{}, errors.Join(ErrActivationRejected, err)
	}
	if err := matchAntiReplay(replayRequest, replayReceipt); err != nil {
		return AntiReplayReceipt{}, err
	}
	return replayReceipt, nil
}

// Validate 验证 prepared credential 仅是未授权的外部引用。
func (r PreparedCredential) Validate() error {
	if !preparedHeaderValid(r) || !preparedCredentialValid(r) || !preparedIdentityValid(r) || !preparedFenceValid(r) {
		return ErrInvalidActivation
	}
	return nil
}

func preparedHeaderValid(r PreparedCredential) bool {
	return r.SchemaVersion == SchemaVersion && r.State == StatePrepared
}
func preparedCredentialValid(r PreparedCredential) bool {
	return validText(r.CredentialLeaseID) && validDigest(r.CredentialDigest)
}
func preparedIdentityValid(r PreparedCredential) bool {
	return validText(r.AgentID) && validText(r.IncarnationID) && validText(r.PreparationRef) && validDigest(r.PreparationDigest)
}
func preparedFenceValid(r PreparedCredential) bool {
	return r.BindingVersion > 0 && validDigest(r.BindingDigest) && validText(r.ActiveProcessReceiptRef) && validDigest(r.ConnectionNonceDigest)
}

// Validate 验证外部 owner 签发的 active credential receipt。
func (r ActiveCredentialReceipt) Validate() error {
	if !activeReceiptHeaderValid(r) || !activeReceiptIdentityValid(r) || !activeReceiptFenceValid(r) || !activeReceiptSecurityValid(r) {
		return ErrInvalidActiveReceipt
	}
	issued, expires, err := parseWindow(r.IssuedAt, r.ExpiresAt)
	if err != nil || !expires.After(issued) {
		return ErrInvalidActiveReceipt
	}
	want, err := ComputeActiveReceiptDigest(r)
	if err != nil || want != r.ReceiptDigest {
		return ErrInvalidActiveReceipt
	}
	return nil
}

func activeReceiptHeaderValid(r ActiveCredentialReceipt) bool {
	return r.SchemaVersion == SchemaVersion && r.State == StateActive && validText(r.ReceiptRef) && validDigest(r.ReceiptDigest)
}

// activeReceiptIdentityValid 检查 active receipt 的 credential 和 identity 引用。
func activeReceiptIdentityValid(r ActiveCredentialReceipt) bool {
	return validText(r.CredentialLeaseID) && validDigest(r.CredentialDigest) && validText(r.PreparationRef) && validDigest(r.PreparationDigest) && validText(r.AgentID) && validText(r.IncarnationID)
}
func activeReceiptFenceValid(r ActiveCredentialReceipt) bool {
	return r.BindingVersion > 0 && validDigest(r.BindingDigest) && validText(r.ActiveProcessReceiptRef) && validDigest(r.ConnectionNonceDigest)
}
func activeReceiptSecurityValid(r ActiveCredentialReceipt) bool {
	return validText(r.NonceSlot) && validDigest(r.SecurityTimeLineageDigest) && r.SecurityTimeGeneration > 0 && r.SecurityTimeRevision > 0
}

// Validate 验证 current binding 的 active 状态和完整 fence。
func (b CurrentBinding) Validate() error {
	if b.SchemaVersion != SchemaVersion || b.State != StateActive {
		return ErrInvalidBinding
	}
	if !validText(b.AgentID) || !validText(b.IncarnationID) || b.BindingVersion <= 0 {
		return ErrInvalidBinding
	}
	if !validDigest(b.BindingDigest) || !validText(b.ActiveProcessReceiptRef) {
		return ErrInvalidBinding
	}
	return nil
}

// Validate 验证安全时钟查询绑定的 operation 和当前 tuple。
func (q SecurityTimeQuery) Validate() error {
	if q.SchemaVersion != SchemaVersion || !validText(q.OperationID) || !validDigest(q.RequestDigest) || !validDigest(q.ExpectedLineageDigest) || q.ExpectedGeneration <= 0 || q.ExpectedRevision <= 0 {
		return ErrInvalidSecurityTime
	}
	return nil
}

// Validate 验证安全时钟 owner 返回的带 receipt 观察值。
func (o SecurityTimeObservation) Validate() error {
	if !securityObservationHeaderValid(o) || !securityObservationPayloadValid(o) {
		return ErrInvalidSecurityTime
	}
	if _, err := parseTime(o.ObservedAt); err != nil {
		return ErrInvalidSecurityTime
	}
	want, err := ComputeSecurityTimeObservationDigest(o)
	if err != nil || want != o.ReceiptDigest {
		return ErrInvalidSecurityTime
	}
	return nil
}

func securityObservationHeaderValid(o SecurityTimeObservation) bool {
	return o.SchemaVersion == SchemaVersion && validDigest(o.LineageDigest) && o.Generation > 0 && o.Revision > 0
}
func securityObservationPayloadValid(o SecurityTimeObservation) bool {
	return validText(o.ObservedAt) && validText(o.ReceiptRef) && validDigest(o.ReceiptDigest)
}

// Validate 验证 anti-replay owner 返回的一次性消费 receipt。
func (r AntiReplayReceipt) Validate() error {
	if !antiReplayHeaderValid(r) || !antiReplayPayloadValid(r) {
		return ErrInvalidAntiReplay
	}
	if _, err := parseTime(r.ConsumedAt); err != nil {
		return ErrInvalidAntiReplay
	}
	want, err := ComputeAntiReplayReceiptDigest(r)
	if err != nil || want != r.ReceiptDigest {
		return ErrInvalidAntiReplay
	}
	return nil
}

func antiReplayHeaderValid(r AntiReplayReceipt) bool {
	return r.SchemaVersion == SchemaVersion && validText(r.ReceiptRef) && validDigest(r.ReceiptDigest) && validText(r.OperationID) && validDigest(r.RequestDigest)
}
func antiReplayPayloadValid(r AntiReplayReceipt) bool {
	return validText(r.CredentialLeaseID) && validDigest(r.CredentialDigest) && validDigest(r.ConnectionNonceDigest) && validText(r.NonceSlot) && validText(r.ConsumedAt)
}

// Validate 验证 anti-replay 请求的完整 nonce tuple。
func (r AntiReplayRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion || !validText(r.OperationID) || !validDigest(r.RequestDigest) || !validText(r.CredentialLeaseID) || !validDigest(r.CredentialDigest) || !validDigest(r.ConnectionNonceDigest) || !validText(r.NonceSlot) {
		return ErrInvalidAntiReplay
	}
	return nil
}

// Validate 验证 activation 请求只引用 prepared 和外部 owner receipt。
func (r ActivationRequest) Validate() error {
	if !activationHeaderValid(r) || !activationFenceValid(r) || !activationSecurityValid(r) || !validText(r.NonceSlot) {
		return ErrInvalidActivation
	}
	if err := r.Prepared.Validate(); err != nil {
		return err
	}
	if err := r.ActiveReceipt.Validate(); err != nil {
		return err
	}
	if !activationTupleMatches(r) {
		return ErrInvalidActivation
	}
	want, err := ComputeActivationRequestDigest(r)
	if err != nil || want != r.RequestDigest {
		return ErrInvalidActivation
	}
	return nil
}

func activationHeaderValid(r ActivationRequest) bool {
	return r.SchemaVersion == SchemaVersion && validText(r.OperationID)
}
func activationFenceValid(r ActivationRequest) bool {
	return validDigest(r.ExpectedBindingDigest) && r.ExpectedBindingVersion > 0
}
func activationSecurityValid(r ActivationRequest) bool {
	return validDigest(r.ExpectedLineageDigest) && r.ExpectedSecurityGeneration > 0 && r.ExpectedSecurityRevision > 0
}
func activationTupleMatches(r ActivationRequest) bool {
	return activationIdentityMatches(r) && activationFenceMatches(r) && activationSecurityMatches(r)
}

// activationIdentityMatches 比较 prepared 与 active receipt 的身份引用。
func activationIdentityMatches(r ActivationRequest) bool {
	return r.ActiveReceipt.CredentialLeaseID == r.Prepared.CredentialLeaseID && r.ActiveReceipt.CredentialDigest == r.Prepared.CredentialDigest && r.ActiveReceipt.PreparationRef == r.Prepared.PreparationRef && r.ActiveReceipt.PreparationDigest == r.Prepared.PreparationDigest && r.ActiveReceipt.AgentID == r.Prepared.AgentID && r.ActiveReceipt.IncarnationID == r.Prepared.IncarnationID
}

// activationFenceMatches 比较 binding version、digest、process 和 nonce fence。
func activationFenceMatches(r ActivationRequest) bool {
	return r.ActiveReceipt.BindingVersion == r.Prepared.BindingVersion && r.ActiveReceipt.BindingVersion == r.ExpectedBindingVersion && r.ActiveReceipt.BindingDigest == r.ExpectedBindingDigest && r.ActiveReceipt.ActiveProcessReceiptRef == r.Prepared.ActiveProcessReceiptRef && r.ActiveReceipt.ConnectionNonceDigest == r.Prepared.ConnectionNonceDigest
}

// activationSecurityMatches 比较 nonce slot 和 security-time tuple。
func activationSecurityMatches(r ActivationRequest) bool {
	return r.ActiveReceipt.NonceSlot == r.NonceSlot && r.ActiveReceipt.SecurityTimeLineageDigest == r.ExpectedLineageDigest && r.ActiveReceipt.SecurityTimeGeneration == r.ExpectedSecurityGeneration && r.ActiveReceipt.SecurityTimeRevision == r.ExpectedSecurityRevision
}

// Validate 验证 candidate 仅包含不可逆引用和证明摘要。
func (c Candidate) Validate() error {
	if !candidateHeaderValid(c) || !candidateIdentityValid(c) || !candidateReceiptValid(c) || !candidateSecurityValid(c) || !candidateReplayValid(c) {
		return ErrInvalidActivation
	}
	return nil
}

func candidateHeaderValid(c Candidate) bool {
	return c.SchemaVersion == SchemaVersion && c.State == StateCandidate && validText(c.OperationID) && validDigest(c.RequestDigest)
}
func candidateIdentityValid(c Candidate) bool {
	return validText(c.CredentialLeaseID) && validDigest(c.CredentialDigest) && validText(c.AgentID) && validText(c.IncarnationID)
}
func candidateReceiptValid(c Candidate) bool {
	return validDigest(c.BindingDigest) && validText(c.ActiveReceiptRef) && validDigest(c.ActiveReceiptDigest)
}
func candidateSecurityValid(c Candidate) bool {
	return validDigest(c.SecurityLineageDigest) && c.SecurityGeneration > 0 && c.SecurityRevision > 0
}
func candidateReplayValid(c Candidate) bool {
	return validText(c.AntiReplayReceiptRef) && validDigest(c.AntiReplayReceiptDigest)
}

// matchCurrentBinding 比较 current binding 与 prepared/active receipt 的完整 fence。
func matchCurrentBinding(request ActivationRequest, current CurrentBinding) error {
	if err := current.Validate(); err != nil {
		return err
	}
	if current.AgentID != request.Prepared.AgentID || current.IncarnationID != request.Prepared.IncarnationID || current.BindingVersion != request.ExpectedBindingVersion || current.BindingDigest != request.ExpectedBindingDigest || current.ActiveProcessReceiptRef != request.Prepared.ActiveProcessReceiptRef || current.ActiveProcessReceiptRef != request.ActiveReceipt.ActiveProcessReceiptRef {
		return ErrInvalidBinding
	}
	return nil
}

// matchSecurityTime 比较 security-time tuple 并检查 active receipt 时间窗。
func matchSecurityTime(request ActivationRequest, observation SecurityTimeObservation) error {
	if err := observation.Validate(); err != nil {
		return err
	}
	if observation.LineageDigest != request.ExpectedLineageDigest || observation.Generation != request.ExpectedSecurityGeneration || observation.Revision != request.ExpectedSecurityRevision {
		return ErrInvalidSecurityTime
	}
	issued, expires, err := parseWindow(request.ActiveReceipt.IssuedAt, request.ActiveReceipt.ExpiresAt)
	if err != nil {
		return ErrInvalidSecurityTime
	}
	now, err := parseTime(observation.ObservedAt)
	if err != nil || now.Before(issued) || !now.Before(expires) {
		return ErrInvalidSecurityTime
	}
	return nil
}

// matchAntiReplay 比较 anti-replay owner 返回的单次消费 receipt。
func matchAntiReplay(request AntiReplayRequest, receipt AntiReplayReceipt) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if receipt.OperationID != request.OperationID || receipt.RequestDigest != request.RequestDigest || receipt.CredentialLeaseID != request.CredentialLeaseID || receipt.CredentialDigest != request.CredentialDigest || receipt.ConnectionNonceDigest != request.ConnectionNonceDigest || receipt.NonceSlot != request.NonceSlot {
		return ErrInvalidAntiReplay
	}
	return nil
}

// ComputeActiveReceiptDigest 计算 active receipt 的 domain-separated 摘要。
func ComputeActiveReceiptDigest(receipt ActiveCredentialReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return digestCanonical(receiptDigestDomain, receipt)
}

// ComputeActivationRequestDigest 计算 activation request 的 domain-separated 摘要。
func ComputeActivationRequestDigest(request ActivationRequest) (string, error) {
	request.RequestDigest = ""
	return digestCanonical(requestDigestDomain, request)
}

// ComputeSecurityTimeQueryDigest 计算 security-time query 的 domain-separated 摘要。
func ComputeSecurityTimeQueryDigest(query SecurityTimeQuery) (string, error) {
	return digestCanonical(queryDigestDomain, query)
}

// ComputeSecurityTimeObservationDigest 计算 security-time observation 的 domain-separated 摘要。
func ComputeSecurityTimeObservationDigest(observation SecurityTimeObservation) (string, error) {
	observation.ReceiptDigest = ""
	return digestCanonical(observationDigestDomain, observation)
}

// ComputeAntiReplayReceiptDigest 计算 anti-replay receipt 的 domain-separated 摘要。
func ComputeAntiReplayReceiptDigest(receipt AntiReplayReceipt) (string, error) {
	receipt.ReceiptDigest = ""
	return digestCanonical(nonceDigestDomain, receipt)
}

// parseWindow 解析并返回 UTC activation 时间窗。
func parseWindow(issued, expires string) (time.Time, time.Time, error) {
	issuedAt, err := parseTime(issued)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	expiresAt, err := parseTime(expires)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return issuedAt, expiresAt, nil
}

// parseTime 只接受 canonical UTC RFC3339Nano 时间。
func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC {
		return time.Time{}, ErrInvalidSecurityTime
	}
	return parsed, nil
}

// digestCanonical 使用 domain-separated JSON 计算稳定 SHA-256 摘要。
func digestCanonical(domain string, value any) (string, error) {
	payload, err := json.Marshal(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{domain, value})
	if err != nil {
		return "", fmt.Errorf("encode credential activation digest: %w", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

// validText 检查有界 UTF-8 文本并拒绝控制字符。
func validText(value string) bool {
	if value == "" || len(value) > maxText || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	return true
}

// validDigest 检查小写 SHA-256 十六进制摘要。
func validDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

// interfaceIsNil 统一识别接口中的 typed nil owner。
func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
