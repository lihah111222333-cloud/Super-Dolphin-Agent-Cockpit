package commandcard

import (
	"context"
	"encoding/json"
	"time"
)

// Reader 提供 command card 的只读列表查询能力。
type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]CommandCard, error)
}

// ListFilter 是 command card 列表查询过滤条件。
type ListFilter struct {
	Keyword string
	Limit   int32
}

// CommandCard 是命令卡片当前版本的运行时投影。
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

// Store 提供 command card 的读写和版本归档能力。
type Store interface {
	Reader
	Get(ctx context.Context, cardKey string) (*CommandCard, error)
	Delete(ctx context.Context, cardKey string) error
	InsertVersion(ctx context.Context, version CommandCardVersion) error
	ListVersions(ctx context.Context, cardKey string) ([]CommandCardVersion, error)
	Upsert(ctx context.Context, card CommandCard) (*CommandCard, error)
}

// CommandCardVersion 是命令卡片历史版本记录。
type CommandCardVersion struct {
	ID              int64
	CardKey         string
	Title           string
	Description     string
	CommandTemplate string
	ArgsSchema      json.RawMessage
	RiskLevel       string
	Enabled         bool
	CreatedBy       string
	UpdatedBy       string
	SourceUpdatedAt *time.Time
	CreatedAt       time.Time
	ArchivedAt      time.Time
}
