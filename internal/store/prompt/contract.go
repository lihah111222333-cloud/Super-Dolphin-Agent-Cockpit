package prompt

import (
	"context"
	"encoding/json"
	"time"
)

// Reader provides read-only access to prompt templates.
// This is the shared interface consumed by both internal modules and cmd/mcp-orch.
type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
}

// Store 是 p20.1 恢复的完整写能力接口。
//
// 历史：c50ef009 (2026-03-25) 删除 dashboard/prompt_rpc.go 时同步删掉了
// store.Write 接口，导致 `prompts/list|write|delete` 宛主 RPC 无件可用。
// 这里以 p20.1 任务单方案 B（merge-in-place）恢复，使 `dashboard` 所依赖的
// `Reader` 保持向后兼容——`Store` 嵌入 `Reader`，不打破既有消费者。
//
// `WithTx` 的语义是：在 fn 内拿到的 `Store` 也应当可以参与同一事务。
// `Upsert` / `Delete` / `InsertVersion` 在 `WithTx` 内也走事务上下文。
type Store interface {
	Reader
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version PromptTemplateVersion) error
	Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
}

// PromptTemplateVersion 是 `prompt_template_versions` 历史表的写入 DTO。
// 还原自 c50ef009^ 的 upstream 定义，字段顺序对齐 `sqlc.InsertPromptVersionParams`
// 导出（tool_name / description / enabled / created_by / updated_by / source_updated_at）。
type PromptTemplateVersion struct {
	PromptKey       string
	Title           string
	AgentKey        string
	ToolName        string
	PromptText      string
	Variables       json.RawMessage
	Tags            json.RawMessage
	Description     string
	Enabled         bool
	CreatedBy       string
	UpdatedBy       string
	SourceUpdatedAt *time.Time
}

type ListFilter struct {
	AgentKey string
	Keyword  string
	Limit    int32
}

// PromptTemplate is the shared domain DTO for prompt templates.
type PromptTemplate struct {
	ID          int64           `json:"id"`
	PromptKey   string          `json:"prompt_key"`
	Title       string          `json:"title"`
	AgentKey    string          `json:"agent_key"`
	ToolName    string          `json:"tool_name"`
	PromptText  string          `json:"prompt_text"`
	Variables   json.RawMessage `json:"variables"`
	Tags        json.RawMessage `json:"tags"`
	Enabled     bool            `json:"enabled"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Description string          `json:"description"`
}
