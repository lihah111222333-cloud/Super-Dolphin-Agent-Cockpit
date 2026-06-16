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

// ReleaseScopeRequest carries input for multilsp operations.
type ReleaseScopeRequest struct {
	ScopeKind  string
	AgentID    string
	ThreadID   string
	ManagerKey string
	Drain      bool
	Reason     string
}

// ReleaseScopeResult contains output returned by multilsp operations.
type ReleaseScopeResult struct {
	MatchedManagers int
	ClosedManagers  int
	BusyLeases      int
	Drained         bool
	ScopeKeys       []string
	ManagerKeys     []string
}

// ScopeReleaser describes a multilsp API type.
type ScopeReleaser interface {
	ReleaseScope(req ReleaseScopeRequest) (ReleaseScopeResult, error)
}

// ReleaseScope 处理release作用域。
func (m *manager) ReleaseScope(req ReleaseScopeRequest) (ReleaseScopeResult, error) {
	if m == nil || m.pool == nil {
		return ReleaseScopeResult{}, errors.New("LSP manager pool is nil")
	}
	return m.pool.ReleaseScope(req)
}

// ReleaseScope 处理release作用域。
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

// ReleaseManagerKey 处理releasemanager键。
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

// detachReleaseScopeManagersFromShard 从shard处理detachrelease作用域managers。
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

// closeReleaseScopeManagers 关闭release作用域managers。
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

// validateReleaseScopeRequest 校验release作用域请求。
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

// activeLeasesForManager 为manager处理activeleases。
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
