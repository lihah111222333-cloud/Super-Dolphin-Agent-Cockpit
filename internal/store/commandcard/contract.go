// Package commandcard 提供命令卡片的只读持久化接口和前端 JSON wire DTO。
package commandcard

import (
	"context"
	"encoding/json"
	"time"
)

// Reader 定义命令卡片只读访问边界，供内部模块和 cmd/mcp-orch 共用。
type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]CommandCard, error)
}

// ListFilter 是命令卡片列表过滤条件，Limit 由调用方控制窗口大小。
type ListFilter struct {
	Keyword string
	Limit   int32
}

// CommandCard 是命令卡片的跨模块 DTO，ArgsSchema 保持原始 JSON 以便 UI 动态渲染参数。
type CommandCard struct {
	ID              int64           `json:"id"`
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema"`
	RiskLevel       string          `json:"risk_level"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastRunAt       *time.Time      `json:"last_run_at,omitempty"`
	RunCount        int64           `json:"run_count"`
}
