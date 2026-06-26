package multilsp

import (
	"errors"
	"fmt"
	"slices"
	"strings"
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
// 仍有活跃租约的 manager 只计入 BusyLeases，不会被强制关闭；Drain 用于调用方判断是否需要重试。
func (p *ManagerPool) ReleaseScope(req ReleaseScopeRequest) (ReleaseScopeResult, error) {
	if p == nil {
		return ReleaseScopeResult{}, errors.New("LSP manager pool is nil")
	}
	req = normalizeReleaseScopeRequest(req)
	if err := validateReleaseScopeRequest(req); err != nil {
		return ReleaseScopeResult{}, err
	}

	result, toClose := p.detachReleaseScopeManagers(req)
	closed, firstErr := closeReleaseScopeManagers(req, toClose)
	result.ClosedManagers += closed
	if req.Drain && result.BusyLeases == 0 {
		result.Drained = true
	}
	return result, firstErr
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

func (p *ManagerPool) detachReleaseScopeManagers(req ReleaseScopeRequest) (ReleaseScopeResult, []*manager) {
	var result ReleaseScopeResult
	var toClose []*manager
	seenClose := map[*manager]struct{}{}
	for _, shard := range p.shards {
		shardResult, shardManagers := p.detachReleaseScopeManagersFromShard(shard, req, seenClose)
		mergeReleaseScopeResult(&result, shardResult)
		toClose = append(toClose, shardManagers...)
	}
	return result, toClose
}

// detachReleaseScopeManagersFromShard 在单个 shard 锁内摘除可释放的 clone。
// 有活跃租约的 clone 只记录 busy，不从 map 删除，避免正在使用的 client 被关闭。
func (p *ManagerPool) detachReleaseScopeManagersFromShard(shard *managerShard, req ReleaseScopeRequest, seen map[*manager]struct{}) (ReleaseScopeResult, []*manager) {
	if shard == nil {
		return ReleaseScopeResult{}, nil
	}
	var result ReleaseScopeResult
	var toClose []*manager
	shard.mu.Lock()
	defer shard.mu.Unlock()
	for key, clone := range shard.clones {
		if !p.releaseScopeCanDetach(req, clone) {
			continue
		}
		recordReleaseScopeMatch(&result, clone.resolvedScope)
		if busy := p.activeLeasesForManager(clone.manager); busy > 0 {
			result.BusyLeases += busy
			continue
		}
		delete(shard.clones, key)
		if _, ok := seen[clone.manager]; !ok {
			seen[clone.manager] = struct{}{}
			toClose = append(toClose, clone.manager)
		}
	}
	return result, toClose
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

// closeReleaseScopeManagers 关闭已从池中摘除的 manager 列表。
// 函数继续尝试关闭全部目标，只包装第一个错误返回，防止一个失败阻断其余资源清理。
func closeReleaseScopeManagers(req ReleaseScopeRequest, managers []*manager) (int, error) {
	var firstErr error
	closed := 0
	for _, mgr := range managers {
		if mgr == nil {
			continue
		}
		if mgr.logger != nil {
			mgr.logger.Info("LSP release scope closing manager",
				"scope_kind", req.ScopeKind,
				"agent_id", req.AgentID,
				"thread_id", req.ThreadID,
				"manager_key", req.ManagerKey,
				"drain", req.Drain,
				"reason", req.Reason,
			)
		}
		if err := mgr.closeWithoutPool(); err != nil && firstErr == nil {
			firstErr = err
		}
		closed++
	}
	if firstErr != nil {
		return closed, fmt.Errorf("release scope close failed: %w", firstErr)
	}
	return closed, nil
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
	clients := make([]Client, 0, len(mgr.workspaces))
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client != nil {
			clients = append(clients, workspace.client)
		}
	}
	mgr.mu.RUnlock()
	total := 0
	for _, client := range clients {
		total += p.activeLeases(client)
	}
	return total
}

func appendUniqueNonEmpty(dst []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || slices.Contains(dst, value) {
		return dst
	}
	return append(dst, value)
}
