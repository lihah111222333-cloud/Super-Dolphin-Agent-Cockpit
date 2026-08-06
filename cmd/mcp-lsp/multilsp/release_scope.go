package multilsp

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	ReleaseScopeAgentThread     = "agent_thread"
	ReleaseScopeAgentAllThreads = "agent_all_threads"
	ReleaseScopeManagerKey      = "manager_key"
)

// ReleaseScopeRequest 描述一次 LSP manager 释放请求，scope 字段决定关闭哪些缓存实例。
type ReleaseScopeRequest struct {
	ScopeKind  string // 释放维度：agent/thread、agent 全线程或指定 manager key。
	AgentID    string // agent 维度释放时的必填身份。
	ThreadID   string // 单线程释放时的必填身份。
	ManagerKey string // 指定 manager 释放时的精确缓存键。
	Drain      bool   // true 时只有无忙碌租约才视为完全 drain。
	Reason     string // 调用方提供的审计/日志原因。
}

// ReleaseScopeResult 汇总释放匹配、关闭和忙碌租约数量，供调用方判断是否需要重试。
type ReleaseScopeResult struct {
	MatchedManagers int      // 命中 release 条件的 manager 数。
	ClosedManagers  int      // 实际关闭的 manager 数。
	BusyLeases      int      // 因仍有租约而不能关闭的 manager 数。
	Drained         bool     // drain 请求是否已完全清空目标 manager。
	ScopeKeys       []string // 命中的 scope key 列表。
	ManagerKeys     []string // 命中的 manager key 列表。
}

// ScopeReleaser 是对外暴露的 LSP scope 释放能力，隐藏具体池实现。
type ScopeReleaser interface {
	ReleaseScope(req ReleaseScopeRequest) (ReleaseScopeResult, error)
}

// ReleaseScope 通过 manager 持有的池释放指定 LSP scope。
// 非池 manager 不能执行精确释放，会返回错误让调用方显式感知配置不完整。
func (m *manager) ReleaseScope(req ReleaseScopeRequest) (ReleaseScopeResult, error) {
	if m == nil || m.pool == nil {
		return ReleaseScopeResult{}, errors.New("LSP manager pool is nil")
	}
	return m.pool.ReleaseScope(req)
}

// ReleaseScope 从池中摘除匹配 scope 的 manager clone 并关闭可安全释放的实例。
// Drain 会先建立代际栅栏并摘除 busy manager；延后 cleanup 由受管 recycler 在最后租约释放后执行，不会中断进行中的请求。
func (p *ManagerPool) ReleaseScope(req ReleaseScopeRequest) (ReleaseScopeResult, error) {
	if p == nil {
		return ReleaseScopeResult{}, errors.New("LSP manager pool is nil")
	}
	req = normalizeReleaseScopeRequest(req)
	if err := validateReleaseScopeRequest(req); err != nil {
		return ReleaseScopeResult{}, err
	}
	p.lifecycleMu.RLock()
	defer p.lifecycleMu.RUnlock()
	if p.closing || p.closed {
		return ReleaseScopeResult{}, ErrManagerPoolClosed
	}
	p.releaseMu.Lock()
	defer p.releaseMu.Unlock()

	pendingResult, pendingErr := p.pendingReleaseScopeResult(req)
	result, toClose, pending := p.detachReleaseScopeManagers(req)
	mergeReleaseScopeResult(&result, pendingResult)
	p.rememberPendingReleases(pending)
	closeErr := p.closeDetachedReleaseManagers(req, toClose, &result)
	firstErr := errors.Join(pendingErr, closeErr)
	if req.Drain && result.BusyLeases == 0 && result.ClosedManagers == result.MatchedManagers && firstErr == nil {
		result.Drained = true
	}
	return result, firstErr
}

// pendingReleaseScopeResult 把已摘除但仍有租约的旧 manager 代际计入后续 drain 查询。
func (p *ManagerPool) pendingReleaseScopeResult(req ReleaseScopeRequest) (ReleaseScopeResult, error) {
	pending := p.snapshotPendingReleaseStates()
	var result ReleaseScopeResult
	var firstErr error
	var consumed []*manager
	for _, candidate := range pending {
		if candidate.manager == nil || !candidate.state.report || !releaseScopeMatches(req, candidate.state.scope) {
			continue
		}
		consume, closeErr := p.mergePendingReleaseState(&result, candidate)
		if consume {
			consumed = append(consumed, candidate.manager)
		}
		firstErr = errors.Join(firstErr, closeErr)
	}
	p.consumeSuccessfulReleaseReceipts(consumed)
	if firstErr != nil {
		firstErr = fmt.Errorf("release scope close failed: %w", firstErr)
	}
	return result, firstErr
}

type pendingReleaseSnapshot struct {
	manager *manager
	state   pendingManagerReleaseState
}

func (p *ManagerPool) snapshotPendingReleaseStates() []pendingReleaseSnapshot {
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	pending := make([]pendingReleaseSnapshot, 0, len(p.pendingReleases))
	for mgr, state := range p.pendingReleases {
		pending = append(pending, pendingReleaseSnapshot{manager: mgr, state: state})
	}
	return pending
}

func (p *ManagerPool) mergePendingReleaseState(result *ReleaseScopeResult, candidate pendingReleaseSnapshot) (bool, error) {
	recordReleaseScopeMatch(result, candidate.state.scope)
	if candidate.state.completed {
		result.ClosedManagers++
		return candidate.state.closeErr == nil, candidate.state.closeErr
	}
	result.BusyLeases += p.activeLeasesForManager(candidate.manager)
	return false, candidate.state.closeErr
}

// consumeSuccessfulReleaseReceipts 只消费已成功关闭且已向查询方返回的 receipt。
func (p *ManagerPool) consumeSuccessfulReleaseReceipts(managers []*manager) {
	if len(managers) == 0 {
		return
	}
	p.pendingMu.Lock()
	defer p.pendingMu.Unlock()
	for _, mgr := range managers {
		if state, ok := p.pendingReleases[mgr]; ok && state.completed && state.closeErr == nil {
			delete(p.pendingReleases, mgr)
		}
	}
}

// ReleaseManagerKey 释放指定 manager key 对应的 clone。
// 它使用 drain=true，确保返回前能暴露忙碌租约导致的未完全释放状态。
func (p *ManagerPool) ReleaseManagerKey(managerKey string) error {
	_, err := p.ReleaseScope(ReleaseScopeRequest{
		ScopeKind:  ReleaseScopeManagerKey,
		ManagerKey: managerKey,
		Drain:      true,
		Reason:     "release_manager_key",
	})
	return err
}

func (p *ManagerPool) detachReleaseScopeManagers(req ReleaseScopeRequest) (ReleaseScopeResult, []pendingManagerRelease, []pendingManagerRelease) {
	var result ReleaseScopeResult
	var toClose []pendingManagerRelease
	var pending []pendingManagerRelease
	seenClose := map[*manager]struct{}{}
	seenPending := map[*manager]struct{}{}
	for _, shard := range p.shards {
		shardResult, shardManagers, shardPending := p.detachReleaseScopeManagersFromShard(shard, req, seenClose, seenPending)
		mergeReleaseScopeResult(&result, shardResult)
		toClose = append(toClose, shardManagers...)
		pending = append(pending, shardPending...)
	}
	return result, toClose, pending
}

// detachReleaseScopeManagersFromShard 在单个 shard 锁内摘除可释放的 clone。
// drain 请求会先从 map 摘除 busy clone，并在最后租约释放后按 manager 指针代际关闭。
func (p *ManagerPool) detachReleaseScopeManagersFromShard(
	shard *managerShard,
	req ReleaseScopeRequest,
	seenClose map[*manager]struct{},
	seenPending map[*manager]struct{},
) (ReleaseScopeResult, []pendingManagerRelease, []pendingManagerRelease) {
	if shard == nil {
		return ReleaseScopeResult{}, nil, nil
	}
	var result ReleaseScopeResult
	var toClose []pendingManagerRelease
	var pending []pendingManagerRelease
	shard.mu.Lock()
	defer shard.mu.Unlock()
	for key, clone := range shard.clones {
		if !p.releaseScopeCanDetach(req, clone) {
			continue
		}
		recordReleaseScopeMatch(&result, clone.resolvedScope)
		busy, detach, idleBlocked := p.retireManagerForRelease(clone.manager, req.Drain)
		if busy > 0 {
			result.BusyLeases += busy
			if detach {
				delete(shard.clones, key)
				if _, ok := seenPending[clone.manager]; !ok {
					seenPending[clone.manager] = struct{}{}
					pending = append(pending, pendingManagerRelease{
						manager: clone.manager,
						scope:   clone.resolvedScope,
					})
				}
			}
			continue
		}
		if idleBlocked || !detach {
			continue
		}
		delete(shard.clones, key)
		if _, ok := seenClose[clone.manager]; !ok {
			seenClose[clone.manager] = struct{}{}
			toClose = append(toClose, pendingManagerRelease{
				manager: clone.manager,
				scope:   clone.resolvedScope,
			})
		}
	}
	return result, toClose, pending
}

// retireManagerForRelease 在 manager 写临界区内复核租约并建立摘除栅栏。
// 普通 scope release 只有全部 workspace 通过完整 IdleEligible 才能摘除；最后租约归零不构成立即关闭资格。
func (p *ManagerPool) retireManagerForRelease(mgr *manager, drain bool) (busy int, detach bool, idleBlocked bool) {
	if p == nil || mgr == nil {
		return 0, false, false
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	now := mgr.managerNow()
	busy, idleBlocked = p.releaseWorkspaceStatus(mgr, now)
	if busy > 0 && !drain {
		return busy, false, false
	}
	if idleBlocked && busy == 0 {
		return 0, false, true
	}
	mgr.retiring = true
	return busy, true, false
}

// releaseWorkspaceStatus 汇总 manager 内权威租约并标记未完成完整 idle window 的 workspace。
func (p *ManagerPool) releaseWorkspaceStatus(mgr *manager, now time.Time) (busy int, idleBlocked bool) {
	for _, workspace := range mgr.workspaces {
		if workspace == nil || workspace.client == nil {
			continue
		}
		active := p.activeLeasesForWorkspace(workspace)
		busy += active
		if active == 0 && !idleEligible(workspace, now, mgr.idleTimeout) {
			idleBlocked = true
		}
	}
	return busy, idleBlocked
}

func (p *ManagerPool) releaseScopeCanDetach(req ReleaseScopeRequest, clone *pooledManager) bool {
	return p != nil && clone != nil && clone.manager != nil && releaseScopeMatches(req, clone.resolvedScope)
}

func recordReleaseScopeMatch(result *ReleaseScopeResult, scope ResolvedLSPToolScope) {
	if result == nil {
		return
	}
	result.MatchedManagers++
	result.ScopeKeys = appendUniqueNonEmpty(result.ScopeKeys, scope.ScopeKey)
	result.ManagerKeys = appendUniqueNonEmpty(result.ManagerKeys, scope.ManagerKey)
}

func mergeReleaseScopeResult(dst *ReleaseScopeResult, src ReleaseScopeResult) {
	if dst == nil {
		return
	}
	dst.MatchedManagers += src.MatchedManagers
	dst.ClosedManagers += src.ClosedManagers
	dst.BusyLeases += src.BusyLeases
	dst.Drained = dst.Drained || src.Drained
	for _, scopeKey := range src.ScopeKeys {
		dst.ScopeKeys = appendUniqueNonEmpty(dst.ScopeKeys, scopeKey)
	}
	for _, managerKey := range src.ManagerKeys {
		dst.ManagerKeys = appendUniqueNonEmpty(dst.ManagerKeys, managerKey)
	}
}

// closeDetachedReleaseManagers 关闭本次摘除的 manager；失败状态写入 receipt，后续相同 drain 会继续重试。
func (p *ManagerPool) closeDetachedReleaseManagers(
	req ReleaseScopeRequest,
	releases []pendingManagerRelease,
	result *ReleaseScopeResult,
) error {
	var firstErr error
	for _, release := range releases {
		if release.manager == nil {
			continue
		}
		workspaces := snapshotWorkspaceClients(release.manager)
		if release.manager.logger != nil {
			args := recyclerManagerLogArgs(release.scope, release.manager, len(workspaces))
			args = append(args,
				"scope_kind", req.ScopeKind,
				"drain", req.Drain,
				"action", "close",
				"action_result", "started",
			)
			args = append(args, platformshared.SafePayloadLogFields("release_reason", req.Reason)...)
			release.manager.logger.Info("LSP release scope closing manager", args...)
		}
		done, closeErr := release.manager.closeWithoutPoolStatus()
		if done && closeErr == nil {
			if result != nil {
				result.ClosedManagers++
			}
			continue
		}
		p.rememberPendingReleaseState(release, done, closeErr)
		if closeErr != nil {
			logRecyclerCleanupFailure(release.manager, release.scope, workspaces, release.manager.managerNow(), "close", "release_scope", closeErr, "LSP release scope close failed")
		} else if !done {
			logRecyclerCleanupPending(release.manager, release.scope, workspaces, release.manager.managerNow(), "close", "release_scope", "LSP release scope close pending")
		}
		firstErr = errors.Join(firstErr, closeErr)
	}
	if firstErr != nil {
		return fmt.Errorf("release scope close failed: %w", firstErr)
	}
	return nil
}

func (p *ManagerPool) rememberPendingReleaseState(release pendingManagerRelease, completed bool, closeErr error) {
	if p == nil || release.manager == nil {
		return
	}
	p.pendingMu.Lock()
	p.pendingReleases[release.manager] = pendingManagerReleaseState{
		scope:     release.scope,
		completed: completed,
		closeErr:  closeErr,
		report:    true,
	}
	p.pendingMu.Unlock()
}

// closeDetachedPoolManagers 关闭 cap/TTL 摘除的 manager，并把失败 owner 留给 recycler 或 pool Close 重试。
func (p *ManagerPool) closeDetachedPoolManagers(releases []pendingManagerRelease, reason string) error {
	var firstErr error
	for _, release := range releases {
		if release.manager == nil {
			continue
		}
		workspaces := snapshotWorkspaceClients(release.manager)
		done, closeErr := release.manager.closeWithoutPoolStatus()
		if done && closeErr == nil {
			continue
		}
		p.pendingMu.Lock()
		p.pendingReleases[release.manager] = pendingManagerReleaseState{
			scope:     release.scope,
			completed: done,
			closeErr:  closeErr,
		}
		p.pendingMu.Unlock()
		if closeErr != nil {
			logRecyclerCleanupFailure(release.manager, release.scope, workspaces, release.manager.managerNow(), "close", reason, closeErr, "LSP detached manager close failed")
		} else if !done {
			logRecyclerCleanupPending(release.manager, release.scope, workspaces, release.manager.managerNow(), "close", reason, "LSP detached manager close pending")
		}
		firstErr = errors.Join(firstErr, closeErr)
	}
	if firstErr != nil {
		return fmt.Errorf("%s: %w", reason, firstErr)
	}
	return nil
}

func normalizeReleaseScopeRequest(req ReleaseScopeRequest) ReleaseScopeRequest {
	req.ScopeKind = strings.TrimSpace(req.ScopeKind)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.ManagerKey = strings.TrimSpace(req.ManagerKey)
	req.Reason = strings.TrimSpace(req.Reason)
	return req
}

// validateReleaseScopeRequest 校验 release 请求包含当前释放维度所需的身份字段。
// 缺少 scope kind 或必填 id 会立即报错，避免误释放更宽范围的 LSP manager。
func validateReleaseScopeRequest(req ReleaseScopeRequest) error {
	switch req.ScopeKind {
	case ReleaseScopeAgentThread:
		if req.AgentID == "" || req.ThreadID == "" {
			return errors.New("LSP ReleaseScope agent_thread requires agent ID and thread ID")
		}
	case ReleaseScopeAgentAllThreads:
		if req.AgentID == "" {
			return errors.New("LSP ReleaseScope agent_all_threads requires agent ID")
		}
	case ReleaseScopeManagerKey:
		if req.ManagerKey == "" {
			return errors.New("LSP ReleaseScope manager_key requires manager key")
		}
	default:
		return errors.New("LSP ReleaseScope scope kind is required")
	}
	return nil
}

func releaseScopeMatches(req ReleaseScopeRequest, scope ResolvedLSPToolScope) bool {
	switch req.ScopeKind {
	case ReleaseScopeAgentThread:
		return scope.AgentID == req.AgentID && scope.ThreadID == req.ThreadID
	case ReleaseScopeAgentAllThreads:
		return scope.AgentID == req.AgentID
	case ReleaseScopeManagerKey:
		return scope.ManagerKey == req.ManagerKey
	default:
		return false
	}
}

// activeLeasesForManager 汇总某个 manager 当前全部 workspace client 的活跃租约。
// 调用方用它决定 release/recycle 是否能安全关闭，不直接读取池内租约 map。
func (p *ManagerPool) activeLeasesForManager(mgr *manager) int {
	if p == nil || mgr == nil {
		return 0
	}
	mgr.mu.RLock()
	workspaces := make([]workspaceClient, 0, len(mgr.workspaces))
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client != nil {
			workspaces = append(workspaces, *workspace)
		}
	}
	mgr.mu.RUnlock()
	total := 0
	for i := range workspaces {
		total += p.activeLeasesForWorkspace(&workspaces[i])
	}
	return total
}

// activeLeasesForWorkspace 读取 manager workspace 的权威租约计数。
func (p *ManagerPool) activeLeasesForWorkspace(workspace *workspaceClient) int {
	if workspace == nil || workspace.client == nil {
		return 0
	}
	return workspace.activeLeases
}

func appendUniqueNonEmpty(dst []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(dst, value) {
		return dst
	}
	return append(dst, value)
}
