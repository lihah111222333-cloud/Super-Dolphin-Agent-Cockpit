package thread

import (
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

// Started 报告 thread 已变为可路由状态。
// PendingLaunch=true 时仅持久化占位 thread；首次 turn 触发 SpawnIfNeeded 后才启动 provider CLI。
type Started struct {
	shared.EventHeader
	ThreadID         string              `json:"thread_id"`
	AgentID          string              `json:"agent_id,omitempty"`
	Provider         string              `json:"provider,omitempty"`
	ProviderThreadID string              `json:"provider_thread_id,omitempty"`
	CWD              string              `json:"cwd,omitempty"`
	Model            string              `json:"model,omitempty"`
	Name             string              `json:"name,omitempty"`
	PendingLaunch    bool                `json:"pending_launch,omitempty"`
	Board            *agentdto.BoardView `json:"board,omitempty"`
}

// Launched 报告 pending_launch thread 已成功启动 provider CLI，并携带启动时的路由结果。
type Launched struct {
	shared.EventHeader
	ThreadID         string `json:"thread_id"`
	AgentID          string `json:"agent_id,omitempty"`
	Provider         string `json:"provider,omitempty"`
	ProviderThreadID string `json:"provider_thread_id,omitempty"`
	CWD              string `json:"cwd,omitempty"`
	Model            string `json:"model,omitempty"`
	Name             string `json:"name,omitempty"`
	AgentKey         string `json:"agent_key,omitempty"`
	PromptVersionID  *int64 `json:"prompt_version_id,omitempty"`
}

// Stopped 报告 thread 已进入非活动状态，Status/Reason 给出停止原因。
type Stopped struct {
	shared.EventHeader
	ThreadID string `json:"thread_id"`
	AgentID  string `json:"agent_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// MessagesPage 报告 thread 消息分页刷新，供 UI 同步总数和页数。
type MessagesPage struct {
	shared.EventHeader
	ThreadID   string `json:"thread_id"`
	TotalCount int    `json:"total_count"`
	Pages      int    `json:"pages"`
}

// Compacted 报告 thread 压缩流程完成，包含压缩前后的 token 估算。
type Compacted struct {
	shared.EventHeader
	ThreadID     string `json:"thread_id"`
	Command      string `json:"command,omitempty"`
	BeforeTokens int    `json:"before_tokens,omitempty"`
	AfterTokens  int    `json:"after_tokens,omitempty"`
	Compacted    bool   `json:"compacted"`
	Estimated    bool   `json:"estimated,omitempty"`
}

// Updated 报告 thread 元数据变更，例如名称或模型变化。
type Updated struct {
	shared.EventHeader
	ThreadID string  `json:"thread_id"`
	Name     string  `json:"name"`
	Model    *string `json:"model,omitempty"`
}

// SpawnRouting 承载 pending-launch thread 在惰性 SpawnIfNeeded 路径里的路由决策。
// 放在共享 DTO 包是为了让 thread 生产方和 turn/start 转发方共用类型，避免重新引入 thread 与 turn 的循环依赖。
//
// 空值表示 SpawnIfNeeded 没有启动新进程；非空表示刚完成一次新 spawn，UI 用它补齐 thread/start 无法提前给出的路由徽标。
type SpawnRouting struct {
	AgentKey string `json:"agent_key,omitempty"`
	// AgentTitle 是可直接展示的人格名称，UI 不需要再用 slug 反查名称。
	AgentTitle      string `json:"agent_title,omitempty"`
	PromptKey       string `json:"prompt_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	PromptKeyStale  bool   `json:"prompt_key_stale,omitempty"`
}

// Type 返回事件总线使用的稳定类型编号，保持 thread started 事件可路由。
func (Started) Type() uint32 { return shared.EventTypeThreadStarted }

// Type 返回事件总线使用的稳定类型编号，保持 thread stopped 事件可路由。
func (Stopped) Type() uint32 { return shared.EventTypeThreadStopped }

// Type 返回事件总线使用的稳定类型编号，保持消息分页事件可路由。
func (MessagesPage) Type() uint32 { return shared.EventTypeThreadMessagesPage }

// Type 返回事件总线使用的稳定类型编号，保持压缩完成事件可路由。
func (Compacted) Type() uint32 { return shared.EventTypeThreadCompacted }

// Type 返回事件总线使用的稳定类型编号，保持 thread updated 事件可路由。
func (Updated) Type() uint32 { return shared.EventTypeThreadUpdated }

// Type 返回事件总线使用的稳定类型编号，保持 lazy launch 完成事件可路由。
func (Launched) Type() uint32 { return shared.EventTypeThreadLaunched }
