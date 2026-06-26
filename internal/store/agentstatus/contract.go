// Package agentstatus 提供 agent 运行状态的持久化接口和前端 JSON wire DTO。
package agentstatus

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义 agent 状态读写边界，调用方只接触领域 DTO，不直接依赖 sqlc 行。
type Store interface {
	Upsert(ctx context.Context, params UpsertParams) (*AgentStatus, error)
	Get(ctx context.Context, agentID string) (*AgentStatus, error)
	List(ctx context.Context, status string) ([]AgentStatus, error)
}

// UpsertParams 是写入 agent 最新状态的输入，OutputTail 保持原始 JSON 片段用于 UI 展示。
type UpsertParams struct {
	AgentID     string
	AgentName   string
	SessionID   string
	Status      string
	StagnantSec int32
	Error       string
	OutputTail  json.RawMessage
}

// AgentStatus 是跨模块和前端 JSON wire 共用的 agent 状态快照。
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
