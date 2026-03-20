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
	AgentID     string
	AgentName   string
	SessionID   string
	Status      string
	StagnantSec int32
	Error       string
	OutputTail  json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
