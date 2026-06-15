package eventsurface

import (
	"encoding/json"
	"errors"
	"strings"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

type RemoteParamDecoder func(any) error

// DecodeRemoteTurnCompleted 解码remoteturncompleted。
func DecodeRemoteTurnCompleted(decode RemoteParamDecoder) (turndto.TurnCompleted, error) {
	var ev turndto.TurnCompleted
	if err := decode(&ev); err != nil {
		return turndto.TurnCompleted{}, err
	}
	if strings.TrimSpace(ev.AgentID) == "" {
		return turndto.TurnCompleted{}, errors.New("remote turn completed missing agent_id")
	}
	return ev, nil
}

// DecodeRemoteTurnInterrupted 解码remoteturninterrupted。
func DecodeRemoteTurnInterrupted(decode RemoteParamDecoder) (turndto.TurnInterrupted, error) {
	var ev turndto.TurnInterrupted
	if err := decode(&ev); err == nil && strings.TrimSpace(ev.AgentID) != "" {
		return ev, nil
	}
	var payload map[string]json.RawMessage
	if err := decode(&payload); err != nil {
		return turndto.TurnInterrupted{}, err
	}
	ev.TurnHeader = shareddto.TurnHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{ThreadID: remoteEventString(payload, "thread_id", "threadId")},
			AgentID:      remoteEventString(payload, "agent_id", "agentId"),
		},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: remoteEventString(payload, "turn_id", "turnId")},
	}
	ev.Reason = remoteEventString(payload, "reason")
	if strings.TrimSpace(ev.AgentID) == "" {
		return turndto.TurnInterrupted{}, errors.New("remote turn interrupted missing agent_id")
	}
	return ev, nil
}

func remoteEventString(payload map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var value string
		if err := json.Unmarshal(payload[key], &value); err == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
