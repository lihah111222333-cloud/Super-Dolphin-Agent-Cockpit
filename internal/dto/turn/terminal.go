package turn

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// TurnRefV1 是 canonical turn 身份在 Go 生产链中的序列化类型。
type TurnRefV1 struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// PublicErrorV1 是可安全发布到外部事件面的错误描述。
type PublicErrorV1 struct {
	Code            string   `json:"code"`
	Title           string   `json:"title"`
	Message         string   `json:"message"`
	DiagnosticID    string   `json:"diagnosticId"`
	Retryable       bool     `json:"retryable"`
	RecoveryActions []string `json:"recoveryActions"`
}

// TurnTerminalV2 是 turn/terminal 唯一公开终态载荷。
type TurnTerminalV2 struct {
	SchemaVersion        int            `json:"schemaVersion"`
	EventID              string         `json:"eventId"`
	ThreadID             string         `json:"threadId"`
	TurnID               string         `json:"turnId"`
	Outcome              string         `json:"outcome"`
	TerminationCause     string         `json:"terminationCause,omitempty"`
	TerminationRequestID string         `json:"terminationRequestId,omitempty"`
	PublicError          *PublicErrorV1 `json:"publicError,omitempty"`
	PartialItemIDs       []string       `json:"partialItemIds,omitempty"`
	OccurredAt           string         `json:"occurredAt"`
}

// NewTurnTerminalV2 把 provider-neutral TurnCompleted 映射成 canonical 终态并调用生成 validator。
func NewTurnTerminalV2(ev TurnCompleted, eventID string) (TurnTerminalV2, error) {
	turnRef := TurnRefV1{ThreadID: strings.TrimSpace(ev.ThreadID), TurnID: strings.TrimSpace(ev.TurnID)}
	if err := ValidateTurnRefV1(turnRef); err != nil {
		return TurnTerminalV2{}, fmt.Errorf("turn ref contract: %w", err)
	}
	outcome, err := canonicalTerminalOutcome(ev)
	if err != nil {
		return TurnTerminalV2{}, err
	}
	terminal := TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       strings.TrimSpace(eventID),
		ThreadID:      turnRef.ThreadID,
		TurnID:        turnRef.TurnID,
		Outcome:       outcome,
	}
	if ev.Timestamp.IsZero() {
		return TurnTerminalV2{}, errors.New("turn terminal occurredAt is required")
	}
	terminal.OccurredAt = ev.Timestamp.UTC().Format(time.RFC3339Nano)
	applyTerminalDependencies(&terminal, ev)
	if terminal.PublicError != nil {
		if err := ValidatePublicErrorV1(*terminal.PublicError); err != nil {
			return TurnTerminalV2{}, fmt.Errorf("public error contract: %w", err)
		}
	}
	if err := ValidateTurnTerminalV2(terminal); err != nil {
		return TurnTerminalV2{}, fmt.Errorf("turn terminal contract: %w", err)
	}
	return terminal, nil
}

// canonicalTerminalOutcome 严格折叠内部 status/success，不允许冲突或未知终态。
func canonicalTerminalOutcome(ev TurnCompleted) (string, error) {
	status := strings.ToLower(strings.TrimSpace(ev.Status))
	switch status {
	case "completed":
		if ev.Success && strings.TrimSpace(ev.Error) == "" {
			return "success", nil
		}
	case "failed", "interrupted", "cancelled":
		if !ev.Success {
			return status, nil
		}
	default:
		return "", fmt.Errorf("turn terminal has unsupported status %q", status)
	}
	return "", fmt.Errorf("turn terminal success/status conflict: success=%v status=%q", ev.Success, status)
}

func applyTerminalDependencies(terminal *TurnTerminalV2, ev TurnCompleted) {
	if terminal.Outcome == "failed" {
		terminal.PublicError = terminalPublicError(terminal.EventID, "PROVIDER_FAILED", "Turn failed", "The provider could not complete this turn.")
		return
	}
	if terminal.Outcome != "interrupted" && terminal.Outcome != "cancelled" {
		return
	}
	terminal.TerminationCause = strings.TrimSpace(ev.Reason)
	terminal.TerminationRequestID = strings.TrimSpace(ev.TerminationRequestID)
	if terminal.TerminationCause != "user_request" {
		terminal.PublicError = terminalPublicError(terminal.EventID, "TURN_TERMINATED", "Turn ended", "The provider or system ended this turn.")
	}
}

func terminalPublicError(diagnosticID, code, title, message string) *PublicErrorV1 {
	return &PublicErrorV1{
		Code: code, Title: title, Message: message, DiagnosticID: diagnosticID,
		Retryable: true, RecoveryActions: []string{"retry", "copy_diagnostics"},
	}
}
