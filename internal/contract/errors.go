package contract

import (
	"context"
	"database/sql"
	"errors"
)

var (
	ErrSessionNotFound = errors.New("session not found")
)

// ErrSkillMissingCWD 是 skill 模块 cwd 缺失错误的跨层哨兵。
// platform/toolbridge 等低层消费者用 errors.Is 匹配它，不需要导入 skill 模块。
var ErrSkillMissingCWD = errors.New("cwd is required")

// ErrSkillSameNameConflict 表示多个 canonical skill 同名且没有显式策略选择单一来源。
var ErrSkillSameNameConflict = errors.New("skill same-name conflict")

// ErrSkillRefIdentityMismatch 表示结构化 skill 引用无法精确匹配 canonical inventory。
// 调用方不得将该错误降级为 name-only 查找，否则可能切换到不同 scope 或 owner。
var ErrSkillRefIdentityMismatch = errors.New("skill ref identity mismatch")

// SkillApprovalRequiredError 表示 skill artifact 执行前需要用户审批。
// Request 保留审批载荷，调用方据此展示审批 UI 并阻断执行。
type SkillApprovalRequiredError struct {
	Request ApprovalRequest
}

var errSkillApprovalRequired = errors.New("skill artifact approval required")

// Error 返回稳定的 skill 审批错误文本。
func (e SkillApprovalRequiredError) Error() string {
	return errSkillApprovalRequired.Error()
}

// Unwrap 暴露审批哨兵，允许 errors.Is 识别该错误类别。
func (e SkillApprovalRequiredError) Unwrap() error { return errSkillApprovalRequired }

// 存储层哨兵错误由 contract 暴露给 module 层，避免 module 直接依赖 platform/db。
var (
	ErrNotFound = errors.New("store: not found")
	ErrConflict = errors.New("store: conflict")
)

// IsNotFound 判断错误链是否匹配存储层 not-found 哨兵。
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows)
}

// ErrThreadRuntimeRequired 表示 toolbridge 调用无法解析 thread runtime。
// 需要 thread 身份才能应用 per-thread 策略的工具必须返回该哨兵而不是静默降级。
var ErrThreadRuntimeRequired = errors.New("toolbridge: thread runtime is required")

// ErrPersistentSubagentRuntimeRequired 表示 thread 存在但缺少 runtime config。
// 这种情况下 persistent_subagent_default 无法求值，调用方必须阻断策略判断。
var ErrPersistentSubagentRuntimeRequired = errors.New("toolbridge: persistent subagent runtime is required")

// ErrPersistentSubagentFlagRequired 表示 runtime config 未显式携带 persistent-subagent 标志。
// spawn_agent 策略检查用它区分“缺少配置”与“显式关闭”。
var ErrPersistentSubagentFlagRequired = errors.New("toolbridge: persistent subagent flag is required")

// CacheKeepaliveBinding 是 keepalive 判断 agent 是否仍可 ping 的最小绑定快照。
type CacheKeepaliveBinding struct {
	AgentID  string
	Archived bool
}

// CacheKeepaliveThreadRef 是启动事件缺少 agentID 时用于回查的最小线程引用。
type CacheKeepaliveThreadRef struct {
	ThreadID string
	AgentID  string
}

// CacheKeepaliveBindingLookup 隔离 cache keepalive 对 binding store 的只读需求。
type CacheKeepaliveBindingLookup interface {
	GetCacheKeepaliveBindingByAgentID(ctx context.Context, agentID string) (*CacheKeepaliveBinding, error)
}

// CacheKeepaliveThreadLookup 隔离 cache keepalive 对 thread store 的只读需求。
type CacheKeepaliveThreadLookup interface {
	GetCacheKeepaliveThreadByID(ctx context.Context, threadID string) (*CacheKeepaliveThreadRef, error)
}
