package turn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// UnmarshalJSON 保持 canonical schema 的 additionalProperties=false 语义。
func (terminal *TurnTerminalV2) UnmarshalJSON(data []byte) error {
	type wireTurnTerminal TurnTerminalV2
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded wireTurnTerminal
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("turn terminal contains multiple JSON values")
		}
		return err
	}
	*terminal = TurnTerminalV2(decoded)
	return nil
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
		SchemaVersion:  2,
		EventID:        strings.TrimSpace(eventID),
		ThreadID:       turnRef.ThreadID,
		TurnID:         turnRef.TurnID,
		Outcome:        outcome,
		PartialItemIDs: cloneStrings(ev.PartialItemIDs),
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

// AttachCanonicalTurnTerminal 把远端 canonical truth 以不可导出的深拷贝附着到内部事件。
func AttachCanonicalTurnTerminal(ev TurnCompleted, terminal TurnTerminalV2) (TurnCompleted, error) {
	if ev.canonicalTerminal != nil {
		return TurnCompleted{}, errors.New("turn completed already has canonical terminal")
	}
	if err := ValidateTurnTerminalV2(terminal); err != nil {
		return TurnCompleted{}, fmt.Errorf("turn terminal contract: %w", err)
	}
	if err := validateTerminalIdentity(ev, terminal); err != nil {
		return TurnCompleted{}, err
	}
	clone := cloneTurnTerminalV2(terminal)
	ev.canonicalTerminal = &clone
	return ev, nil
}

// CanonicalTurnTerminal 返回远端 canonical truth 的深拷贝；本地事件返回 ok=false。
func CanonicalTurnTerminal(ev TurnCompleted) (TurnTerminalV2, bool, error) {
	if ev.canonicalTerminal == nil {
		return TurnTerminalV2{}, false, nil
	}
	terminal := cloneTurnTerminalV2(*ev.canonicalTerminal)
	if err := ValidateTurnTerminalV2(terminal); err != nil {
		return TurnTerminalV2{}, false, fmt.Errorf("turn terminal contract: %w", err)
	}
	if err := validateTerminalIdentity(ev, terminal); err != nil {
		return TurnTerminalV2{}, false, err
	}
	return terminal, true, nil
}

// validateTerminalIdentity 锁定内部 header 与 canonical TurnRef/occurredAt 的同一性。
func validateTerminalIdentity(ev TurnCompleted, terminal TurnTerminalV2) error {
	if ev.ThreadID != terminal.ThreadID || ev.TurnID != terminal.TurnID {
		return fmt.Errorf("turn terminal identity mismatch: header=%q/%q canonical=%q/%q", ev.ThreadID, ev.TurnID, terminal.ThreadID, terminal.TurnID)
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, terminal.OccurredAt)
	if err != nil {
		return fmt.Errorf("turn terminal occurredAt: %w", err)
	}
	if ev.Timestamp.IsZero() || !ev.Timestamp.Equal(occurredAt) {
		return fmt.Errorf("turn terminal timestamp mismatch: header=%q canonical=%q", ev.Timestamp.Format(time.RFC3339Nano), terminal.OccurredAt)
	}
	return nil
}

func cloneTurnTerminalV2(terminal TurnTerminalV2) TurnTerminalV2 {
	clone := TurnTerminalV2{
		SchemaVersion:        terminal.SchemaVersion,
		EventID:              terminal.EventID,
		ThreadID:             terminal.ThreadID,
		TurnID:               terminal.TurnID,
		Outcome:              terminal.Outcome,
		TerminationCause:     terminal.TerminationCause,
		TerminationRequestID: terminal.TerminationRequestID,
		PartialItemIDs:       cloneStrings(terminal.PartialItemIDs),
		OccurredAt:           terminal.OccurredAt,
	}
	if terminal.PublicError != nil {
		clone.PublicError = &PublicErrorV1{
			Code:            terminal.PublicError.Code,
			Title:           terminal.PublicError.Title,
			Message:         terminal.PublicError.Message,
			DiagnosticID:    terminal.PublicError.DiagnosticID,
			Retryable:       terminal.PublicError.Retryable,
			RecoveryActions: cloneStrings(terminal.PublicError.RecoveryActions),
		}
	}
	return clone
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	clone := make([]string, len(values))
	copy(clone, values)
	return clone
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
		Retryable: false, RecoveryActions: []string{"copy_diagnostics"},
	}
}
