package eventsurface

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// RemoteParamDecoder 抽象 JSON-RPC 参数解码函数，便于同一解码逻辑复用到不同远端事件。
type RemoteParamDecoder func(any) error

const (
	remotePublicErrorCodeFallback  = "REMOTE_TERMINAL_FAILED"
	remotePublicErrorTitle         = "Remote terminal error"
	remotePublicErrorMessage       = "Remote terminal error"
	remoteDiagnosticIDFallback     = "diag-remote-terminal-error"
	remotePublicErrorCodeMaxLength = 64
	remotePublicErrorCodeAlphabet  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_"
)

// DecodeRemoteTurnTerminal 解码并严格验证 canonical turn/terminal，不推断 owner 身份。
func DecodeRemoteTurnTerminal(decode RemoteParamDecoder) (turndto.TurnTerminalV2, error) {
	var terminal turndto.TurnTerminalV2
	if err := decode(&terminal); err != nil {
		return turndto.TurnTerminalV2{}, err
	}
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return turndto.TurnTerminalV2{}, fmt.Errorf("remote turn terminal contract: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, terminal.OccurredAt); err != nil {
		return turndto.TurnTerminalV2{}, fmt.Errorf("remote turn terminal occurredAt: %w", err)
	}
	terminal.PublicError = sanitizeRemotePublicError(terminal.PublicError)
	return terminal, nil
}

// sanitizeRemotePublicError 清除远端展示文本，仅保留受限的机器可读字段。
func sanitizeRemotePublicError(remote *turndto.PublicErrorV1) *turndto.PublicErrorV1 {
	if remote == nil {
		return nil
	}
	return &turndto.PublicErrorV1{
		Code:            safeRemotePublicErrorCode(remote.Code),
		Title:           remotePublicErrorTitle,
		Message:         remotePublicErrorMessage,
		DiagnosticID:    safeRemoteDiagnosticID(remote.DiagnosticID),
		Retryable:       false,
		RecoveryActions: safeRemoteRecoveryActions(remote.RecoveryActions),
	}
}

// safeRemoteRecoveryActions 只保留前端已实现的固定恢复动作。
func safeRemoteRecoveryActions(actions []string) []string {
	if slices.Contains(actions, "copy_diagnostics") {
		return []string{"copy_diagnostics"}
	}
	return []string{}
}

// safeRemotePublicErrorCode 保留受限机器码，未知展示语义由前端 registry 决定。
func safeRemotePublicErrorCode(value string) string {
	if isSafeRemotePublicErrorCode(value) {
		return value
	}
	return remotePublicErrorCodeFallback
}

// isSafeRemotePublicErrorCode 验证远端机器码不包含可显示的自由文本。
func isSafeRemotePublicErrorCode(value string) bool {
	return value != "" && len(value) <= remotePublicErrorCodeMaxLength && containsOnly(value, remotePublicErrorCodeAlphabet)
}

// safeRemoteDiagnosticID 通过 canonical PublicErrorV1 schema 保留安全关联标识。
func safeRemoteDiagnosticID(value string) string {
	if err := turndto.ValidatePublicErrorV1(turndto.PublicErrorV1{
		Code: remotePublicErrorCodeFallback, Title: remotePublicErrorTitle, Message: remotePublicErrorMessage,
		DiagnosticID: value, Retryable: false, RecoveryActions: []string{},
	}); err == nil {
		return value
	}
	return remoteDiagnosticIDFallback
}

// containsOnly 验证文本的每个字符都属于调用方提供的受限字符集合。
func containsOnly(value, alphabet string) bool {
	for _, character := range value {
		if !strings.ContainsRune(alphabet, character) {
			return false
		}
	}
	return true
}

// ProjectRemoteTurnTerminal 把 canonical 终态投影为内部事件；ownerAgentID 必须由 consumer 的运行时映射提供。
func ProjectRemoteTurnTerminal(terminal turndto.TurnTerminalV2, ownerAgentID string) (turndto.TurnCompleted, error) {
	ownerAgentID = strings.TrimSpace(ownerAgentID)
	if ownerAgentID == "" {
		return turndto.TurnCompleted{}, errors.New("remote turn terminal owner agent id is required")
	}
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return turndto.TurnCompleted{}, fmt.Errorf("remote turn terminal contract: %w", err)
	}
	timestamp, err := time.Parse(time.RFC3339Nano, terminal.OccurredAt)
	if err != nil {
		return turndto.TurnCompleted{}, fmt.Errorf("remote turn terminal occurredAt: %w", err)
	}
	return turndto.AttachCanonicalTurnTerminal(remoteTurnCompleted(terminal, timestamp, ownerAgentID), terminal)
}

func remoteTurnCompleted(terminal turndto.TurnTerminalV2, timestamp time.Time, ownerAgentID string) turndto.TurnCompleted {
	errorText := ""
	if terminal.PublicError != nil {
		errorText = terminal.PublicError.Message
	}
	return turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{EventHeader: shareddto.EventHeader{Timestamp: timestamp}, ThreadID: terminal.ThreadID},
				AgentID:      ownerAgentID,
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: terminal.TurnID},
		},
		Success:              terminal.Outcome == "success",
		Status:               remoteTerminalStatus(terminal.Outcome),
		Summary:              terminal.PublicSummary,
		Reason:               terminal.TerminationCause,
		Error:                errorText,
		TerminationRequestID: terminal.TerminationRequestID,
		PartialItemIDs:       append([]string(nil), terminal.PartialItemIDs...),
	}
}

func remoteTerminalStatus(outcome string) string {
	if outcome == "success" {
		return "completed"
	}
	return outcome
}
