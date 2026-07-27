// Package binding 持久化 agent、provider thread、Codex thread 和工作目录之间的绑定关系。
package binding

import (
	"context"
)

// Store 定义 session 绑定的持久化边界，供 provider 恢复、thread 查询和 orchestration 清理共用。
type Store interface {
	GetByProviderThread(ctx context.Context, provider, providerThreadID string) (*Binding, error)
	Upsert(ctx context.Context, params UpsertParams) error
	DeleteByAgentID(ctx context.Context, agentID string) error
	UpdateSessionUUID(ctx context.Context, params UpdateSessionUUIDParams) error
	UpdateProviderThreadID(ctx context.Context, params UpdateProviderThreadIDParams) error
	GetByAgentID(ctx context.Context, agentID string) (*Binding, error)
	BindAgentThread(ctx context.Context, params BindAgentThreadParams) error
	UnbindAgentThread(ctx context.Context, agentID string) error
	ListAgentThreadBindings(ctx context.Context) ([]Binding, error)
	GetThreadByAgent(ctx context.Context, agentID string) (string, error)
	UpdateAgentCwd(ctx context.Context, params UpdateAgentCwdParams) error
	Rebind(ctx context.Context, params RebindParams) error
	ListProviderMap(ctx context.Context) (map[string]string, error)
	ListCwdMap(ctx context.Context) (map[string]string, error)
}

// RebindParams 表示 agent 重新绑定到 thread 和 cwd 的原子更新输入。
type RebindParams struct {
	AgentID   string
	ThreadID  string
	Cwd       string
	UpdatedAt int64
}

// UpsertParams 是 provider session 绑定的写入输入，Codex 身份字段用于重启后的精确恢复。
type UpsertParams struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	RolloutPath      string
	SessionUUID      string
	Cwd              string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	CreatedAt        int64
	UpdatedAt        int64

	// Codex 身份字段为空时表示 upsert 保留已有值；非空修复必须由调用方确认仍是同一实例元组。
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}

// UpdateSessionUUIDParams 更新 provider session UUID，供历史恢复路径补齐线程身份。
type UpdateSessionUUIDParams struct {
	SessionUUID string
	UpdatedAt   int64
	AgentID     string
}

// UpdateProviderThreadIDParams 更新 provider thread ID，供 provider 恢复后回填真实线程。
type UpdateProviderThreadIDParams struct {
	ProviderThreadID string
	UpdatedAt        int64
	AgentID          string
}

// BindAgentThreadParams 记录 agent 到公共 thread 和 cwd 的绑定，供跨模块查找使用。
type BindAgentThreadParams struct {
	AgentID   string
	ThreadID  string
	Cwd       string
	CreatedAt int64
	UpdatedAt int64
}

// UpdateAgentCwdParams 更新 agent 当前工作目录，保持 session 恢复使用的 cwd 最新。
type UpdateAgentCwdParams struct {
	AgentID   string
	Cwd       string
	UpdatedAt int64
}

// Binding 是绑定表的领域 DTO，跨 provider/session/thread 模块传递恢复所需身份。
type Binding struct {
	AgentID          string
	Provider         string
	ProviderThreadID string
	CodexThreadID    string
	RolloutPath      string
	Cwd              string
	ParentAgentID    string
	AgentType        string
	AgentMemoryScope string
	Archived         bool
	CreatedAt        int64
	UpdatedAt        int64
	SessionUUID      string

	// Codex 身份字段保存规范化 home、实例 key 和模型 provider，auto-resume 用它路由回正确本地进程。
	CodexHome          string
	CodexInstanceKey   string
	CodexModelProvider string
}
