package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/contextlock"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

type agentRegistry struct {
	mu                       contextlock.RWMutex
	agents                   map[string]*agentRuntime
	suppressedStoppedThreads sync.Map
	nextTurnSeq              int64
}

func newAgentRegistry() *agentRegistry {
	return &agentRegistry{agents: make(map[string]*agentRuntime)}
}

type agentIdentityKind int

const (
	agentIdentityLocalOnly agentIdentityKind = iota
	agentIdentityAny
)

func (r *agentRegistry) lock() {
	r.mu.Lock()
}

func (r *agentRegistry) unlock() {
	r.mu.Unlock()
}

func (r *agentRegistry) rLock() {
	r.mu.RLock()
}

func (r *agentRegistry) rUnlock() {
	r.mu.RUnlock()
}

func (r *agentRegistry) lockRead(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return r.mu.RLockCtx(ctx)
}

// lookupAgentByIDLocked 面向可信 hook/event 入口，允许用远端 agent/thread id 反查本地 runtime。
func (r *agentRegistry) lookupAgentByIDLocked(agentID string) (*agentState, error) {
	return r.lookupAgentByIdentityLocked(agentID, agentIdentityAny)
}

// lookupAgentByIdentityLocked 按调用方声明的信任范围查找 agent。
func (r *agentRegistry) lookupAgentByIdentityLocked(agentID string, kind agentIdentityKind) (*agentState, error) {
	agentID = strings.TrimSpace(agentID)
	if agent, ok := r.agents[agentID]; ok {
		return agent, nil
	}
	if kind == agentIdentityLocalOnly {
		return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
	}
	for _, candidate := range r.agents {
		if candidate.remoteAgentID == agentID || candidate.remoteThreadID == agentID {
			return candidate, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", errAgentNotFound, agentID)
}

func (r *agentRegistry) lookupAgentBySeqLocked(agentID string, launchSeq uint64) (*agentState, error) {
	agent, err := r.lookupAgentByIDLocked(agentID)
	if err != nil {
		return nil, err
	}
	if agent.launchSeq != launchSeq {
		return nil, fmt.Errorf("%w: %s/%d", errAgentNotFound, strings.TrimSpace(agentID), launchSeq)
	}
	return agent, nil
}

func (r *agentRegistry) withAgentLocked(agentID string, fn func(*agentState) error) error {
	r.lock()
	defer r.unlock()

	agent, err := r.lookupAgentByIDLocked(agentID)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (r *agentRegistry) withAgentReadLocked(agentID string, fn func(*agentState) error) error {
	r.rLock()
	defer r.rUnlock()

	agent, err := r.lookupAgentByIDLocked(agentID)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (r *agentRegistry) withAgentReadLockedByAgentID(ctx context.Context, agentID string, fn func(*agentState) error) error {
	if err := r.lockRead(ctx); err != nil {
		return err
	}
	defer r.rUnlock()

	agent, err := r.lookupAgentByIdentityLocked(agentID, agentIdentityLocalOnly)
	if err != nil {
		return err
	}
	return fn(agent)
}

func (r *agentRegistry) agentIDs() []string {
	r.rLock()
	defer r.rUnlock()

	ids := make([]string, 0, len(r.agents))
	for agentID := range r.agents {
		ids = append(ids, agentID)
	}
	return ids
}

func (r *agentRegistry) listAgents() []agentRuntime {
	r.rLock()
	defer r.rUnlock()

	agents := make([]agentRuntime, 0, len(r.agents))
	for _, agent := range r.agents {
		snapshot := *agent
		snapshot.queue = nil
		snapshot.sm = nil
		snapshot.exitedAt = shared.CloneTime(agent.exitedAt)
		agents = append(agents, snapshot)
	}
	return agents
}

func (r *agentRegistry) runtimeAgentSnapshots(
	ctx context.Context,
	snapshot func(context.Context, *agentRuntime) AgentSnapshot,
) ([]AgentSnapshot, error) {
	if err := r.lockRead(ctx); err != nil {
		return nil, err
	}
	defer r.rUnlock()

	snapshots := make([]AgentSnapshot, 0, len(r.agents))
	for _, agent := range r.agents {
		snapshots = append(snapshots, snapshot(ctx, agent))
	}
	return snapshots, nil
}

func (r *agentRegistry) agentForLaunchLocked(req LaunchRequest, newAgent func(string) *agentRuntime) *agentRuntime {
	agent, err := r.lookupAgentByIdentityLocked(req.AgentID, agentIdentityLocalOnly)
	if errors.Is(err, errAgentNotFound) {
		agent = newAgent(req.AgentID)
		r.agents[req.AgentID] = agent
	}
	applyLaunchRequestLocked(agent, req)
	resetRuntimeStateLocked(agent)
	clearAgentLifecycleErrorLocked(agent)
	clearAgentStopReasonLocked(agent)
	clearAgentAutoRecoveryLocked(agent)
	return agent
}

func (r *agentRegistry) requestedAgentLaunchInProgressLocked(agentID string, inProgress func(*agentRuntime) bool) *agentRuntime {
	agentID = strings.TrimSpace(agentID)
	for _, existing := range r.agents {
		if strings.TrimSpace(existing.requestedAgentID) == agentID && inProgress(existing) {
			return existing
		}
	}
	return nil
}

// rekeyLaunchedAgentLocked 把已启动 runtime 从临时本地 ID 改挂到远端返回的稳定 agent ID。
func (r *agentRegistry) rekeyLaunchedAgentLocked(agent *agentRuntime) error {
	if agent == nil {
		return nil
	}
	finalID := strings.TrimSpace(agent.remoteAgentID)
	if finalID == "" || finalID == agent.id {
		return nil
	}
	if existing, ok := r.agents[finalID]; ok && existing != agent {
		return fmt.Errorf("orchestration: remote agent_id %q collides with local agent %q", finalID, existing.id)
	}
	delete(r.agents, agent.id)
	agent.id = finalID
	r.agents[finalID] = agent
	return nil
}

func (r *agentRegistry) addRehydratedRuntimeAgent(agent *agentRuntime) bool {
	r.lock()
	defer r.unlock()
	if _, err := r.lookupAgentByIDLocked(agent.id); err == nil {
		return false
	}
	r.agents[agent.id] = agent
	return true
}

func (r *agentRegistry) hasRuntimeAgent(agentID string) bool {
	r.rLock()
	defer r.rUnlock()
	_, err := r.lookupAgentByIDLocked(agentID)
	return err == nil
}

// ownerAgentIDForThreadID 通过当前 runtime 的本地或远端 thread 绑定反查唯一 owner。
func (r *agentRegistry) ownerAgentIDForThreadID(threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", errors.New("remote terminal thread id is required")
	}
	r.rLock()
	defer r.rUnlock()
	ownerID := ""
	for _, candidate := range r.agents {
		if candidate == nil || (strings.TrimSpace(candidate.threadID) != threadID && strings.TrimSpace(candidate.remoteThreadID) != threadID) {
			continue
		}
		candidateID := strings.TrimSpace(candidate.id)
		if candidateID == "" {
			return "", errors.New("remote terminal thread owner has empty agent id")
		}
		if ownerID != "" && ownerID != candidateID {
			return "", fmt.Errorf("remote terminal thread %q has multiple owners", threadID)
		}
		ownerID = candidateID
	}
	if ownerID == "" {
		return "", fmt.Errorf("%w: remote thread %s", errAgentNotFound, threadID)
	}
	return ownerID, nil
}

func (r *agentRegistry) turnIDFor(sub TurnSubmission) string {
	if turnID := strings.TrimSpace(sub.ExpectedTurnID); turnID != "" {
		return turnID
	}
	baseID := strings.TrimSpace(sub.ThreadID)
	if baseID == "" {
		baseID = strings.TrimSpace(sub.AgentID)
	}
	if baseID == "" {
		baseID = "turn"
	}
	r.nextTurnSeq++
	return fmt.Sprintf("%s-turn-%d", baseID, r.nextTurnSeq)
}

func (r *agentRegistry) suppressStoppedHookThreadLocked(threadID string) {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		r.suppressedStoppedThreads.Store(threadID, stoppedHookSuppression{permanent: true})
	}
}

func (r *agentRegistry) suppressStoppedHookThreadUntilLocked(threadID string, beforeOrAt time.Time) {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		r.suppressedStoppedThreads.Store(threadID, stoppedHookSuppression{beforeOrAt: beforeOrAt})
	}
}

func (r *agentRegistry) stoppedHookThreadSuppressed(threadID string, timestamp time.Time) bool {
	raw, ok := r.suppressedStoppedThreads.Load(strings.TrimSpace(threadID))
	if !ok {
		return false
	}
	suppression, ok := raw.(stoppedHookSuppression)
	if !ok || suppression.permanent {
		return true
	}
	return !timestamp.IsZero() && !timestamp.After(suppression.beforeOrAt)
}
