package agentstatus

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Upsert(ctx context.Context, params UpsertParams) (*AgentStatus, error)
	Get(ctx context.Context, agentID string) (*AgentStatus, error)
	List(ctx context.Context, status string) ([]AgentStatus, error)
}

type UpsertParams struct {
	AgentID     string
	AgentName   string
	SessionID   string
	Status      string
	StagnantSec int32
	Error       string
	OutputTail  json.RawMessage
}

type AgentStatus struct {
	AgentID     string          `json:"agent_id"`
	AgentName   string          `json:"agent_name"`
	SessionID   string          `json:"session_id"`
	Status      string          `json:"status"`
	StagnantSec int32           `json:"stagnant_sec"`
	Error       string          `json:"error"`
	OutputTail  json.RawMessage `json:"output_tail"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}
