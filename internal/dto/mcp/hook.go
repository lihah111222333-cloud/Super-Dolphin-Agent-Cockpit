package mcp

import (
	"encoding/json"
	"time"
)

// ----- Hook Payload -----

// HookPayload 是发送给 hook 处理方的公共信封，包含调用上下文和主题信息。
type HookPayload struct {
	HookCallID string          `json:"hook_call_id"`          // 本次 hook 调用的唯一标识。
	DeadlineMs int64           `json:"deadline_ms,omitempty"` // 处理截止时间（Unix 毫秒），0 表示无限制。
	AgentID    string          `json:"agent_id"`
	ThreadID   string          `json:"thread_id"`
	TurnID     string          `json:"turn_id,omitempty"`
	Topic      string          `json:"topic"`           // hook 主题，与订阅时的 topics 对应。
	Depth      int             `json:"depth,omitempty"` // 嵌套深度，防止递归 hook 死循环。
	Context    json.RawMessage `json:"context,omitempty"`
}

// ----- Hook Decisions -----

// BeforeDecision 是 before 阶段 hook 派发的决策结果。
type BeforeDecision struct {
	Decision     string          `json:"decision"`                // 见 HookDecision* 常量。
	Patch        json.RawMessage `json:"patch,omitempty"`         // 对请求载荷的修改补丁。
	Mutations    json.RawMessage `json:"mutations,omitempty"`     // 结构化变更集合。
	AllowedTools []string        `json:"allowed_tools,omitempty"` // 白名单工具列表。
	DeniedTools  []string        `json:"denied_tools,omitempty"`  // 黑名单工具列表。
	Mode         string          `json:"mode,omitempty"`
	RetryAfterMs int64           `json:"retry_after_ms,omitempty"` // wait 决策时建议重试的等待毫秒数。
	Reason       string          `json:"reason,omitempty"`
}

// CheckDecision 是 check 阶段 hook 派发的决策结果。
type CheckDecision struct {
	Decision string `json:"decision"` // 见 HookDecision* 常量。
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// AfterDecision 是 after 阶段 hook 派发的决策结果。
type AfterDecision struct {
	Decision  string          `json:"decision"` // 见 HookDecision* 常量。
	Patch     json.RawMessage `json:"patch,omitempty"`
	Mutations json.RawMessage `json:"mutations,omitempty"`
	// DispatchIntent 保留兼容性；协议上该字段属于 mutations.dispatch_intent。
	DispatchIntent json.RawMessage `json:"dispatch_intent,omitempty"`
	TTLMs          int64           `json:"ttl_ms,omitempty"` // 决策缓存有效期（毫秒）。
	Reason         string          `json:"reason,omitempty"`
}

// ----- Hook Subscribe -----

// HookSubscribeRequest 是 ctl/hook/subscribe 的请求载荷，peer 用它注册感兴趣的 hook 主题。
type HookSubscribeRequest struct {
	SubscriptionID string          `json:"subscription_id"`
	Topics         []string        `json:"topics"`
	Scope          Selector        `json:"scope,omitzero"`
	Filters        json.RawMessage `json:"filters,omitempty"` // 协议要求的 hook 过滤条件。
	Mode           string          `json:"mode,omitempty"`
}

// HookSubscribeResponse 是 ctl/hook/subscribe 的响应，包含实际生效的主题和 scope。
type HookSubscribeResponse struct {
	Accepted            bool     `json:"accepted"`
	SubscriptionVersion int64    `json:"subscription_version,omitempty"`
	EffectiveTopics     []string `json:"effective_topics"`
	EffectiveScope      Selector `json:"effective_scope,omitzero"`
}

// ----- Hook Resolve -----

// HookResolveRequest 是 ctl/hook/resolve 的请求载荷，用于提交人工或自动审批结论。
type HookResolveRequest struct {
	HookCallID     string `json:"hook_call_id"`
	Decision       string `json:"decision"` // 见 HookDecision* 常量。
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotency_key"` // 防止重复提交同一决策。
	ResolvedBy     string `json:"resolved_by,omitempty"`
}

// HookResolveResponse 是 ctl/hook/resolve 的响应载荷。
type HookResolveResponse struct {
	Accepted          bool   `json:"accepted"`
	ResolvedAt        string `json:"resolved_at,omitempty"`
	CanonicalDecision string `json:"canonical_decision,omitempty"`
	PendingState      string `json:"pending_state,omitempty"`
}

// HookPendingCursor 是 ctl/hook/pending 的 keyset cursor。
type HookPendingCursor struct {
	CreatedAt  time.Time `json:"created_at"`
	HookCallID string    `json:"hook_call_id"`
}

// HookPendingRequest 是 ctl/hook/pending 的请求载荷，查询指定 agent 待处理的 hook。
type HookPendingRequest struct {
	AgentID string             `json:"agent_id,omitempty"`
	Limit   int                `json:"limit"`
	Cursor  *HookPendingCursor `json:"cursor,omitempty"`
}

// HookPendingResponse 是 ctl/hook/pending 的有界分页响应。
type HookPendingResponse struct {
	Reviews    []PendingHookReview `json:"reviews"`
	Limit      int                 `json:"limit"`
	HasMore    bool                `json:"has_more"`
	NextCursor *HookPendingCursor  `json:"next_cursor,omitempty"`
}

// ----- Pending Hook Review -----

// PendingHookReview 表示一个正在等待人工审核的 hook 调用记录。
type PendingHookReview struct {
	HookCallID      string          `json:"hook_call_id"`
	Topic           string          `json:"topic"`
	AgentID         string          `json:"agent_id"`
	ThreadID        string          `json:"thread_id"`
	TurnID          string          `json:"turn_id,omitempty"`
	SubscriberLease string          `json:"subscriber_lease,omitempty"`
	Payload         json.RawMessage `json:"payload"`
	CreatedAt       time.Time       `json:"created_at"`
	DeadlineAt      time.Time       `json:"deadline_at"` // 超过此时间后服务端会按 DefaultAction 自动决策。
	DefaultAction   string          `json:"default_action"`
}
