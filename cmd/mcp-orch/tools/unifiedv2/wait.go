// Package unifiedv2 contains disabled-by-composition candidates for the strict unified_v2 surface.
package unifiedv2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/waitaggregate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	toolspkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// ErrWaitAgentsDisabled 表示 wait_agents 尚未由 composition 装配。
var ErrWaitAgentsDisabled = errors.New("unified_v2 wait_agents is disabled")

// ErrWaitAgentsUnavailable 表示 wait_agents 调用缺少可信 task 身份或归因。
var ErrWaitAgentsUnavailable = errors.New("unified_v2 wait_agents is unavailable")

// ErrUnifiedDisabled 保留旧候选调用方的别名；新代码应使用工具专属错误。
var ErrUnifiedDisabled = ErrWaitAgentsDisabled

// ErrUnifiedUnavailable 保留旧候选调用方的别名；新代码应使用工具专属错误。
var ErrUnifiedUnavailable = ErrWaitAgentsUnavailable

// WaitTarget 是 caller-authorized target wait 句柄；Key 仅为 opaque handle，不是身份权威。
type WaitTarget struct {
	// Key 是不透明 wait handle 标识。
	Key string `json:"key"`
	// GrantID 是 owner 校验的授权标识。
	GrantID string `json:"grant_id"`
	// GrantRevision 是授权修订版本。
	GrantRevision int64 `json:"grant_revision"`
	// Selector 是固定事件源选择器。
	Selector accesswait.EventSelector `json:"selector"`
	// AfterSequence 是客户端已观察的源序列。
	AfterSequence int64 `json:"after_sequence"`
	// AfterCursor 是客户端已观察的 canonical cursor。
	AfterCursor string `json:"after_cursor,omitempty"`
}

// Target 是 wait_agents 合同使用的简洁别名。
type Target = WaitTarget

// WaitAgentsInput 是 wait_agents 严格 JSON 入参。
type WaitAgentsInput struct {
	// Targets 是有界目标集合。
	Targets []WaitTarget `json:"targets"`
	// Condition 是聚合条件。
	Condition             waitaggregate.Condition `json:"condition"`
	QuorumCount           int                     `json:"quorum_count,omitempty"`
	Predicate             waitaggregate.Predicate `json:"predicate,omitempty"`
	TimeoutMS             *int                    `json:"timeout_ms,omitempty"`
	Deadline              *string                 `json:"deadline,omitempty"`
	ReturnOnIndeterminate bool                    `json:"return_on_indeterminate,omitempty"`
}

// WaitItemError 是不会泄露底层实现的错误分类 DTO。
type WaitItemError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// EventSummary 仅返回 canonical event 元数据，绝不内联正文。
type EventSummary struct {
	EventID         string `json:"event_id"`
	EventType       string `json:"event_type"`
	SourceSequence  int64  `json:"source_sequence"`
	CanonicalCursor string `json:"canonical_cursor,omitempty"`
}

// WaitItem 是单目标安全结果摘要。
type WaitItem struct {
	TargetKey  string                    `json:"target_key"`
	Outcome    waitaggregate.ItemOutcome `json:"outcome"`
	Status     string                    `json:"status"`
	IsError    bool                      `json:"is_error"`
	NextCursor string                    `json:"next_cursor,omitempty"`
	Event      *EventSummary             `json:"event,omitempty"`
	Error      *WaitItemError            `json:"error,omitempty"`
}

// WaitAgentsOutput 是 wait_agents 的稳定 wire 返回，不含报告正文或 provider 数据。
type WaitAgentsOutput struct {
	Condition    waitaggregate.Condition `json:"condition"`
	Status       string                  `json:"status"`
	Items        []WaitItem              `json:"items"`
	WinnerKey    string                  `json:"winner_key,omitempty"`
	SuccessCount int                     `json:"success_count"`
	IsError      bool                    `json:"is_error"`
}

// WaitAgentsHandler 是故意不进入 legacy registry 的 unified_v2 候选处理器。
type WaitAgentsHandler struct {
	resolver *taskprincipal.Resolver
	waiter   grantWaiter
	clock    waitaggregate.Clock
}

type grantWaiter interface {
	waitaggregate.Waiter
	GetGrant(context.Context, string) (accesswait.AccessGrant, error)
}

// waitHandleReader 是 owner 的只读句柄端口。候选 wait 在进入 owner.Wait
// 之前必须通过该端口重新读取 durable handle，避免把 caller 提供的字段当作
// 查询授权或事件游标。
type waitHandleReader interface {
	GetWait(context.Context, accesswait.PollRequest) (accesswait.WaitHandle, error)
}

// New 构造未注册的 unified_v2 wait_agents 候选；仅为既有内部测试保留
// 非权威 wall-clock 兼容 seam。生产 candidate 接线必须使用 NewWithClock。
// Deprecated: candidate composition should use NewWithClock.
func New(resolver *taskprincipal.Resolver, owner any) (*WaitAgentsHandler, error) {
	return newHandler(resolver, owner, compatibilityClock)
}

// NewWithClock 构造绑定显式 security-time seam 的 wait_agents candidate。
// clock 缺失立即失败，不会隐式回退到本地 wall clock。
func NewWithClock(resolver *taskprincipal.Resolver, owner any, clock waitaggregate.Clock) (*WaitAgentsHandler, error) {
	return newHandler(resolver, owner, clock)
}

// newHandler 严格检查 resolver、durable wait owner、读句柄和安全时钟依赖。
func newHandler(resolver *taskprincipal.Resolver, owner any, clock waitaggregate.Clock) (*WaitAgentsHandler, error) {
	grantOwner, ok := owner.(grantWaiter)
	reader, readerOK := owner.(waitHandleReader)
	if resolver == nil || !ok || isNil(grantOwner) || !readerOK || isNil(reader) {
		return nil, ErrWaitAgentsDisabled
	}
	if clock == nil {
		return nil, fmt.Errorf("%w: %w", ErrWaitAgentsUnavailable, waitaggregate.ErrNilClock)
	}
	return &WaitAgentsHandler{resolver: resolver, waiter: grantOwner, clock: clock}, nil
}

// NewWaitAgents 按 resolver、wait owner 顺序构造候选处理器。
func NewWaitAgents(resolver *taskprincipal.Resolver, owner any) (*WaitAgentsHandler, error) {
	return New(resolver, owner)
}

// NewWaitAgentsWithClock 是 wait_agents candidate 的显式时钟构造别名。
func NewWaitAgentsWithClock(resolver *taskprincipal.Resolver, owner any, clock waitaggregate.Clock) (*WaitAgentsHandler, error) {
	return NewWithClock(resolver, owner, clock)
}

// NewWithOwner 为未来 Fx wiring 保留 owner-first 构造顺序。
func NewWithOwner(owner any, resolver *taskprincipal.Resolver) (*WaitAgentsHandler, error) {
	return New(resolver, owner)
}

// NewWithOwnerWithClock 是 owner-first 调用方的显式时钟构造别名。
func NewWithOwnerWithClock(owner any, resolver *taskprincipal.Resolver, clock waitaggregate.Clock) (*WaitAgentsHandler, error) {
	return NewWithClock(resolver, owner, clock)
}

// ToolHandler 返回 MCP 工具处理函数，不改变当前 registry。
func (h *WaitAgentsHandler) ToolHandler() toolspkg.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) { return h.Handle(ctx, raw) }
}

// Handle 执行严格解码、caller fence 校验和 eventstore wait 聚合。
func (h *WaitAgentsHandler) Handle(ctx context.Context, raw json.RawMessage) (any, error) {
	if !h.ready() {
		return nil, ErrWaitAgentsDisabled
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrWaitAgentsUnavailable)
	}
	principal, err := requireTaskPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	input, err := decodeInput(raw)
	if err != nil {
		return nil, err
	}
	task, source, callerFence, err := h.resolveTask(ctx)
	if err != nil {
		return nil, err
	}
	principal = task.Principal
	now := h.clock()
	if err := validateSecurityClock(now); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWaitAgentsUnavailable, err)
	}
	req, err := buildRequestAt(input, principal, source, now)
	if err != nil {
		return nil, err
	}
	guard := &validatingWaiter{waiter: h.waiter, targets: input.Targets, source: source, callerFence: callerFence}
	aggregator, err := waitaggregate.NewWithClock(guard, func() time.Time { return now })
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWaitAgentsUnavailable, err)
	}
	result, err := aggregator.WaitAt(ctx, req, now)
	if err != nil {
		return nil, err
	}
	return summarizeResult(result), nil
}

func (h *WaitAgentsHandler) ready() bool {
	return h != nil && h.resolver != nil && h.waiter != nil && !isNil(h.waiter) && h.clock != nil
}

func (h *WaitAgentsHandler) resolveTask(ctx context.Context) (taskprincipal.TaskPrincipal, taskeventsource.Source, accesswait.FenceSnapshot, error) {
	task, err := h.resolver.RequireCurrent(ctx)
	if err != nil {
		return taskprincipal.TaskPrincipal{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, fmt.Errorf("%w: %w", ErrWaitAgentsUnavailable, err)
	}
	source, err := taskeventsource.FromCurrentBinding(task.Binding)
	if err != nil {
		return taskprincipal.TaskPrincipal{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, fmt.Errorf("%w: %w", ErrWaitAgentsUnavailable, err)
	}
	fence := accesswait.FenceSnapshot{AgentID: task.Binding.AgentID, ReservationID: task.Binding.ReservationID, ReservationGeneration: task.Binding.ReservationGeneration, IncarnationID: task.Binding.IncarnationID, IncarnationGeneration: task.Binding.IncarnationGeneration, BindingVersion: task.Binding.BindingVersion, BindingDigest: task.Binding.BindingDigest, SourceFenceRef: task.Binding.SourceFenceRef}
	return task, source, fence, nil
}

// requireTaskPrincipal 先做 transport guard，保证 operator 调用零 owner 访问。
func requireTaskPrincipal(ctx context.Context) (common.CallerPrincipal, error) {
	principal, err := common.RequireCallerPrincipal(ctx)
	if err != nil {
		return common.CallerPrincipal{}, fmt.Errorf("%w: %w", ErrWaitAgentsUnavailable, err)
	}
	if strings.EqualFold(principal.Profile, "operator") {
		return common.CallerPrincipal{}, fmt.Errorf("%w: operator principal has no task attribution", ErrWaitAgentsUnavailable)
	}
	return principal, nil
}

// compatibilityClock 是旧构造器路径的非权威 wall-clock seam；candidate
// 接线必须从 NewWithClock 传入已认证 security-time owner observation。
func compatibilityClock() time.Time { return time.Now().UTC() }

func validateSecurityClock(now time.Time) error {
	if now.IsZero() || now.Location() != time.UTC {
		return waitaggregate.ErrInvalidClock
	}
	return nil
}

func buildRequest(in WaitAgentsInput, principal common.CallerPrincipal, source taskeventsource.Source) (waitaggregate.Request, error) {
	return buildRequestAt(in, principal, source, compatibilityClock())
}

func buildRequestAt(in WaitAgentsInput, principal common.CallerPrincipal, source taskeventsource.Source, now time.Time) (waitaggregate.Request, error) {
	if err := validateSecurityClock(now); err != nil {
		return waitaggregate.Request{}, err
	}
	deadline, err := in.deadline(now)
	if err != nil {
		return waitaggregate.Request{}, err
	}
	req := waitaggregate.Request{Condition: in.Condition, QuorumCount: in.QuorumCount, Predicate: in.Predicate, DeadlineAt: deadline, ReturnOnIndeterminate: in.ReturnOnIndeterminate, Targets: make([]waitaggregate.Target, len(in.Targets))}
	for i, target := range in.Targets {
		if err := validateTarget(target, source); err != nil {
			return waitaggregate.Request{}, err
		}
		req.Targets[i] = waitaggregate.Target{Key: target.Key, Poll: accesswait.PollRequest{WaitHandleID: target.Key, GrantRevision: target.GrantRevision, CallerPrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef}}
	}
	return req, nil
}

func summarizeResult(result waitaggregate.Result) WaitAgentsOutput {
	out := WaitAgentsOutput{Condition: result.Condition, Status: result.Status, WinnerKey: result.WinnerKey, SuccessCount: result.SuccessCount, IsError: result.IsError, Items: make([]WaitItem, len(result.Items))}
	for i, item := range result.Items {
		out.Items[i] = WaitItem{TargetKey: item.TargetKey, Outcome: item.Outcome, Status: item.Result.Status, IsError: item.Result.IsError, NextCursor: item.Result.NextCursor, Error: typedError(item.Err)}
		if item.Result.Event != nil {
			e := item.Result.Event
			out.Items[i].Event = &EventSummary{EventID: e.Envelope.EventID, EventType: e.Envelope.EventType, SourceSequence: e.SourceSequence, CanonicalCursor: e.CanonicalCursor}
		}
	}
	return out
}

func typedError(err error) *WaitItemError {
	if err == nil {
		return nil
	}
	code, message := "indeterminate", "wait outcome is indeterminate"
	if errors.Is(err, accesswait.ErrAccessDenied) {
		code, message = "access_denied", "access denied"
	}
	if errors.Is(err, accesswait.ErrWaitGap) {
		code, message = "wait_gap", "event source has a gap"
	}
	return &WaitItemError{Code: code, Message: message}
}

type validatingWaiter struct {
	waiter      grantWaiter
	targets     []WaitTarget
	source      taskeventsource.Source
	callerFence accesswait.FenceSnapshot
}

// Wait 调用 canonical accesswait owner，先校验完整结果合同，再校验句柄不可变绑定。
func (w *validatingWaiter) Wait(ctx context.Context, poll accesswait.PollRequest) (accesswait.WaitResult, error) {
	target, grant, err := w.prepareTarget(ctx, poll)
	if err != nil {
		return accesswait.WaitResult{}, err
	}
	result, err := w.waiter.Wait(ctx, poll)
	if err != nil {
		return accesswait.WaitResult{}, err
	}
	if err := validateWaitResult(result, target, w.source, grant.Target); err != nil {
		return accesswait.WaitResult{}, err
	}
	return result, nil
}

// prepareTarget 在 owner.Wait 前完成 durable handle、grant 与 fence 校验。
func (w *validatingWaiter) prepareTarget(ctx context.Context, poll accesswait.PollRequest) (WaitTarget, accesswait.AccessGrant, error) {
	idx := w.targetIndex(poll.WaitHandleID)
	if idx < 0 {
		return WaitTarget{}, accesswait.AccessGrant{}, fmt.Errorf("%w: unknown target key", waitaggregate.ErrInvalidRequest)
	}
	target := w.targets[idx]
	reader, ok := w.waiter.(waitHandleReader)
	if !ok {
		return WaitTarget{}, accesswait.AccessGrant{}, fmt.Errorf("%w: owner has no durable wait reader", ErrWaitAgentsUnavailable)
	}
	// GetWait 是第一个 owner 调用，仅执行只读认证；任何不匹配都在事件读取/CAS 前失败。
	handle, err := reader.GetWait(ctx, poll)
	if err != nil {
		return WaitTarget{}, accesswait.AccessGrant{}, err
	}
	if err := validateRequestedHandle(handle, poll, target, w.source); err != nil {
		return WaitTarget{}, accesswait.AccessGrant{}, err
	}
	grant, err := w.validateGrant(ctx, poll, target)
	if err != nil {
		return WaitTarget{}, accesswait.AccessGrant{}, err
	}
	if !matchesTarget(handle, target, w.source, grant.Target) {
		return WaitTarget{}, accesswait.AccessGrant{}, fmt.Errorf("%w: durable wait handle binding mismatch", accesswait.ErrAccessDenied)
	}
	return target, grant, nil
}

// validateWaitResult 校验 owner 返回的结果完整性及 immutable handle 绑定。
func validateWaitResult(result accesswait.WaitResult, target WaitTarget, source taskeventsource.Source, targetFence accesswait.FenceSnapshot) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("%w: malformed owner wait result: %v", ErrWaitAgentsUnavailable, err)
	}
	if result.Handle.WaitHandleID != target.Key || !matchesTarget(result.Handle, target, source, targetFence) {
		return fmt.Errorf("%w: wait handle binding mismatch", accesswait.ErrAccessDenied)
	}
	return nil
}

// validateRequestedHandle 比较 caller 请求与 durable wait handle 的不可变字段。
func validateRequestedHandle(handle accesswait.WaitHandle, poll accesswait.PollRequest, target WaitTarget, source taskeventsource.Source) error {
	if !requestedHandleIdentityMatches(handle, poll, target) || !requestedHandleSourceMatches(handle, target, source) {
		return fmt.Errorf("%w: requested target does not match durable wait handle", accesswait.ErrAccessDenied)
	}
	return nil
}

// requestedHandleIdentityMatches 比较 wait handle 的 ID、授权版本与 caller scope。
func requestedHandleIdentityMatches(handle accesswait.WaitHandle, poll accesswait.PollRequest, target WaitTarget) bool {
	return handle.WaitHandleID == poll.WaitHandleID && handle.GrantID == target.GrantID &&
		handle.GrantRevision == target.GrantRevision && handle.GrantRevision == poll.GrantRevision &&
		handle.CallerPrincipalRef == poll.CallerPrincipalRef && handle.ScopeRef == poll.ScopeRef
}

// requestedHandleSourceMatches 比较 handle 的 source、selector 与游标字段。
func requestedHandleSourceMatches(handle accesswait.WaitHandle, target WaitTarget, source taskeventsource.Source) bool {
	return handle.SourceAgentID == source.AgentID && reflect.DeepEqual(handle.Selector, target.Selector) &&
		handle.AfterSequence == target.AfterSequence && handle.AfterCursor == target.AfterCursor
}

func (w *validatingWaiter) targetIndex(key string) int {
	for i := range w.targets {
		if w.targets[i].Key == key {
			return i
		}
	}
	return -1
}

// validateGrant 在 owner.Wait 之前校验授权快照与 caller/source fence。
func (w *validatingWaiter) validateGrant(ctx context.Context, poll accesswait.PollRequest, target WaitTarget) (accesswait.AccessGrant, error) {
	grant, err := w.waiter.GetGrant(ctx, target.GrantID)
	if err != nil {
		return accesswait.AccessGrant{}, err
	}
	if err := grant.Validate(); err != nil {
		return accesswait.AccessGrant{}, fmt.Errorf("%w: malformed grant", accesswait.ErrAccessDenied)
	}
	if err := grant.Source.ValidateBound(); err != nil {
		return accesswait.AccessGrant{}, fmt.Errorf("%w: malformed source fence", accesswait.ErrAccessDenied)
	}
	if err := grant.Target.ValidateBound(); err != nil {
		return accesswait.AccessGrant{}, fmt.Errorf("%w: malformed target fence", accesswait.ErrAccessDenied)
	}
	if !grantMatches(grant, poll, target, w.source, w.callerFence) {
		return accesswait.AccessGrant{}, fmt.Errorf("%w: grant binding mismatch", accesswait.ErrAccessDenied)
	}
	return grant, nil
}

// grantMatches 比较 grant 的 caller/source/target fence 全量绑定；selector source 必须指向 target。
func grantMatches(grant accesswait.AccessGrant, poll accesswait.PollRequest, target WaitTarget, source taskeventsource.Source, callerFence accesswait.FenceSnapshot) bool {
	return grantIdentityMatches(grant, poll, target) && reflect.DeepEqual(grant.Source, callerFence) && grant.Source.AgentID == source.AgentID && grant.Source.IncarnationID == source.IncarnationID && targetSourceMatchesFence(target.Selector, grant.Target)
}

// grantIdentityMatches 比较 grant id、revision、scope 与读能力。
func grantIdentityMatches(grant accesswait.AccessGrant, poll accesswait.PollRequest, target WaitTarget) bool {
	return grant.GrantID == target.GrantID && grant.Revision == target.GrantRevision && grant.Status == "active" && grant.CanRead && grant.CallerPrincipalRef == poll.CallerPrincipalRef && grant.ScopeRef == poll.ScopeRef
}

// matchesTarget 比较 wait handle id、grant、selector 与游标，阻断 target rollover；句柄 source agent 仍绑定 caller。
func matchesTarget(handle accesswait.WaitHandle, target WaitTarget, source taskeventsource.Source, targetFence accesswait.FenceSnapshot) bool {
	return handle.WaitHandleID == target.Key && handle.GrantID == target.GrantID && handle.GrantRevision == target.GrantRevision && handle.SourceAgentID == source.AgentID && reflect.DeepEqual(handle.Target, targetFence) && reflect.DeepEqual(handle.Selector, target.Selector) && handle.AfterSequence == target.AfterSequence && handle.AfterCursor == target.AfterCursor && targetSourceMatchesFence(handle.Selector, targetFence)
}

// validateTarget 检查单个目标的 caller-relative 字段和 target canonical source。
func validateTarget(t WaitTarget, _ taskeventsource.Source) error {
	if !validTargetIdentity(t) {
		return fmt.Errorf("%w: invalid target", waitaggregate.ErrInvalidRequest)
	}
	if err := t.Selector.Validate(); err != nil {
		return fmt.Errorf("%w: selector: %v", waitaggregate.ErrInvalidRequest, err)
	}
	if !validTargetCursor(t) {
		return fmt.Errorf("%w: invalid cursor", waitaggregate.ErrInvalidRequest)
	}
	if !targetSourceTupleMatchesSelector(t.Selector) {
		return fmt.Errorf("%w: selector source does not match target task source", waitaggregate.ErrInvalidRequest)
	}
	return nil
}

// targetSourceTupleMatchesSelector 要求 selector 的 source tuple 由 target agent id
// 派生，避免 caller source 被误用为跨任务目标事件源。source epoch 仍由 grant fence
// 在 owner admission 前逐字段绑定。
func targetSourceTupleMatchesSelector(selector accesswait.EventSelector) bool {
	sourceID, ok := canonicalTargetSourceID(selector.TargetAgentID)
	return ok && selector.SourceID == sourceID
}

func canonicalTargetSourceID(agentID string) (string, bool) {
	if agentID == "" || strings.ContainsAny(agentID, "/\\") {
		return "", false
	}
	return "task/" + agentID, true
}

// targetSourceMatchesFence 将 selector source tuple 绑定到完整 target fence；事件源
// 的 epoch 不能跨 target incarnation 漂移。完整 fence 仍由 accesswait owner 重验。
func targetSourceMatchesFence(selector accesswait.EventSelector, target accesswait.FenceSnapshot) bool {
	if err := target.ValidateBound(); err != nil || strings.ContainsAny(target.IncarnationID, "/\\") {
		return false
	}
	sourceID, ok := canonicalTargetSourceID(target.AgentID)
	if !ok {
		return false
	}
	return selector.SourceID == sourceID && selector.SourceEpoch == target.IncarnationID && selector.TargetAgentID == target.AgentID
}

func validTargetIdentity(t WaitTarget) bool {
	return strings.TrimSpace(t.Key) == t.Key && t.Key != "" && strings.TrimSpace(t.GrantID) == t.GrantID && t.GrantID != "" && t.GrantRevision > 0
}
func validTargetCursor(t WaitTarget) bool {
	if t.AfterSequence < 0 || strings.TrimSpace(t.AfterCursor) != t.AfterCursor {
		return false
	}
	if t.AfterSequence == 0 {
		return t.AfterCursor == ""
	}
	want, err := eventstore.CanonicalCursor(t.Selector.SourceID, t.Selector.SourceEpoch, t.Selector.Lane, t.AfterSequence)
	return err == nil && t.AfterCursor == want
}

// deadline 将互斥 timeout_ms/deadline 转换为有界 UTC 截止时间。
func (in WaitAgentsInput) deadline(now time.Time) (time.Time, error) {
	if (in.TimeoutMS == nil) == (in.Deadline == nil) {
		return time.Time{}, fmt.Errorf("%w: exactly one of timeout_ms or deadline is required", waitaggregate.ErrInvalidRequest)
	}
	if in.TimeoutMS != nil {
		return deadlineFromTimeout(*in.TimeoutMS, now)
	}
	return deadlineFromString(*in.Deadline, now)
}

// deadlineFromTimeout 校验 timeout_ms 范围并计算绝对 UTC 截止时间。
func deadlineFromTimeout(timeoutMS int, now time.Time) (time.Time, error) {
	if timeoutMS < waitaggregate.MinTimeoutMS || timeoutMS > waitaggregate.MaxTimeoutMS {
		return time.Time{}, fmt.Errorf("%w: timeout_ms out of range", waitaggregate.ErrInvalidRequest)
	}
	return now.Add(time.Duration(timeoutMS) * time.Millisecond), nil
}

// deadlineFromString 解析并校验 canonical UTC RFC3339 截止时间。
func deadlineFromString(raw string, now time.Time) (time.Time, error) {
	value := strings.TrimSpace(raw)
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.Location() != time.UTC || parsed.Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("%w: deadline must be canonical UTC RFC3339", waitaggregate.ErrInvalidRequest)
	}
	if !parsed.After(now) || parsed.Sub(now) < time.Duration(waitaggregate.MinTimeoutMS)*time.Millisecond || parsed.Sub(now) > time.Duration(waitaggregate.MaxTimeoutMS)*time.Millisecond {
		return time.Time{}, fmt.Errorf("%w: deadline out of range", waitaggregate.ErrInvalidRequest)
	}
	return parsed, nil
}

// decodeInput 严格拒绝未知、重复、null 和非对象输入。
func decodeInput(raw json.RawMessage) (WaitAgentsInput, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return WaitAgentsInput{}, fmt.Errorf("%w: input must be an object", waitaggregate.ErrInvalidRequest)
	}
	if trimmed[0] != '{' {
		return WaitAgentsInput{}, fmt.Errorf("%w: input must be an object", waitaggregate.ErrInvalidRequest)
	}
	if err := scanWaitJSON(trimmed); err != nil {
		return WaitAgentsInput{}, fmt.Errorf("%w: %v", waitaggregate.ErrInvalidRequest, err)
	}
	var in WaitAgentsInput
	if err := shared.DecodeInput(trimmed, &in); err != nil {
		return WaitAgentsInput{}, err
	}
	if err := validateDecodedInput(in); err != nil {
		return WaitAgentsInput{}, err
	}
	return in, nil
}

// validateDecodedInput 执行 one-hot、条件和目标数量守卫。
func validateDecodedInput(in WaitAgentsInput) error {
	if len(in.Targets) == 0 || len(in.Targets) > 32 {
		return fmt.Errorf("%w: targets must contain 1..32 entries", waitaggregate.ErrInvalidRequest)
	}
	if err := validateInputDeadlineFields(in); err != nil {
		return err
	}
	if err := validateCondition(in); err != nil {
		return err
	}
	return validateTargetKeys(in.Targets)
}

// validateInputDeadlineFields 检查 timeout_ms/deadline 的 one-hot 与范围。
func validateInputDeadlineFields(in WaitAgentsInput) error {
	if in.TimeoutMS != nil && in.Deadline != nil {
		return fmt.Errorf("%w: timeout_ms and deadline are mutually exclusive", waitaggregate.ErrInvalidRequest)
	}
	if in.TimeoutMS == nil && in.Deadline == nil {
		return fmt.Errorf("%w: exactly one of timeout_ms or deadline is required", waitaggregate.ErrInvalidRequest)
	}
	if in.TimeoutMS != nil && (*in.TimeoutMS < waitaggregate.MinTimeoutMS || *in.TimeoutMS > waitaggregate.MaxTimeoutMS) {
		return fmt.Errorf("%w: timeout_ms out of range", waitaggregate.ErrInvalidRequest)
	}
	return nil
}

// validateCondition 检查条件枚举及 quorum_count 单选约束。
func validateCondition(in WaitAgentsInput) error {
	valid := in.Condition == waitaggregate.ConditionAny || in.Condition == waitaggregate.ConditionAll || in.Condition == waitaggregate.ConditionQuorum || in.Condition == waitaggregate.ConditionFirstSuccess
	if !valid {
		return fmt.Errorf("%w: invalid condition", waitaggregate.ErrInvalidRequest)
	}
	if in.Condition == waitaggregate.ConditionQuorum && (in.QuorumCount <= 0 || in.QuorumCount > len(in.Targets)) {
		return fmt.Errorf("%w: invalid quorum_count", waitaggregate.ErrInvalidRequest)
	}
	if in.Condition != waitaggregate.ConditionQuorum && in.QuorumCount != 0 {
		return fmt.Errorf("%w: quorum_count only valid for quorum", waitaggregate.ErrInvalidRequest)
	}
	return nil
}

func validateTargetKeys(targets []WaitTarget) error {
	seen := make(map[string]bool, len(targets))
	for _, target := range targets {
		if seen[target.Key] {
			return fmt.Errorf("%w: duplicate target key", waitaggregate.ErrInvalidRequest)
		}
		seen[target.Key] = true
	}
	return nil
}

func scanWaitJSON(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanValue(d); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON payload")
	}
	return nil
}

// scanValue 递归扫描 JSON，检测 null 和重复对象键。
func scanValue(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	switch v := tok.(type) {
	case nil:
		return errors.New("null values are not allowed")
	case json.Delim:
		if v == '{' {
			return scanObject(d)
		}
		if v == '[' {
			return scanArray(d)
		}
	}
	return nil
}

// scanObject 扫描对象键并拒绝重复字段。
func scanObject(d *json.Decoder) error {
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok {
			return errors.New("invalid object key")
		}
		if seen[name] {
			return fmt.Errorf("duplicate field %q", name)
		}
		seen[name] = true
		if err := scanValue(d); err != nil {
			return err
		}
	}
	_, err := d.Token()
	return err
}

func scanArray(d *json.Decoder) error {
	for d.More() {
		if err := scanValue(d); err != nil {
			return err
		}
	}
	_, err := d.Token()
	return err
}

func isNil(v any) bool {
	if v == nil {
		return true
	}
	x := reflect.ValueOf(v)
	switch x.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
		return x.IsNil()
	}
	return false
}

// WaitDefinition 仅供未来 unified_v2 manifest 使用，当前不会注册工具。
func WaitDefinition() toolspkg.ToolDefinition {
	return toolspkg.ToolDefinition{Name: "wait_agents", Description: "Wait for caller-authorized durable agent events.", InputSchema: waitInputSchema()}
}

func waitBoundedText(description string, max int) toolspkg.Schema {
	schema := toolspkg.StringSchema(description)
	schema["minLength"], schema["maxLength"] = 1, max
	return schema
}

func waitSelectorSchema() toolspkg.Schema {
	eventTypes := toolspkg.ArraySchema(waitBoundedText("Event type.", 128), "Sorted unique event types (maximum 32).")
	eventTypes["minItems"], eventTypes["maxItems"], eventTypes["uniqueItems"] = 1, 32, true
	return toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"version":         {"type": "integer", "const": 1, "description": "Selector schema version."},
		"source_id":       waitBoundedText("Canonical task event source id.", 256),
		"source_epoch":    waitBoundedText("Canonical task event source epoch.", 128),
		"lane":            toolspkg.EnumStringSchema("Event lane.", "control", "output", "content"),
		"target_agent_id": waitBoundedText("Selector target binding id.", 128),
		"event_types":     eventTypes,
	}, "version", "source_id", "source_epoch", "lane", "target_agent_id", "event_types")
}

func waitTargetSchema() toolspkg.Schema {
	grantRevision := toolspkg.IntegerSchema("Expected positive grant revision.")
	grantRevision["minimum"] = 1
	afterSequence := toolspkg.IntegerSchema("Last observed nonnegative source sequence.")
	afterSequence["minimum"] = 0
	afterCursor := toolspkg.StringSchema("Last observed canonical cursor.")
	afterCursor["maxLength"] = 512
	target := toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"key":            waitBoundedText("Opaque durable wait handle key.", 128),
		"grant_id":       waitBoundedText("Access grant reference.", 128),
		"grant_revision": grantRevision,
		"selector":       waitSelectorSchema(),
		"after_sequence": afterSequence,
		"after_cursor":   afterCursor,
	}, "key", "grant_id", "grant_revision", "selector")
	// after_cursor is absent for the initial sequence and required thereafter.
	target["oneOf"] = []any{
		map[string]any{"properties": map[string]any{"after_sequence": map[string]any{"const": 0}, "after_cursor": map[string]any{"const": ""}}},
		map[string]any{"properties": map[string]any{"after_sequence": map[string]any{"minimum": 1}, "after_cursor": waitBoundedText("Last observed canonical cursor after the initial sequence.", 512)}, "required": []string{"after_cursor"}},
	}
	return target
}

func waitInputSchema() toolspkg.Schema {
	targets := toolspkg.ArraySchema(waitTargetSchema(), "Caller-relative durable wait targets (maximum 32).")
	targets["minItems"], targets["maxItems"] = 1, 32
	quorumCount := toolspkg.IntegerSchema("Required only for quorum; at most 32.")
	quorumCount["minimum"], quorumCount["maximum"] = 1, 32
	predicate := toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"required_event_type":       {"type": "string", "maxLength": 128, "description": "Required event type."},
		"require_nonempty_metadata": toolspkg.BooleanSchema("Require non-empty event metadata."),
	})
	timeoutMS := toolspkg.IntegerSchema("Bounded wait timeout in milliseconds (minimum 10ms to avoid deadline-capture races).")
	timeoutMS["minimum"], timeoutMS["maximum"] = waitaggregate.MinTimeoutMS, waitaggregate.MaxTimeoutMS
	deadline := toolspkg.StringSchema("Canonical UTC RFC3339 deadline; mutually exclusive with timeout_ms.")
	deadline["format"], deadline["minLength"], deadline["maxLength"], deadline["pattern"] = "date-time", 20, 30, `Z$`
	input := toolspkg.ObjectSchema(map[string]toolspkg.Schema{
		"targets":                 targets,
		"condition":               toolspkg.EnumStringSchema("Aggregation condition.", "any", "all", "quorum", "first_success"),
		"quorum_count":            quorumCount,
		"predicate":               predicate,
		"timeout_ms":              timeoutMS,
		"deadline":                deadline,
		"return_on_indeterminate": toolspkg.BooleanSchema("Return as soon as any target is indeterminate."),
	}, "targets", "condition")
	input["oneOf"] = []any{
		map[string]any{"required": []string{"timeout_ms"}, "not": map[string]any{"required": []string{"deadline"}}},
		map[string]any{"required": []string{"deadline"}, "not": map[string]any{"required": []string{"timeout_ms"}}},
	}
	input["allOf"] = waitConditionalSchemas()
	return input
}

func waitConditionalSchemas() []any {
	firstPredicate := map[string]any{"required": []string{"required_event_type"}, "properties": map[string]any{"required_event_type": waitBoundedText("Required event type.", 128)}}
	return []any{
		map[string]any{"if": map[string]any{"properties": map[string]any{"condition": map[string]any{"const": "quorum"}}}, "then": map[string]any{"required": []string{"quorum_count"}}, "else": map[string]any{"not": map[string]any{"required": []string{"quorum_count"}}}},
		map[string]any{"if": map[string]any{"properties": map[string]any{"condition": map[string]any{"const": "first_success"}}}, "then": map[string]any{"required": []string{"predicate"}, "properties": map[string]any{"predicate": firstPredicate}}},
		map[string]any{"if": map[string]any{"properties": map[string]any{"predicate": map[string]any{"properties": map[string]any{"require_nonempty_metadata": map[string]any{"const": true}}}}}, "then": map[string]any{"properties": map[string]any{"predicate": firstPredicate}}},
	}
}
