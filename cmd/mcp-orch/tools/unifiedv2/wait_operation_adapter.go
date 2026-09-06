package unifiedv2

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskeventsource"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/taskprincipal"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/waitaggregate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/waitoperation"
	toolspkg "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

var (
	// ErrWaitOperationAdapterDisabled 表示 operation-backed candidate 未装配。
	ErrWaitOperationAdapterDisabled = errors.New("wait operation wait_agents adapter is disabled")
	// ErrWaitOperationAdapterUnavailable 表示 durable operation 或其身份围栏不可读。
	ErrWaitOperationAdapterUnavailable = errors.New("wait operation wait_agents adapter is unavailable")
	// ErrWaitOutcomeProjectionUnavailable 表示没有可授权的 durable outcome projection。
	ErrWaitOutcomeProjectionUnavailable = errors.New("wait outcome projection is unavailable")
	// ErrWaitOperationOutcomeUnknown 表示 durable target 的效果仍不确定，禁止猜测成功。
	ErrWaitOperationOutcomeUnknown = errors.New("wait operation outcome is unknown")
)

// WaitOperationInput 只引用已持久化 operation 和 target key；selector、cursor、
// grant 与 wait handle 均从 owner 精确读回，不能由 caller 替换。
type WaitOperationInput struct {
	OperationKey          string                  `json:"operation_key"`
	OperationDigest       string                  `json:"operation_digest"`
	TargetKeys            []string                `json:"target_keys"`
	Kind                  string                  `json:"kind,omitempty"`
	Condition             waitaggregate.Condition `json:"condition"`
	QuorumCount           int                     `json:"quorum_count,omitempty"`
	Predicate             waitaggregate.Predicate `json:"predicate"`
	ReturnOnIndeterminate bool                    `json:"return_on_indeterminate,omitempty"`
}

// WaitOperationAdapter 是未注册的 candidate-only durable wait adapter。
// operation owner 只负责读取计划/target，accesswait 仍是唯一 wait owner。
type WaitOperationAdapter struct {
	resolver  *taskprincipal.Resolver
	operation waitoperation.Owner
	waiter    operationGrantWaiter
	clock     waitaggregate.Clock
}

type operationGrantWaiter interface {
	grantWaiter
	waitHandleReader
}

// NewWaitOperationAdapter 构造 operation-backed candidate；不注册 unified_v2。
func NewWaitOperationAdapter(resolver *taskprincipal.Resolver, operation waitoperation.Owner, owner any, clock waitaggregate.Clock) (*WaitOperationAdapter, error) {
	waiter, ok := owner.(operationGrantWaiter)
	if resolver == nil || operation == nil || !ok || isNil(operation) || isNil(waiter) {
		return nil, ErrWaitOperationAdapterDisabled
	}
	if clock == nil {
		return nil, fmt.Errorf("%w: %w", ErrWaitOperationAdapterUnavailable, waitaggregate.ErrNilClock)
	}
	return &WaitOperationAdapter{resolver: resolver, operation: operation, waiter: waiter, clock: clock}, nil
}

// NewWithWaitOperation 是 NewWaitOperationAdapter 的简洁别名。
func NewWithWaitOperation(resolver *taskprincipal.Resolver, operation waitoperation.Owner, owner any, clock waitaggregate.Clock) (*WaitOperationAdapter, error) {
	return NewWaitOperationAdapter(resolver, operation, owner, clock)
}

// NewWaitOperationWithOwner 保留 owner-first 的内部构造顺序。
func NewWaitOperationWithOwner(operation waitoperation.Owner, owner any, resolver *taskprincipal.Resolver, clock waitaggregate.Clock) (*WaitOperationAdapter, error) {
	return NewWaitOperationAdapter(resolver, operation, owner, clock)
}

// ToolHandler 返回未注册候选处理器。
func (a *WaitOperationAdapter) ToolHandler() toolspkg.ToolHandler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) { return a.Handle(ctx, raw) }
}

// Handle 从 durable operation/target 读回 exact wait handles，然后委托 waitaggregate。
func (a *WaitOperationAdapter) Handle(ctx context.Context, raw json.RawMessage) (any, error) {
	if !a.ready() {
		return nil, ErrWaitOperationAdapterDisabled
	}
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrWaitOperationAdapterUnavailable)
	}
	if _, err := requireTaskPrincipal(ctx); err != nil {
		return nil, err
	}
	in, err := decodeWaitOperationInput(raw)
	if err != nil {
		return nil, err
	}
	if err := validateOperationKind(in.Kind); err != nil {
		return nil, err
	}
	return a.waitOperation(ctx, in)
}

// waitOperation 执行 operation-backed 聚合并在返回前复核 durable 快照。
func (a *WaitOperationAdapter) waitOperation(ctx context.Context, in WaitOperationInput) (any, error) {
	operation, targets, waitTargets, now, source, callerFence, err := a.prepareOperation(ctx, in)
	if err != nil {
		return nil, err
	}
	if err := a.verifyOperationSnapshot(ctx, operation, in.TargetKeys); err != nil {
		return nil, err
	}
	req := waitaggregate.Request{Targets: targets, Condition: in.Condition, QuorumCount: in.QuorumCount, Predicate: in.Predicate, DeadlineAt: operation.DeadlineAt, ReturnOnIndeterminate: in.ReturnOnIndeterminate}
	guard := &operationValidatingWaiter{waiter: a.waiter, targets: waitTargets, source: source, callerFence: callerFence}
	aggregator, err := waitaggregate.NewWithClock(guard, func() time.Time { return now })
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWaitOperationAdapterUnavailable, err)
	}
	result, err := aggregator.WaitAt(ctx, req, now)
	if err != nil {
		return nil, err
	}
	if err := a.verifyOperationSnapshot(ctx, operation, in.TargetKeys); err != nil {
		return nil, err
	}
	return summarizeResult(result), nil
}

// verifyOperationSnapshot 在进入聚合器前确认 operation/target head 未发生 rollover。
func (a *WaitOperationAdapter) verifyOperationSnapshot(ctx context.Context, expected waitoperation.Operation, keys []string) error {
	actual, err := a.operation.Get(ctx, waitoperation.OperationLookup{Key: expected.OperationKey, Digest: expected.OperationDigest})
	if err != nil {
		return fmt.Errorf("%w: operation snapshot: %w", ErrWaitOperationAdapterUnavailable, err)
	}
	if err := verifyOperationHead(expected, actual); err != nil {
		return err
	}
	expectedTargets, actualTargets := indexTargetsByKey(expected.Targets), indexTargetsByKey(actual.Targets)
	for _, key := range keys {
		before, beforeOK := expectedTargets[key]
		after, afterOK := actualTargets[key]
		if !beforeOK || !afterOK || !sameTargetProjection(before, after) {
			if afterOK && targetOutcomeUnknown(after) {
				return ErrWaitOperationOutcomeUnknown
			}
			return fmt.Errorf("%w: target %s head changed before wait", ErrWaitOutcomeProjectionUnavailable, key)
		}
	}
	return nil
}

// sameTargetProjection 比较 target 的完整 durable 快照，阻断同版本伪造字段返回。
func sameTargetProjection(expected, actual waitoperation.TargetState) bool {
	return reflect.DeepEqual(expected, actual)
}

// verifyOperationHead 比较 operation revision/status，阻断未知或 rollover 头继续等待。
func verifyOperationHead(expected, actual waitoperation.Operation) error {
	if err := validateOperationProjection(actual); err != nil {
		return err
	}
	if sameOperationIdentity(expected, actual) && actual.Revision == expected.Revision && actual.Status == expected.Status && actual.UncertainPhase == expected.UncertainPhase {
		return nil
	}
	if actual.Status == "outcome_unknown" || actual.UncertainPhase != "" {
		return ErrWaitOperationOutcomeUnknown
	}
	return fmt.Errorf("%w: operation head changed before wait", ErrWaitOutcomeProjectionUnavailable)
}

// sameOperationIdentity 比较 operation 的不可变身份和时间边界，避免二次读取被替换。
func sameOperationIdentity(expected, actual waitoperation.Operation) bool {
	return actual.OperationKey == expected.OperationKey &&
		actual.OperationDigest == expected.OperationDigest &&
		actual.TargetSetDigest == expected.TargetSetDigest &&
		actual.CallerPrincipalRef == expected.CallerPrincipalRef &&
		actual.ScopeRef == expected.ScopeRef &&
		reflect.DeepEqual(actual.Source, expected.Source) &&
		actual.IssuedAt.Equal(expected.IssuedAt) &&
		actual.DeadlineAt.Equal(expected.DeadlineAt) &&
		actual.TargetCount == expected.TargetCount &&
		actual.SchemaVersion == expected.SchemaVersion
}

func indexTargetsByKey(targets []waitoperation.TargetState) map[string]waitoperation.TargetState {
	result := make(map[string]waitoperation.TargetState, len(targets))
	for _, target := range targets {
		result[target.Key] = target
	}
	return result
}

// validateOperationKind 在 Operation 无 kind 字段时仅允许唯一安全的 event 投影。
func validateOperationKind(kind string) error {
	if kind == "command" || kind == "command_submission" {
		return fmt.Errorf("%w: %s", ErrWaitOutcomeProjectionUnavailable, kind)
	}
	if kind != "event" {
		return fmt.Errorf("%w: kind must be explicit event", waitaggregate.ErrInvalidRequest)
	}
	return nil
}

// prepareOperation 完成 task 身份、operation head 和 target projection 的只读准备。
func (a *WaitOperationAdapter) prepareOperation(ctx context.Context, input WaitOperationInput) (waitoperation.Operation, []waitaggregate.Target, map[string]WaitTarget, time.Time, taskeventsource.Source, accesswait.FenceSnapshot, error) {
	base := &WaitAgentsHandler{resolver: a.resolver, waiter: a.waiter, clock: a.clock}
	task, source, callerFence, err := base.resolveTask(ctx)
	if err != nil {
		return waitoperation.Operation{}, nil, nil, time.Time{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, err
	}
	now := a.clock()
	if err := validateSecurityClock(now); err != nil {
		return waitoperation.Operation{}, nil, nil, time.Time{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, fmt.Errorf("%w: %w", ErrWaitOperationAdapterUnavailable, err)
	}
	operation, err := a.operation.Get(ctx, waitoperation.OperationLookup{Key: input.OperationKey, Digest: input.OperationDigest})
	if err != nil {
		return waitoperation.Operation{}, nil, nil, time.Time{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, fmt.Errorf("%w: operation: %w", ErrWaitOperationAdapterUnavailable, err)
	}
	if err := validateOperationProjection(operation); err != nil {
		return waitoperation.Operation{}, nil, nil, time.Time{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, err
	}
	if err := validateOperationIdentity(operation, task.Principal, callerFence, source); err != nil {
		return waitoperation.Operation{}, nil, nil, time.Time{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, err
	}
	if err := validateOperationOutcome(operation); err != nil {
		return waitoperation.Operation{}, nil, nil, time.Time{}, taskeventsource.Source{}, accesswait.FenceSnapshot{}, err
	}
	targets, waitTargets, err := a.readTargets(ctx, operation, input.TargetKeys, task.Principal, source, callerFence)
	return operation, targets, waitTargets, now, source, callerFence, err
}

// ready 检查 operation、accesswait、resolver 和安全时间依赖是否完整。
func (a *WaitOperationAdapter) ready() bool {
	return a != nil && a.resolver != nil && a.operation != nil && !isNil(a.operation) && a.waiter != nil && !isNil(a.waiter) && a.clock != nil
}

// validateOperationIdentity 校验 operation 与当前 task principal/source fence 完全一致。
func validateOperationIdentity(operation waitoperation.Operation, principal common.CallerPrincipal, callerFence accesswait.FenceSnapshot, source taskeventsource.Source) error {
	if !validOperationShape(operation) {
		return fmt.Errorf("%w: malformed operation", ErrWaitOperationAdapterUnavailable)
	}
	if !operationCallerMatches(operation, principal, callerFence, source) {
		return fmt.Errorf("%w: operation caller/source fence mismatch", accesswait.ErrAccessDenied)
	}
	return nil
}

// validOperationShape 验证 operation 标识、摘要、schema 与有界 target 数量。
func validOperationShape(operation waitoperation.Operation) bool {
	return validOperationIdentityShape(operation) && validOperationLifecycleShape(operation) && validOperationTargetCount(operation)
}

// validOperationIdentityShape 校验 operation 的规范身份、摘要与 schema。
func validOperationIdentityShape(operation waitoperation.Operation) bool {
	return validCanonicalText(operation.OperationKey, waitoperation.MaxOperationKey) &&
		validDigest(operation.OperationDigest) && validDigest(operation.TargetSetDigest) &&
		validCanonicalText(operation.CallerPrincipalRef, 512) && validCanonicalText(operation.ScopeRef, 512) &&
		operation.SchemaVersion == waitoperation.SchemaVersion
}

// validOperationLifecycleShape 校验 operation 修订号及已知生命周期状态。
func validOperationLifecycleShape(operation waitoperation.Operation) bool {
	return operation.Revision > 0 && validOperationStatus(operation.Status)
}

// validOperationTargetCount 校验 target_count 与 owner 返回集合长度一致且有界。
func validOperationTargetCount(operation waitoperation.Operation) bool {
	return operation.TargetCount > 0 && operation.TargetCount <= waitoperation.MaxTargetCount && len(operation.Targets) == int(operation.TargetCount)
}

// validateOperationProjection 校验 owner 返回的 operation 及完整 target 集合边界。
func validateOperationProjection(operation waitoperation.Operation) error {
	if !validOperationShape(operation) {
		return fmt.Errorf("%w: malformed operation projection", ErrWaitOperationAdapterUnavailable)
	}
	seen := make(map[string]struct{}, len(operation.Targets))
	for index, target := range operation.Targets {
		if err := validateTargetProjection(operation, target, index); err != nil {
			return err
		}
		if _, exists := seen[target.Key]; exists {
			return fmt.Errorf("%w: duplicate durable target key", ErrWaitOperationAdapterUnavailable)
		}
		seen[target.Key] = struct{}{}
	}
	return nil
}

// validateTargetProjection 校验 target 与 operation 的归属、索引及摘要格式。
func validateTargetProjection(operation waitoperation.Operation, target waitoperation.TargetState, index int) error {
	if !validTargetIdentityShape(operation, target, index) {
		return fmt.Errorf("%w: malformed target projection", ErrWaitOperationAdapterUnavailable)
	}
	if !validTargetReferenceShape(target) {
		return fmt.Errorf("%w: malformed target predecessor digest", ErrWaitOperationAdapterUnavailable)
	}
	if !validTargetDigestShape(target) {
		return fmt.Errorf("%w: malformed target digest", ErrWaitOperationAdapterUnavailable)
	}
	return nil
}

// validTargetIdentityShape 校验 target 的 operation 归属、索引、key 与主摘要。
func validTargetIdentityShape(operation waitoperation.Operation, target waitoperation.TargetState, index int) bool {
	return target.OperationKey == operation.OperationKey && target.OperationDigest == operation.OperationDigest &&
		target.SchemaVersion == operation.SchemaVersion && target.TargetIndex == int64(index+1) &&
		validCanonicalText(target.Key, waitoperation.MaxTargetKey) && validDigest(target.TargetDigest) && validDigest(target.RecordDigest)
}

// validTargetReferenceShape 校验 target 的可选 resolved 引用及前驱摘要。
func validTargetReferenceShape(target waitoperation.TargetState) bool {
	if target.PreviousRecordDigest != "" && !validDigest(target.PreviousRecordDigest) {
		return false
	}
	if target.ResolvedGrantID != nil && !validCanonicalText(*target.ResolvedGrantID, 128) {
		return false
	}
	return target.ResolvedWaitHandleID == nil || validCanonicalText(*target.ResolvedWaitHandleID, 128)
}

// validTargetDigestShape 校验 target 的四类授权/等待摘要均为规范 SHA-256。
func validTargetDigestShape(target waitoperation.TargetState) bool {
	for _, digest := range []string{target.GrantKeyDigest, target.GrantRequestDigest, target.WaitKeyDigest, target.WaitRequestDigest} {
		if !validDigest(digest) {
			return false
		}
	}
	return true
}

// validOperationStatus 限定 durable operation 的已知状态，未知状态不得进入等待。
func validOperationStatus(status string) bool {
	switch status {
	case "provisioning", "ready", "partial", "outcome_unknown", "cleanup_pending", "cleanup_cleared":
		return true
	default:
		return false
	}
}

func operationCallerMatches(operation waitoperation.Operation, principal common.CallerPrincipal, callerFence accesswait.FenceSnapshot, source taskeventsource.Source) bool {
	return operation.CallerPrincipalRef == principal.PrincipalRef && operation.ScopeRef == principal.ScopeRef && reflect.DeepEqual(operation.Source, callerFence) && operation.Source.AgentID == source.AgentID && operation.Source.IncarnationID == source.IncarnationID
}

func validateOperationOutcome(operation waitoperation.Operation) error {
	if operation.Status == "outcome_unknown" || operation.UncertainPhase != "" {
		return ErrWaitOperationOutcomeUnknown
	}
	return nil
}

// readTargets 将 caller 选择的 target keys 解析为 exact durable wait poll 目标。
func (a *WaitOperationAdapter) readTargets(ctx context.Context, operation waitoperation.Operation, keys []string, principal common.CallerPrincipal, source taskeventsource.Source, callerFence accesswait.FenceSnapshot) ([]waitaggregate.Target, map[string]WaitTarget, error) {
	if !validTargetKeyCount(keys) {
		return nil, nil, fmt.Errorf("%w: target_keys must contain 1..32 entries", waitaggregate.ErrInvalidRequest)
	}
	byKey, err := indexOperationTargets(operation.Targets)
	if err != nil {
		return nil, nil, err
	}
	seen := make(map[string]struct{}, len(keys))
	result := make([]waitaggregate.Target, 0, len(keys))
	waitTargets := make(map[string]WaitTarget, len(keys))
	for _, key := range keys {
		if !validRequestedTargetKey(key, seen) {
			return nil, nil, fmt.Errorf("%w: duplicate target key", waitaggregate.ErrInvalidRequest)
		}
		seen[key] = struct{}{}
		target, ok := byKey[key]
		if !ok {
			return nil, nil, fmt.Errorf("%w: target not found", waitoperation.ErrTargetNotFound)
		}
		poll, durableTarget, err := a.readOperationTarget(ctx, operation, target, key, principal, source, callerFence)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, waitaggregate.Target{Key: key, Poll: poll})
		if _, exists := waitTargets[poll.WaitHandleID]; exists {
			return nil, nil, fmt.Errorf("%w: duplicate durable wait handle", ErrWaitOutcomeProjectionUnavailable)
		}
		waitTargets[poll.WaitHandleID] = durableTarget
	}
	return result, waitTargets, nil
}

func validTargetKeyCount(keys []string) bool {
	return len(keys) > 0 && len(keys) <= waitaggregate.MaxTargets
}

func indexOperationTargets(targets []waitoperation.TargetState) (map[string]waitoperation.TargetState, error) {
	byKey := make(map[string]waitoperation.TargetState, len(targets))
	for _, target := range targets {
		if _, exists := byKey[target.Key]; exists {
			return nil, fmt.Errorf("%w: duplicate durable target key", ErrWaitOperationAdapterUnavailable)
		}
		byKey[target.Key] = target
	}
	return byKey, nil
}

func validRequestedTargetKey(key string, seen map[string]struct{}) bool {
	if !validCanonicalText(key, waitoperation.MaxTargetKey) {
		return false
	}
	_, exists := seen[key]
	return !exists
}

// readOperationTarget 读取并验证一个 target 的 grant 与 wait handle。
func (a *WaitOperationAdapter) readOperationTarget(ctx context.Context, operation waitoperation.Operation, target waitoperation.TargetState, key string, principal common.CallerPrincipal, source taskeventsource.Source, callerFence accesswait.FenceSnapshot) (accesswait.PollRequest, WaitTarget, error) {
	if err := validateOperationTargetState(target, key); err != nil {
		return accesswait.PollRequest{}, WaitTarget{}, err
	}
	waitTarget := WaitTarget{Key: key, GrantID: *target.ResolvedGrantID, GrantRevision: target.GrantRevision, Selector: target.Selector, AfterSequence: target.AfterSequence, AfterCursor: target.AfterCursor}
	if err := validateTarget(waitTarget, source); err != nil {
		return accesswait.PollRequest{}, WaitTarget{}, fmt.Errorf("%w: target %s: %v", ErrWaitOperationAdapterUnavailable, key, err)
	}
	grant, err := a.waiter.GetGrant(ctx, waitTarget.GrantID)
	if err != nil {
		return accesswait.PollRequest{}, WaitTarget{}, err
	}
	if err := validateOperationGrant(grant, operation, target, principal, callerFence, source); err != nil {
		return accesswait.PollRequest{}, WaitTarget{}, fmt.Errorf("%w: target %s grant fence mismatch", accesswait.ErrAccessDenied, key)
	}
	poll := accesswait.PollRequest{WaitHandleID: *target.ResolvedWaitHandleID, CallerPrincipalRef: principal.PrincipalRef, ScopeRef: principal.ScopeRef, GrantRevision: target.GrantRevision}
	durableTarget := waitTarget
	durableTarget.Key = poll.WaitHandleID
	handle, err := a.waiter.GetWait(ctx, poll)
	if err != nil {
		return accesswait.PollRequest{}, WaitTarget{}, err
	}
	if !pendingHandleMatches(handle, target.WaitRevision) {
		return accesswait.PollRequest{}, WaitTarget{}, fmt.Errorf("%w: target %s wait handle is not the expected pending revision", ErrWaitOutcomeProjectionUnavailable, key)
	}
	if err := validateRequestedHandle(handle, poll, durableTarget, source); err != nil || !matchesTarget(handle, durableTarget, source, grant.Target) {
		if err == nil {
			err = accesswait.ErrAccessDenied
		}
		return accesswait.PollRequest{}, WaitTarget{}, fmt.Errorf("%w: target %s durable wait handle mismatch: %v", accesswait.ErrAccessDenied, key, err)
	}
	return poll, durableTarget, nil
}

func validateOperationGrant(grant accesswait.AccessGrant, operation waitoperation.Operation, target waitoperation.TargetState, principal common.CallerPrincipal, callerFence accesswait.FenceSnapshot, source taskeventsource.Source) error {
	if err := grant.Validate(); err != nil || !grantIdentityForOperation(grant, operation, target, principal, callerFence, source) {
		return accesswait.ErrAccessDenied
	}
	return nil
}

func pendingHandleMatches(handle accesswait.WaitHandle, revision int64) bool {
	return handle.Status == "pending" && handle.Revision == revision
}

// validateOperationTargetState 检查 target 是否仍有可读取的 pending projection。
func validateOperationTargetState(target waitoperation.TargetState, key string) error {
	if targetOutcomeUnknown(target) {
		return fmt.Errorf("%w: target %s", ErrWaitOperationOutcomeUnknown, key)
	}
	if !targetPendingProjection(target) {
		return fmt.Errorf("%w: target %s has no pending durable wait projection", ErrWaitOutcomeProjectionUnavailable, key)
	}
	return nil
}

func targetOutcomeUnknown(target waitoperation.TargetState) bool {
	return target.Phase == "outcome_unknown" || target.UncertainPhase != "" || target.WaitStatus == "unknown" || target.GrantStatus == "unknown"
}

// targetPendingProjection 要求 target 具备 active grant 与 pending wait 的完整引用。
func targetPendingProjection(target waitoperation.TargetState) bool {
	return target.Phase == "waiting" && target.ResolvedGrantID != nil && target.ResolvedWaitHandleID != nil && target.GrantStatus == "active" && target.WaitStatus == "pending" && target.GrantRevision > 0 && target.WaitRevision > 0
}

type operationValidatingWaiter struct {
	waiter      grantWaiter
	targets     map[string]WaitTarget
	source      taskeventsource.Source
	callerFence accesswait.FenceSnapshot
}

// Wait 将 aggregate poll 映射为预验证的 durable wait handle。
func (w *operationValidatingWaiter) Wait(ctx context.Context, poll accesswait.PollRequest) (accesswait.WaitResult, error) {
	target, ok := w.targets[poll.WaitHandleID]
	if !ok {
		return accesswait.WaitResult{}, fmt.Errorf("%w: unknown durable wait handle", waitaggregate.ErrInvalidRequest)
	}
	guard := &validatingWaiter{waiter: w.waiter, targets: []WaitTarget{target}, source: w.source, callerFence: w.callerFence}
	return guard.Wait(ctx, poll)
}

// grantIdentityForOperation 校验 grant、operation 和 target 的完整授权围栏。
func grantIdentityForOperation(grant accesswait.AccessGrant, operation waitoperation.Operation, target waitoperation.TargetState, principal common.CallerPrincipal, callerFence accesswait.FenceSnapshot, source taskeventsource.Source) bool {
	return grantCoreIdentityMatches(grant, target, principal) && reflect.DeepEqual(grant.Source, callerFence) && reflect.DeepEqual(grant.Target, target.Target) && grant.Source.AgentID == source.AgentID && operation.CallerPrincipalRef == grant.CallerPrincipalRef && operation.ScopeRef == grant.ScopeRef
}

// grantCoreIdentityMatches 校验 grant 的 caller、scope、revision 与读能力。
func grantCoreIdentityMatches(grant accesswait.AccessGrant, target waitoperation.TargetState, principal common.CallerPrincipal) bool {
	return grant.GrantID == *target.ResolvedGrantID && grant.Revision == target.GrantRevision && grant.Status == "active" && grant.CanRead && grant.CallerPrincipalRef == principal.PrincipalRef && grant.ScopeRef == principal.ScopeRef
}

// decodeWaitOperationInput 严格解析 operation-backed wait 的有限 JSON 合同。
func decodeWaitOperationInput(raw json.RawMessage) (WaitOperationInput, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' {
		return WaitOperationInput{}, fmt.Errorf("%w: input must be an object", waitaggregate.ErrInvalidRequest)
	}
	if err := scanWaitJSON(trimmed); err != nil {
		return WaitOperationInput{}, fmt.Errorf("%w: %v", waitaggregate.ErrInvalidRequest, err)
	}
	var input WaitOperationInput
	if err := shared.DecodeInput(trimmed, &input); err != nil {
		return WaitOperationInput{}, err
	}
	if err := validateDecodedOperationInput(input); err != nil {
		return WaitOperationInput{}, err
	}
	return input, nil
}

// validateDecodedOperationInput 校验 operation identity、目标数量和 quorum 单选约束。
func validateDecodedOperationInput(input WaitOperationInput) error {
	if !validInputIdentity(input) {
		return fmt.Errorf("%w: operation identity and condition are required", waitaggregate.ErrInvalidRequest)
	}
	if err := validateOperationKind(input.Kind); err != nil {
		return err
	}
	if !validTargetKeyCount(input.TargetKeys) {
		return fmt.Errorf("%w: target_keys must contain 1..32 entries", waitaggregate.ErrInvalidRequest)
	}
	if err := validateInputTargetKeys(input.TargetKeys); err != nil {
		return err
	}
	return validateInputQuorum(input)
}

func validateInputTargetKeys(keys []string) error {
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if !validRequestedTargetKey(key, seen) {
			return fmt.Errorf("%w: target key is invalid or duplicated", waitaggregate.ErrInvalidRequest)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// validateInputQuorum 检查 quorum_count 与 condition 的互斥约束。
func validateInputQuorum(input WaitOperationInput) error {
	if input.Condition == waitaggregate.ConditionQuorum && (input.QuorumCount < 1 || input.QuorumCount > len(input.TargetKeys)) {
		return fmt.Errorf("%w: invalid quorum_count", waitaggregate.ErrInvalidRequest)
	}
	if input.Condition != waitaggregate.ConditionQuorum && input.QuorumCount != 0 {
		return fmt.Errorf("%w: quorum_count only valid for quorum", waitaggregate.ErrInvalidRequest)
	}
	return nil
}

func validInputIdentity(input WaitOperationInput) bool {
	return validCanonicalText(input.OperationKey, waitoperation.MaxOperationKey) && validDigest(input.OperationDigest) && input.Condition != ""
}

func validCanonicalText(value string, max int) bool {
	return value != "" && len(value) <= max && strings.TrimSpace(value) == value && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func validDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
