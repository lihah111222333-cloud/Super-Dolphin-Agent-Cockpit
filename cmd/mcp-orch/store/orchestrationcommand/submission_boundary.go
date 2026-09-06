package orchestrationcommand

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	ErrSubmissionConflict         = errors.New("command submission identity conflict")
	ErrSubmissionNotFound         = errors.New("command submission not found")
	ErrSubmissionUnresolved       = errors.New("command submission outcome is unresolved")
	ErrSubmissionConsumed         = errors.New("command submission permit was already consumed")
	ErrSubmissionTerminal         = errors.New("command submission is terminal")
	ErrSubmissionProvider         = errors.New("command submission provider is required")
	ErrSubmissionAcknowledgement  = errors.New("command submission acknowledgement is invalid")
	ErrSubmissionNotIssued        = errors.New("command submission permit was not issued")
	ErrSubmissionStoreUnavailable = errors.New("command submission store is unavailable")
)

// SubmissionStatus describes the provider boundary only. It does not claim
// that the command reached a local execution terminal state.
type SubmissionStatus string

const (
	SubmissionPending              SubmissionStatus = "pending"
	SubmissionOutcomeUnknown       SubmissionStatus = "outcome_unknown"
	SubmissionProviderAcknowledged SubmissionStatus = "provider_acknowledged"
	SubmissionProviderFailed       SubmissionStatus = "provider_definitive_failure"
)

// SubmissionRequest is the stable command/submission identity. SubmissionID
// is optional for callers that want this candidate to derive it from the
// command and original idempotency tuple.
type SubmissionRequest struct {
	CommandID            string `json:"command_id"`
	SubmissionID         string `json:"submission_id,omitempty"`
	IdempotencyNamespace string `json:"idempotency_namespace"`
	IdempotencyKeyDigest string `json:"idempotency_key_digest"`
	RequestDigest        string `json:"request_digest"`
}

// SubmissionKey is the read-only lookup handle for the original operation.
// It intentionally contains no provider material and cannot mint a permit.
type SubmissionKey struct {
	CommandID    string `json:"command_id"`
	SubmissionID string `json:"submission_id"`
}

// SubmissionPermit is one-use material. Consume must succeed before a
// provider call; the same material is retained for unknown reconciliation.
type SubmissionPermit struct {
	CommandID       string `json:"command_id"`
	SubmissionID    string `json:"submission_id"`
	RequestDigest   string `json:"request_digest"`
	DispatchReceipt string `json:"dispatch_receipt"`
	Permit          string `json:"permit"`
	Nonce           string `json:"nonce"`
}

// ProviderAcknowledgement is a typed provider response. Accepted is a
// provider receipt only, never command completion; unknown is sticky.
type ProviderAcknowledgement struct {
	Kind                 SubmissionStatus `json:"kind"`
	ProviderSubmissionID string           `json:"provider_submission_id,omitempty"`
	ProviderReceiptID    string           `json:"provider_receipt_id,omitempty"`
	FailureCode          string           `json:"failure_code,omitempty"`
}

// Submission is a candidate boundary record. Its status is independent from
// command lifecycle terminal state and remains queryable by original identity.
type Submission struct {
	Request         SubmissionRequest       `json:"request"`
	Permit          SubmissionPermit        `json:"permit"`
	Status          SubmissionStatus        `json:"status"`
	Acknowledgement ProviderAcknowledgement `json:"acknowledgement"`
}

// SubmissionProvider is deliberately narrower than any concrete provider
// package and returns a typed acknowledgement or an indeterminate error.
type SubmissionProvider interface {
	Submit(context.Context, SubmissionPermit) (ProviderAcknowledgement, error)
}

// CommandSubmission 是 Submission 的兼容别名。
type CommandSubmission = Submission

// CommandSubmissionRequest 是 SubmissionRequest 的兼容别名。
type CommandSubmissionRequest = SubmissionRequest

// CommandSubmissionKey 是 SubmissionKey 的兼容别名。
type CommandSubmissionKey = SubmissionKey

// CommandSubmissionPermit 是 SubmissionPermit 的兼容别名。
type CommandSubmissionPermit = SubmissionPermit

// ProviderAck 是 ProviderAcknowledgement 的兼容别名。
type ProviderAck = ProviderAcknowledgement

// SubmissionStore is an isolated candidate owner. It is intentionally
// in-memory until the command_submission schema and recovery owner are ready;
// it is not registered in Fx or any public tool surface.
type SubmissionStore struct {
	mu    sync.Mutex
	byKey map[string]*submissionEntry
	byID  map[string]*submissionEntry
	byCmd map[string]string
}

type submissionEntry struct {
	submission Submission
	consumed   bool
	issued     bool
}

// CommandBoundary and CommandSubmissionStore are descriptive compatibility
// names for callers that refer to this candidate by its boundary role.
type CommandBoundary = SubmissionStore

// CommandSubmissionStore 是 SubmissionStore 的兼容别名。
type CommandSubmissionStore = SubmissionStore

// NewSubmissionStore 创建隔离的 command_submission 候选 owner。
func NewSubmissionStore() *SubmissionStore {
	return &SubmissionStore{byKey: make(map[string]*submissionEntry), byID: make(map[string]*submissionEntry), byCmd: make(map[string]string)}
}

// NewCommandBoundary 创建通用 command boundary 候选。
func NewCommandBoundary() *SubmissionStore { return NewSubmissionStore() }

// NewCommandSubmissionStore 创建 command_submission 候选 owner。
func NewCommandSubmissionStore() *SubmissionStore { return NewSubmissionStore() }

// Prepare 记录稳定 submission identity；精确重放返回原记录且不重新生成 permit。
func (s *SubmissionStore) Prepare(ctx context.Context, req SubmissionRequest) (Submission, error) {
	if err := submissionReady(s, ctx); err != nil {
		return Submission{}, err
	}
	if err := validateSubmissionRequest(req); err != nil {
		return Submission{}, err
	}
	if req.SubmissionID == "" {
		req.SubmissionID = deriveSubmissionID(req)
	}
	key := submissionKey(req)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareLocked(req, key)
}

// prepareLocked 在 owner 锁内执行幂等冲突检查和首次 material 生成。
func (s *SubmissionStore) prepareLocked(req SubmissionRequest, key string) (Submission, error) {
	if existing, ok := s.byKey[key]; ok {
		if existing.submission.Request != req {
			return Submission{}, ErrSubmissionConflict
		}
		return existing.submission, nil
	}
	if priorID, ok := s.byCmd[req.CommandID]; ok && priorID != req.SubmissionID {
		return Submission{}, ErrSubmissionConflict
	}
	permit, err := newSubmissionPermit(req)
	if err != nil {
		return Submission{}, err
	}
	record := Submission{Request: req, Permit: permit, Status: SubmissionPending, Acknowledgement: ProviderAcknowledgement{Kind: SubmissionPending}}
	entry := &submissionEntry{submission: record}
	s.byKey[key], s.byID[identityKey(req.CommandID, req.SubmissionID)], s.byCmd[req.CommandID] = entry, entry, req.SubmissionID
	return record, nil
}

// Issue 仅为尚未发放的 submission 返回 permit；重放和未知结果均只读。
func (s *SubmissionStore) Issue(ctx context.Context, req SubmissionRequest) (SubmissionPermit, error) {
	record, err := s.Prepare(ctx, req)
	if err != nil {
		return SubmissionPermit{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.byID[identityKey(record.Request.CommandID, record.Request.SubmissionID)]
	if entry == nil {
		return SubmissionPermit{}, ErrSubmissionNotFound
	}
	if entry.consumed || entry.issued || entry.submission.Status == SubmissionOutcomeUnknown {
		return SubmissionPermit{}, ErrSubmissionUnresolved
	}
	if entry.submission.Status != SubmissionPending {
		return SubmissionPermit{}, ErrSubmissionTerminal
	}
	entry.issued = true
	return record.Permit, nil
}

// Query 只读取原 operation，不能创建或授权 submission，并保留 unknown 非授权状态。
func (s *SubmissionStore) Query(ctx context.Context, key SubmissionKey) (Submission, error) {
	if err := submissionReady(s, ctx); err != nil {
		return Submission{}, err
	}
	if err := validateSubmissionKey(key); err != nil {
		return Submission{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[identityKey(key.CommandID, key.SubmissionID)]
	if !ok {
		return Submission{}, ErrSubmissionNotFound
	}
	return entry.submission, nil
}

// Get 是 Query 的兼容别名，仍保持只读语义。
func (s *SubmissionStore) Get(ctx context.Context, key SubmissionKey) (Submission, error) {
	return s.Query(ctx, key)
}

// Consume 在 provider 调用前原子消费 permit 并标记 unknown，阻断第二次外部调用。
func (s *SubmissionStore) Consume(ctx context.Context, permit SubmissionPermit) (Submission, error) {
	if err := submissionReady(s, ctx); err != nil {
		return Submission{}, err
	}
	if err := validateSubmissionPermit(permit); err != nil {
		return Submission{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[identityKey(permit.CommandID, permit.SubmissionID)]
	if !ok {
		return Submission{}, ErrSubmissionNotFound
	}
	if !permitMatchesSubmission(entry.submission, permit) {
		return Submission{}, ErrSubmissionConflict
	}
	if !entry.issued {
		return Submission{}, ErrSubmissionNotIssued
	}
	if entry.consumed || entry.submission.Status == SubmissionOutcomeUnknown {
		return Submission{}, errors.Join(ErrSubmissionConsumed, ErrSubmissionUnresolved)
	}
	if entry.submission.Status != SubmissionPending {
		return Submission{}, ErrSubmissionTerminal
	}
	entry.consumed = true
	entry.submission.Status = SubmissionOutcomeUnknown
	entry.submission.Acknowledgement = ProviderAcknowledgement{Kind: SubmissionOutcomeUnknown}
	return entry.submission, nil
}

// Execute 只消费并调用 typed provider 一次；错误、panic 和 unknown 均保持粘滞。
func (s *SubmissionStore) Execute(ctx context.Context, permit SubmissionPermit, provider SubmissionProvider) (Submission, error) {
	if err := submissionReady(s, ctx); err != nil {
		return Submission{}, err
	}
	if isNilDependency(provider) {
		return Submission{}, ErrSubmissionProvider
	}
	consumed, err := s.Consume(ctx, permit)
	if err != nil {
		return Submission{}, err
	}
	ack, submitErr := callSubmissionProvider(ctx, permit, provider)
	if submitErr != nil || ack.Kind == SubmissionOutcomeUnknown || ack.Kind == SubmissionPending {
		if submitErr == nil {
			submitErr = ErrSubmissionUnresolved
		}
		return consumed, errors.Join(ErrSubmissionUnresolved, submitErr)
	}
	resolved, err := s.ReconcileUnknownCandidate(ctx, permit, ack)
	if err != nil {
		return consumed, errors.Join(ErrSubmissionUnresolved, err)
	}
	return resolved, nil
}

// ReconcileUnknownCandidate 是未接权威回执验证器的本地候选对账，不能宣称 authoritative receipt。
func (s *SubmissionStore) ReconcileUnknownCandidate(ctx context.Context, permit SubmissionPermit, ack ProviderAcknowledgement) (Submission, error) {
	if err := submissionReady(s, ctx); err != nil {
		return Submission{}, err
	}
	if err := validateSubmissionPermit(permit); err != nil {
		return Submission{}, err
	}
	if err := ack.Validate(); err != nil {
		return Submission{}, err
	}
	if ack.Kind == SubmissionOutcomeUnknown || ack.Kind == SubmissionPending {
		return Submission{}, ErrSubmissionUnresolved
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[identityKey(permit.CommandID, permit.SubmissionID)]
	if !ok {
		return Submission{}, ErrSubmissionNotFound
	}
	if !permitMatchesSubmission(entry.submission, permit) {
		return Submission{}, ErrSubmissionConflict
	}
	if entry.submission.Status != SubmissionOutcomeUnknown {
		return Submission{}, ErrSubmissionUnresolved
	}
	entry.submission.Acknowledgement = ack
	if ack.Kind == SubmissionProviderAcknowledged {
		entry.submission.Status = SubmissionProviderAcknowledged
	} else {
		entry.submission.Status = SubmissionProviderFailed
	}
	return entry.submission, nil
}

// ReconcileUnknown 保留兼容名称，但结果仍是 candidate-only、非权威回执。
func (s *SubmissionStore) ReconcileUnknown(ctx context.Context, permit SubmissionPermit, ack ProviderAcknowledgement) (Submission, error) {
	return s.ReconcileUnknownCandidate(ctx, permit, ack)
}

// Validate 拒绝缺失回执、unknown 携带伪标识和混合结果类别。
func (a ProviderAcknowledgement) Validate() error {
	if !validAcknowledgementIdentifiers(a) {
		return ErrSubmissionAcknowledgement
	}
	return validateAcknowledgementKind(a)
}

// validAcknowledgementIdentifiers 检查所有 provider 标识的规范文本边界。
func validAcknowledgementIdentifiers(a ProviderAcknowledgement) bool {
	return (a.ProviderSubmissionID == "" || canonicalSubmissionText(a.ProviderSubmissionID, 256)) &&
		(a.ProviderReceiptID == "" || canonicalSubmissionText(a.ProviderReceiptID, 256)) &&
		(a.FailureCode == "" || canonicalSubmissionText(a.FailureCode, 256))
}

// validateAcknowledgementKind 校验 typed acknowledgement 的 one-hot 结果。
func validateAcknowledgementKind(a ProviderAcknowledgement) error {
	switch a.Kind {
	case SubmissionPending, SubmissionOutcomeUnknown:
		return validateEmptyAcknowledgement(a)
	case SubmissionProviderAcknowledged:
		return validateAcceptedAcknowledgement(a)
	case SubmissionProviderFailed:
		return validateFailedAcknowledgement(a)
	default:
		return ErrSubmissionAcknowledgement
	}
}

func validateEmptyAcknowledgement(a ProviderAcknowledgement) error {
	if a.ProviderSubmissionID != "" || a.ProviderReceiptID != "" || a.FailureCode != "" {
		return ErrSubmissionAcknowledgement
	}
	return nil
}

func validateAcceptedAcknowledgement(a ProviderAcknowledgement) error {
	if (a.ProviderSubmissionID == "" && a.ProviderReceiptID == "") || a.FailureCode != "" {
		return ErrSubmissionAcknowledgement
	}
	return nil
}

func validateFailedAcknowledgement(a ProviderAcknowledgement) error {
	if a.FailureCode == "" || a.ProviderSubmissionID != "" || a.ProviderReceiptID != "" {
		return ErrSubmissionAcknowledgement
	}
	return nil
}

// submissionReady 拒绝 nil/zero-value owner 与 nil/cancelled context。
func submissionReady(s *SubmissionStore, ctx context.Context) error {
	if s == nil || s.byKey == nil || s.byID == nil || s.byCmd == nil {
		return ErrSubmissionStoreUnavailable
	}
	if ctx == nil {
		return ErrContextRequired
	}
	return ctx.Err()
}

// validateSubmissionRequest 校验 command/submission 原始幂等字段，不补默认身份。
func validateSubmissionRequest(req SubmissionRequest) error {
	if err := validateText("command_id", req.CommandID, 128); err != nil {
		return err
	}
	if req.SubmissionID != "" {
		if err := validateText("submission_id", req.SubmissionID, 128); err != nil {
			return err
		}
	}
	if err := validateText("idempotency_namespace", req.IdempotencyNamespace, 256); err != nil {
		return err
	}
	if err := validateDigest("idempotency_key_digest", req.IdempotencyKeyDigest); err != nil {
		return err
	}
	return validateDigest("request_digest", req.RequestDigest)
}

func validateSubmissionKey(key SubmissionKey) error {
	if err := validateText("command_id", key.CommandID, 128); err != nil {
		return err
	}
	return validateText("submission_id", key.SubmissionID, 128)
}

// validateSubmissionPermit 校验 provider 只能携带完整且不可变的原 permit。
func validateSubmissionPermit(permit SubmissionPermit) error {
	if err := validateText("command_id", permit.CommandID, 128); err != nil {
		return err
	}
	if err := validateText("submission_id", permit.SubmissionID, 128); err != nil {
		return err
	}
	if err := validateDigest("request_digest", permit.RequestDigest); err != nil {
		return err
	}
	for name, value := range map[string]string{"dispatch_receipt": permit.DispatchReceipt, "permit": permit.Permit, "nonce": permit.Nonce} {
		if err := validateText(name, value, 256); err != nil {
			return err
		}
	}
	return nil
}

func newSubmissionPermit(req SubmissionRequest) (SubmissionPermit, error) {
	receipt, err := randomSubmissionMaterial("submission-receipt")
	if err != nil {
		return SubmissionPermit{}, err
	}
	permit, err := randomSubmissionMaterial("submission-permit")
	if err != nil {
		return SubmissionPermit{}, err
	}
	nonce, err := randomSubmissionMaterial("submission-nonce")
	if err != nil {
		return SubmissionPermit{}, err
	}
	return SubmissionPermit{CommandID: req.CommandID, SubmissionID: req.SubmissionID, RequestDigest: req.RequestDigest, DispatchReceipt: receipt, Permit: permit, Nonce: nonce}, nil
}

func randomSubmissionMaterial(prefix string) (string, error) {
	material := make([]byte, 24)
	if _, err := rand.Read(material); err != nil {
		return "", fmt.Errorf("generate %s: %w", prefix, err)
	}
	return prefix + ":" + hex.EncodeToString(material), nil
}

func deriveSubmissionID(req SubmissionRequest) string {
	raw := strings.Join([]string{"orch.command.submission/v1", req.CommandID, req.IdempotencyNamespace, req.IdempotencyKeyDigest, req.RequestDigest}, "\x00")
	digest := sha256.Sum256([]byte(raw))
	return "submission_" + hex.EncodeToString(digest[:])
}

func submissionKey(req SubmissionRequest) string {
	return req.IdempotencyNamespace + "\x00" + req.IdempotencyKeyDigest
}
func identityKey(commandID, submissionID string) string { return commandID + "\x00" + submissionID }
func (s Submission) submissionID() string               { return s.Request.SubmissionID }
func permitMatchesSubmission(s Submission, permit SubmissionPermit) bool {
	return s.Permit == permit && s.Request.CommandID == permit.CommandID && s.Request.SubmissionID == permit.SubmissionID && s.Request.RequestDigest == permit.RequestDigest
}
func canonicalSubmissionText(value string, limit int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func callSubmissionProvider(ctx context.Context, permit SubmissionPermit, provider SubmissionProvider) (ack ProviderAcknowledgement, err error) {
	defer func() {
		if recover() != nil {
			ack = ProviderAcknowledgement{Kind: SubmissionOutcomeUnknown}
			err = ErrSubmissionUnresolved
		}
	}()
	return provider.Submit(ctx, permit)
}
