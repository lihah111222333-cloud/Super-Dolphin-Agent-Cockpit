package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// ---------------------------------------------------------------------------
// Handler types
// ---------------------------------------------------------------------------

// HookBeforeHandler 处理核心层发来的 ctl/hook/before 回调，并返回执行前决策。
type HookBeforeHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.BeforeDecision, error)

// HookCheckHandler 处理核心层发来的 ctl/hook/check 回调，并返回中途检查决策。
type HookCheckHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.CheckDecision, error)

// HookAfterHandler 处理核心层发来的 ctl/hook/after 回调，并返回收尾决策。
type HookAfterHandler func(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error)

// ---------------------------------------------------------------------------
// HookConfig — tool-side hook configuration
// ---------------------------------------------------------------------------

// HookConfig 保存工具侧注册的 hook handler；nil handler 会触发各阶段的默认安全决策。
type HookConfig struct {
	OnBefore HookBeforeHandler
	OnCheck  HookCheckHandler
	OnAfter  HookAfterHandler
}

// hookState 存储最近一次 SubscribeHooks 的参数，供断线重连后重放。
type hookState struct {
	mu             sync.Mutex
	subscriptionID string
	topics         []string
	scope          mcp.Selector
	filters        json.RawMessage
	mode           string
	replayState    string
	replayAttempts int
	lastReplayErr  string
}

// store 保存订阅参数，重置重放状态，供后续断线重连时使用。
func (hs *hookState) store(subID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.subscriptionID = subID
	hs.topics = shared.CloneStrings(topics)
	hs.scope = shared.CloneSelector(scope)
	hs.filters = shared.CloneRawMessage(filters)
	hs.mode = mode
	hs.replayState = ""
	hs.replayAttempts = 0
	hs.lastReplayErr = ""
}

// load 读取上次保存的订阅参数；topics 为空时返回 ok=false。
func (hs *hookState) load() (subID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string, ok bool) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if len(hs.topics) == 0 {
		return "", nil, mcp.Selector{}, nil, "", false
	}
	return hs.subscriptionID, shared.CloneStrings(hs.topics), shared.CloneSelector(hs.scope), shared.CloneRawMessage(hs.filters), hs.mode, true
}

// markReplayFailure 记录重放失败的尝试次数和错误。
func (hs *hookState) markReplayFailure(attempts int, err error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.replayState = "failed"
	hs.replayAttempts = attempts
	if err != nil {
		hs.lastReplayErr = err.Error()
	} else {
		hs.lastReplayErr = ""
	}
}

// markReplayPending 记录 live SubscribeHooks 失败但期望订阅状态已持久化。
// 初始错误会保留给诊断展示，避免等到下一次 reconnect 重放后才知道首个失败原因。
func (hs *hookState) markReplayPending(err error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.replayState = "pending"
	hs.replayAttempts = 0
	if err != nil {
		hs.lastReplayErr = err.Error()
	} else {
		hs.lastReplayErr = ""
	}
}

// clearReplayFailure 清除重放失败状态，订阅成功后调用。
func (hs *hookState) clearReplayFailure() {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.replayState = ""
	hs.replayAttempts = 0
	hs.lastReplayErr = ""
}

// ---------------------------------------------------------------------------
// Callback dispatch — invoked from dispatchRequest
// ---------------------------------------------------------------------------

// dispatchHookCallback 将 hook 回调路由到 before/check/after handler。
// handled=false 表示该方法不属于 hook，调用方应继续后续 dispatch。
func (c *Client) dispatchHookCallback(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	switch req.Method() {
	case mcp.MethodHookBefore:
		return c.handleHookBefore(ctx, req)
	case mcp.MethodHookCheck:
		return c.handleHookCheck(ctx, req)
	case mcp.MethodHookAfter:
		return c.handleHookAfter(ctx, req)
	default:
		return nil, false, nil
	}
}

// handleHookBefore 处理 ctl/hook/before 回调，未注册时拒绝。
func (c *Client) handleHookBefore(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnBefore
	if handler == nil {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = shared.CloneRawMessage(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

// handleHookCheck 处理 ctl/hook/check 回调，未注册时继续。
func (c *Client) handleHookCheck(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnCheck
	if handler == nil {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = shared.CloneRawMessage(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

// handleHookAfter 处理 ctl/hook/after 回调，未注册时拒绝。
func (c *Client) handleHookAfter(ctx context.Context, req *jrpc2.Request) (any, bool, error) {
	handler := c.cfg.Hooks.OnAfter
	if handler == nil {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}, true, nil
	}
	var payload mcp.HookPayload
	if err := req.UnmarshalParams(&payload); err != nil {
		return nil, true, err
	}
	payload.Context = shared.CloneRawMessage(payload.Context)
	dec, err := handler(ctx, payload)
	return dec, true, err
}

// ---------------------------------------------------------------------------
// SubscribeHooks — outgoing RPC to core layer
// ---------------------------------------------------------------------------

// SubscribeHooks 向控制平面注册 hook 订阅，并持久化期望订阅状态。
// 连接不可用或 live 调用失败时也会保留订阅参数，下一次 reconnect 可自动重放；调用方仍收到原始错误。
func (c *Client) SubscribeHooks(ctx context.Context, subscriptionID string, topics []string, scope mcp.Selector, filters json.RawMessage, mode string) (*mcp.HookSubscribeResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		// 即使当前无法发送，也要保存期望状态，保证 reconnect 后可重放。
		c.hooks.store(subscriptionID, topics, scope, filters, mode)
		return nil, errHookSubscribeUnavailable()
	}

	req := mcp.HookSubscribeRequest{
		SubscriptionID: strings.TrimSpace(subscriptionID),
		Topics:         shared.CloneStrings(topics),
		Scope:          scope,
		Filters:        shared.CloneRawMessage(filters),
		Mode:           strings.TrimSpace(mode),
	}
	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookSubscribeResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookSubscribe, req, &resp); err != nil {
		// live 调用失败也保留订阅参数；pending 状态让诊断区分首次失败与重连重放失败。
		c.hooks.store(subscriptionID, topics, scope, filters, mode)
		c.hooks.markReplayPending(err)
		return nil, err
	}

	// 成功订阅后仍保存参数，连接恢复时沿用同一份期望状态。
	c.hooks.store(subscriptionID, topics, scope, filters, mode)
	return &resp, nil
}

// ResolveHook 将人工审批结果回写到控制平面，连接不可用时 fail-fast。
func (c *Client) ResolveHook(ctx context.Context, req mcp.HookResolveRequest) (*mcp.HookResolveResponse, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return nil, errHookResolveUnavailable()
	}

	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookResolveResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookResolve, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// PendingHooks 拉取当前 agent 待处理的 hook review，缺少 authoritative agent_id 时拒绝读取。
func (c *Client) PendingHooks(ctx context.Context) ([]mcp.PendingHookReview, error) {
	conn, degraded := c.currentConn()
	if conn == nil || degraded {
		return nil, errHookPendingUnavailable()
	}
	// c.cfg.AgentID 是唯一可信身份；boot snapshot 只用于诊断，不能作为读取 pending review 的身份兜底。
	agentID := strings.TrimSpace(c.cfg.AgentID)
	if agentID == "" {
		return nil, errHookPendingAgentIDRequired()
	}

	callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
	defer cancel()

	var resp mcp.HookPendingResponse
	if err := conn.CallResult(callCtx, mcp.MethodHookPending, mcp.HookPendingRequest{AgentID: agentID}, &resp); err != nil {
		return nil, err
	}
	if len(resp.Reviews) == 0 {
		return []mcp.PendingHookReview{}, nil
	}
	return append([]mcp.PendingHookReview(nil), resp.Reviews...), nil
}

// replayHookSubscriptions 在 reconnect 成功后重放最近一次 hook 订阅。
// 没有历史订阅时直接返回；重放失败会记录 attempts 和 last error 供诊断面展示。
func (c *Client) replayHookSubscriptions(ctx context.Context) error {
	subID, topics, scope, filters, mode, ok := c.hooks.load()
	if !ok {
		return nil
	}
	// event 字段是观测侧的稳定锚点，dashboard 依赖它统计 hook replay 起止。
	pkglogger.Info("bootstrap hook replay begin",
		"event", "bootstrap.hook_replay.begin",
		"instance_id", c.instanceID,
		"subscription_id", subID,
		"lease_key", c.currentLease(),
	)

	req := mcp.HookSubscribeRequest{
		SubscriptionID: subID,
		Topics:         topics,
		Scope:          scope,
		Filters:        filters,
		Mode:           mode,
	}
	ctx = defaultContext(ctx)
	delay := time.Second
	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= 3; attempt++ {
		attempts = attempt
		conn, degraded := c.currentConn()
		if conn == nil || degraded {
			lastErr = errHookSubscribeUnavailable()
		} else {
			callCtx, cancel := withTimeoutIfNone(ctx, c.currentSendTimeout())
			var resp mcp.HookSubscribeResponse
			lastErr = conn.CallResult(callCtx, mcp.MethodHookSubscribe, req, &resp)
			cancel()
			if lastErr == nil {
				c.hooks.clearReplayFailure()
				// 成功结束也使用同一 event 名称，通过 outcome 区分结果。
				pkglogger.Info("bootstrap hook subscription replayed",
					"event", "bootstrap.hook_replay.end",
					"outcome", "success",
					"instance_id", c.instanceID,
					"subscription_id", subID,
					"lease_key", c.currentLease(),
					"attempts", attempt,
				)
				return nil
			}
		}
		if attempt == 3 || ctx.Err() != nil {
			break
		}
		pkglogger.Warn("bootstrap hook subscription replay failed; retrying",
			"instance_id", c.instanceID,
			"subscription_id", subID,
			"lease_key", c.currentLease(),
			"attempt", attempt,
			"retry_in", delay,
			"error", lastErr,
		)
		if !sleepContext(ctx, delay) {
			lastErr = ctx.Err()
			break
		}
		delay *= 2
	}
	c.hooks.markReplayFailure(attempts, lastErr)
	// 终态失败必须与 begin 成对打点，避免重连恢复问题只留下自由文本日志。
	pkglogger.Error("bootstrap hook subscription replay failed",
		"event", "bootstrap.hook_replay.end",
		"outcome", "failed",
		"instance_id", c.instanceID,
		"subscription_id", subID,
		"lease_key", c.currentLease(),
		"attempts", attempts,
		"replay_state", "failed",
		"error", lastErr,
	)
	return lastErr
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// errHookSubscribeUnavailable 返回 hook subscribe 连接不可用错误，供重放路径识别。
func errHookSubscribeUnavailable() error {
	return &hookUnavailableError{msg: "bootstrap: hook subscribe unavailable (disconnected)"}
}

// errHookResolveUnavailable 返回 hook resolve 连接不可用错误。
func errHookResolveUnavailable() error {
	return &hookUnavailableError{msg: "bootstrap: hook resolve unavailable (disconnected)"}
}

// errHookPendingUnavailable 返回 pending hook 查询连接不可用错误。
func errHookPendingUnavailable() error {
	return &hookUnavailableError{msg: "bootstrap: hook pending unavailable (disconnected)"}
}

// errHookPendingAgentIDRequired 在缺少 authoritative agent_id 时 fail-closed。
func errHookPendingAgentIDRequired() error {
	return errors.New("bootstrap: hook pending requires agent_id")
}

// hookUnavailableError 标记 hook 连接不可用的非致命错误。
type hookUnavailableError struct{ msg string }

// Error 返回 hook unavailable 的可读错误文本。
func (e *hookUnavailableError) Error() string { return e.msg }
