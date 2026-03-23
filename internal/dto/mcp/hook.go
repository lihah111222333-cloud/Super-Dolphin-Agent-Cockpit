package mcp

import (
	"encoding/json"
	"time"
)

// --- Hook Payload ---

// HookPayload is the common envelope sent to hook handlers.
type HookPayload struct {
	HookCallID string          `json:"hook_call_id"`
	AgentID    string          `json:"agent_id"`
	ThreadID   string          `json:"thread_id"`
	TurnID     string          `json:"turn_id,omitempty"`
	Topic      string          `json:"topic"`
	DeadlineMs int64           `json:"deadline_ms,omitempty"`
	Context    json.RawMessage `json:"context,omitempty"`
}

// --- Hook Decisions ---

// BeforeDecision is the result of a before-phase hook dispatch.
type BeforeDecision struct {
	Decision     string          `json:"decision"`
	Patch        json.RawMessage `json:"patch,omitempty"`
	Mutations    json.RawMessage `json:"mutations,omitempty"`
	AllowedTools []string        `json:"allowed_tools,omitempty"`
	DeniedTools  []string        `json:"denied_tools,omitempty"`
	Mode         string          `json:"mode,omitempty"`
	RetryAfterMs int64           `json:"retry_after_ms,omitempty"`
	Reason       string          `json:"reason,omitempty"`
}

// CheckDecision is the result of a check-phase hook dispatch.
type CheckDecision struct {
	Decision string `json:"decision"`
	Severity string `json:"severity,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// AfterDecision is the result of an after-phase hook dispatch.
type AfterDecision struct {
	Decision  string          `json:"decision"`
	Patch     json.RawMessage `json:"patch,omitempty"`
	Mutations json.RawMessage `json:"mutations,omitempty"`
	// DispatchIntent is retained for compatibility; in the protocol it belongs to mutations.dispatch_intent.
	DispatchIntent json.RawMessage `json:"dispatch_intent,omitempty"`
	Reason         string          `json:"reason,omitempty"`
}

// --- Hook Subscribe ---

// HookSubscribeRequest is the request payload for ctl/hook/subscribe.
type HookSubscribeRequest struct {
	SubscriptionID string          `json:"subscription_id"`
	Topics         []string        `json:"topics"`
	Scope          Selector        `json:"scope,omitempty"`
	Filters        json.RawMessage `json:"filters,omitempty"` // 协议要求的 hook 过滤条件
	Mode           string          `json:"mode,omitempty"`
}

// HookSubscribeResponse is the response for ctl/hook/subscribe.
type HookSubscribeResponse struct {
	Accepted            bool     `json:"accepted"`
	SubscriptionVersion int64    `json:"subscription_version,omitempty"`
	EffectiveTopics     []string `json:"effective_topics"`
	EffectiveScope      Selector `json:"effective_scope,omitempty"`
}

// --- Hook Resolve ---

// HookResolveRequest is the request payload for ctl/hook/resolve.
type HookResolveRequest struct {
	HookCallID     string `json:"hook_call_id"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

// HookResolveResponse is the response payload for ctl/hook/resolve.
type HookResolveResponse struct {
	Accepted          bool   `json:"accepted"`
	ResolvedAt        string `json:"resolved_at,omitempty"`
	CanonicalDecision string `json:"canonical_decision,omitempty"`
	PendingState      string `json:"pending_state,omitempty"`
}

// --- Pending Hook Review ---

// PendingHookReview represents a hook call awaiting human review.
type PendingHookReview struct {
	HookCallID    string    `json:"hook_call_id"`
	Topic         string    `json:"topic"`
	AgentID       string    `json:"agent_id"`
	CreatedAt     time.Time `json:"created_at"`
	DeadlineAt    time.Time `json:"deadline_at"`
	DefaultAction string    `json:"default_action"`
}
