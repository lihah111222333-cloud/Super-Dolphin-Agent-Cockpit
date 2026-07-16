package eventsurface

import (
	"errors"
	"fmt"
	"strings"
	"time"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// RemoteParamDecoder 抽象 JSON-RPC 参数解码函数，便于同一解码逻辑复用到不同远端事件。
type RemoteParamDecoder func(any) error

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
	return terminal, nil
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
	return remoteTurnCompleted(terminal, timestamp, ownerAgentID), nil
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
		Reason:               terminal.TerminationCause,
		Error:                errorText,
		TerminationRequestID: terminal.TerminationRequestID,
	}
}

func remoteTerminalStatus(outcome string) string {
	if outcome == "success" {
		return "completed"
	}
	return outcome
}
