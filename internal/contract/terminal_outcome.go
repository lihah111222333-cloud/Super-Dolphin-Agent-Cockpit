package contract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

const (
	// TerminalOutcomeCapabilityV2 标识 provider/adapter 已提供完整终态围栏。
	TerminalOutcomeCapabilityV2 = "terminal_outcome_commit_v2"
	terminalOutcomeSchemaV2     = 2
)

var (
	// ErrTerminalOutcomeConflict 表示另一 canonical event 已赢得终态 CAS。
	ErrTerminalOutcomeConflict = errors.New("terminal outcome conflicts with canonical terminal")
	// ErrTerminalOutboxFence 表示 projector claim owner 已变化或记录不再可投影。
	ErrTerminalOutboxFence = errors.New("terminal outcome outbox fence mismatch")
	// ErrTerminalOutcomeActive 表示 current head 仍属于运行中的 turn，公开读端不得覆盖运行态。
	ErrTerminalOutcomeActive = errors.New("terminal outcome current head is active")
)

// TerminalOutcomeHeadActivation 在 provider turn 身份确定时建立真实、版本化的 current head。
type TerminalOutcomeHeadActivation struct {
	Capability          string
	AgentID             string
	PublicThreadID      string
	ProviderTurnID      string
	SessionID           string
	Generation          uint64
	ExpectedActiveState string
	ActivatedAt         time.Time
}

// TerminalOutcomeHead 是 current head 激活后返回给 runtime 的 CAS 版本。
type TerminalOutcomeHead struct {
	TerminalOutcomeHeadActivation
	Version uint64
}

// Validate 拒绝缺失 active owner、代际、状态或激活时间。
func (activation TerminalOutcomeHeadActivation) Validate() error {
	identity := CanonicalTerminalIdentity{
		Capability: activation.Capability, AgentID: activation.AgentID,
		PublicThreadID: activation.PublicThreadID, ProviderTurnID: activation.ProviderTurnID,
		SessionID: activation.SessionID, Generation: activation.Generation,
		EventID: "activation", TerminalIdentity: "activation",
		ExpectedActiveState: activation.ExpectedActiveState, HeadVersion: 1,
	}
	if err := identity.Validate(); err != nil {
		return err
	}
	if activation.ActivatedAt.IsZero() {
		return errors.New("terminal outcome head activatedAt is required")
	}
	return nil
}

// CanonicalTerminalIdentity 绑定一个公开 agent turn 的完整终态身份和 expected-state fence。
type CanonicalTerminalIdentity struct {
	Capability          string `json:"capability"`
	AgentID             string `json:"agentId"`
	PublicThreadID      string `json:"publicThreadId"`
	ProviderTurnID      string `json:"providerTurnId"`
	SessionID           string `json:"sessionId"`
	Generation          uint64 `json:"generation"`
	EventID             string `json:"eventId"`
	TerminalIdentity    string `json:"terminalIdentity"`
	ExpectedActiveState string `json:"expectedActiveState"`
	HeadVersion         uint64 `json:"headVersion"`
}

// Validate 拒绝缺失、旧 capability 和终态 expected state，禁止空 session/generation 兼容补齐。
func (identity CanonicalTerminalIdentity) Validate() error {
	required := map[string]string{
		"capability": identity.Capability, "agent id": identity.AgentID,
		"public thread id": identity.PublicThreadID, "provider turn id": identity.ProviderTurnID,
		"session id": identity.SessionID, "event id": identity.EventID,
		"terminal identity": identity.TerminalIdentity, "expected active state": identity.ExpectedActiveState,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("canonical terminal identity %s is required", name)
		}
	}
	if identity.Capability != TerminalOutcomeCapabilityV2 {
		return fmt.Errorf("canonical terminal identity capability %q is unsupported", identity.Capability)
	}
	if identity.Generation == 0 {
		return errors.New("canonical terminal identity generation is required")
	}
	if identity.HeadVersion == 0 {
		return errors.New("canonical terminal identity head version is required")
	}
	if terminalOutcomeStateIsTerminal(identity.ExpectedActiveState) {
		return fmt.Errorf("canonical terminal expected active state %q is terminal", identity.ExpectedActiveState)
	}
	return nil
}

func terminalOutcomeStateIsTerminal(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "failed", "stopped", "archived", "completed", "cancelled", "canceled", "interrupted":
		return true
	default:
		return false
	}
}

// PublicOutcome 是 Board/GetReport/冷启动允许读取的 durable 公开终态。
type PublicOutcome struct {
	Kind        string                 `json:"kind"`
	Code        string                 `json:"code"`
	Summary     string                 `json:"summary,omitempty"`
	PublicError *turndto.PublicErrorV1 `json:"publicError,omitempty"`
	CompletedAt time.Time              `json:"completedAt"`
}

// UnmarshalJSON 对 durable public outcome 保持 additionalProperties=false。
func (outcome *PublicOutcome) UnmarshalJSON(data []byte) error {
	type wire PublicOutcome
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value wire
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("public terminal outcome contains multiple JSON values")
		}
		return err
	}
	*outcome = PublicOutcome(value)
	return outcome.ValidatePublicOutcome()
}

// ValidatePublicOutcome 只允许明确的公开成功摘要或 canonical PublicError。
func (outcome PublicOutcome) ValidatePublicOutcome() error {
	if err := validatePublicOutcomeContent(outcome); err != nil {
		return err
	}
	if strings.TrimSpace(outcome.Code) == "" {
		return errors.New("public terminal outcome code is required")
	}
	if !safeTerminalOutcomeCode(outcome.Code) {
		return errors.New("public terminal outcome code contains unsupported characters")
	}
	if outcome.CompletedAt.IsZero() {
		return errors.New("public terminal outcome completedAt is required")
	}
	return nil
}

// validatePublicOutcomeContent 锁定 success 与 failure/stopped 的互斥公开字段。
func validatePublicOutcomeContent(outcome PublicOutcome) error {
	switch strings.TrimSpace(outcome.Kind) {
	case "success":
		if strings.TrimSpace(outcome.Summary) == "" || outcome.PublicError != nil {
			return errors.New("public success outcome requires summary and rejects public error")
		}
	case "failure", "stopped":
		if outcome.PublicError == nil {
			return errors.New("public terminal failure requires public error")
		}
		if strings.TrimSpace(outcome.Summary) != "" {
			return errors.New("public terminal failure rejects free-form summary")
		}
		if err := turndto.ValidatePublicErrorV1(*outcome.PublicError); err != nil {
			return fmt.Errorf("public terminal error: %w", err)
		}
	default:
		return fmt.Errorf("public terminal outcome kind %q is unsupported", outcome.Kind)
	}
	return nil
}

func safeTerminalOutcomeCode(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return false
	}
	for _, value := range code {
		if !safeTerminalOutcomeCodeRune(value) {
			return false
		}
	}
	return true
}

// safeTerminalOutcomeCodeRune 只允许稳定机器码使用的 ASCII 字符。
func safeTerminalOutcomeCodeRune(value rune) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' ||
		strings.ContainsRune("_.-", value)
}

// TerminalOutcomeCommit 是 DB terminal commit 与 outbox enqueue 的唯一写入载荷。
type TerminalOutcomeCommit struct {
	SchemaVersion  int                       `json:"schemaVersion"`
	ProjectionKind string                    `json:"projectionKind"`
	Identity       CanonicalTerminalIdentity `json:"identity"`
	PublicOutcome  PublicOutcome             `json:"publicOutcome"`
	PublicReport   string                    `json:"publicReport"`
	OccurredAt     time.Time                 `json:"occurredAt"`
	PrivateDAG     *OwnerScopedDAGPayload    `json:"-"`
}

// OwnerScopedDAGPayload 是只允许 DAG owner projector 消费的私有结果，不进入 public JSON。
type OwnerScopedDAGPayload struct {
	OwnerAgentID   string `json:"ownerAgentId"`
	PublicThreadID string `json:"publicThreadId"`
	ProviderTurnID string `json:"providerTurnId"`
	Result         string `json:"result"`
}

// Validate 锁定私有 DAG payload owner 与 canonical public identity 同源。
func (payload OwnerScopedDAGPayload) Validate(identity CanonicalTerminalIdentity) error {
	if strings.TrimSpace(payload.OwnerAgentID) == "" ||
		strings.TrimSpace(payload.PublicThreadID) == "" ||
		strings.TrimSpace(payload.ProviderTurnID) == "" ||
		strings.TrimSpace(payload.Result) == "" {
		return errors.New("owner-scoped DAG payload requires owner, thread, turn and result")
	}
	if payload.OwnerAgentID != identity.AgentID ||
		payload.PublicThreadID != identity.PublicThreadID ||
		payload.ProviderTurnID != identity.ProviderTurnID {
		return errors.New("owner-scoped DAG payload identity mismatch")
	}
	return nil
}

// Validate 对整个 terminal commit 执行 fail-fast 字段和安全合同校验。
func (commit TerminalOutcomeCommit) Validate() error {
	if commit.SchemaVersion != terminalOutcomeSchemaV2 {
		return fmt.Errorf("terminal outcome schema version %d is unsupported", commit.SchemaVersion)
	}
	switch commit.ProjectionKind {
	case "turn_completed", "agent_failed", "agent_stopped", "process_failed", "process_stopped":
	default:
		return fmt.Errorf("terminal outcome projection kind %q is unsupported", commit.ProjectionKind)
	}
	if err := commit.Identity.Validate(); err != nil {
		return err
	}
	if err := commit.PublicOutcome.ValidatePublicOutcome(); err != nil {
		return err
	}
	if strings.TrimSpace(commit.PublicReport) == "" {
		return errors.New("terminal outcome public report is required")
	}
	if commit.PublicReport != expectedTerminalPublicReport(commit.ProjectionKind, commit.PublicOutcome) {
		return errors.New("terminal outcome public report does not match canonical public outcome")
	}
	if commit.OccurredAt.IsZero() || !commit.OccurredAt.Equal(commit.PublicOutcome.CompletedAt) {
		return errors.New("terminal outcome occurredAt must equal public outcome completedAt")
	}
	return validateTerminalPrivateDAG(commit.PrivateDAG, commit.Identity)
}

func validateTerminalPrivateDAG(payload *OwnerScopedDAGPayload, identity CanonicalTerminalIdentity) error {
	if payload == nil {
		return nil
	}
	return payload.Validate(identity)
}

func expectedTerminalPublicReport(projectionKind string, outcome PublicOutcome) string {
	if outcome.Kind == "success" {
		return outcome.Summary
	}
	prefix := "agent"
	if projectionKind == "turn_completed" {
		prefix = "turn"
	}
	if outcome.PublicError == nil {
		return ""
	}
	return fmt.Sprintf("%s %s: %s (diagnostic id: %s)",
		prefix, outcome.Kind, outcome.PublicError.Message, outcome.PublicError.DiagnosticID)
}

// UnmarshalJSON 保持 durable terminal commit additionalProperties=false。
func (commit *TerminalOutcomeCommit) UnmarshalJSON(data []byte) error {
	type wire TerminalOutcomeCommit
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value wire
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("terminal outcome commit contains multiple JSON values")
		}
		return err
	}
	*commit = TerminalOutcomeCommit(value)
	return commit.Validate()
}

// TerminalOutcomeCommitResult 返回 canonical outcome 与同事务 outbox identity。
type TerminalOutcomeCommitResult struct {
	Outcome  TerminalOutcomeCommit
	OutboxID int64
	Replayed bool
}

// TerminalOutcomeOutboxItem 是 projector claim 后的安全公开投影载荷。
type TerminalOutcomeOutboxItem struct {
	ID             int64
	Outcome        TerminalOutcomeCommit
	PrivateDAG     *OwnerScopedDAGPayload
	ClaimToken     string
	LeaseExpiresAt time.Time
}

// TerminalOutcomeCommitPort 是 terminal write、durable public read 与 replay 的唯一持久化端口。
type TerminalOutcomeCommitPort interface {
	ActivateTerminalOutcomeHead(ctx context.Context, activation TerminalOutcomeHeadActivation) (TerminalOutcomeHead, error)
	CommitTerminalOutcome(ctx context.Context, commit TerminalOutcomeCommit) (TerminalOutcomeCommitResult, error)
	GetPublicTerminalOutcome(ctx context.Context, agentID string) (TerminalOutcomeCommit, error)
	ClaimTerminalOutcomeOutbox(ctx context.Context, workerID string, lease time.Duration, limit int) ([]TerminalOutcomeOutboxItem, error)
	RenewTerminalOutcomeOutbox(ctx context.Context, outboxID int64, workerID, claimToken string, lease time.Duration) (time.Time, error)
	MarkTerminalOutcomeProjected(ctx context.Context, outboxID int64, workerID, claimToken string) error
}
