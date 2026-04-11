package difftracker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrMissingAgentID = errors.New("difftracker: missing agent ID")

type MergeRequest struct {
	AgentID  string
	ThreadID string
	CallID   string
	ToolName string
	RepoRoot string
	DiffText string
	Files    []string
}

type AggregatorOption func(*DiffAggregator)

type DiffAggregator struct {
	mu            sync.Mutex
	sessions      map[string]*agentDiffSession
	ttl           time.Duration
	sweepInterval time.Duration
	now           func() time.Time
	emitter       DiffEmitter
	stopCh        chan struct{}
	doneCh        chan struct{}
	running       bool
}

type agentDiffSession struct {
	mu           sync.Mutex
	agentID      string
	threadID     string
	repoRoot     string
	files        map[string]*fileDiff
	revision     int64
	processedIDs map[string]bool
	lastActivity time.Time
	refCount     int
}

func NewDiffAggregator(options ...AggregatorOption) *DiffAggregator {
	aggregator := &DiffAggregator{
		sessions:      make(map[string]*agentDiffSession),
		ttl:           DefaultSessionTTL,
		sweepInterval: DefaultSweepInterval,
		now:           time.Now,
	}
	for _, option := range options {
		option(aggregator)
	}
	return aggregator
}

func withSessionTTL(ttl time.Duration) AggregatorOption {
	return func(aggregator *DiffAggregator) {
		aggregator.ttl = ttl
	}
}

func withSweepInterval(interval time.Duration) AggregatorOption {
	return func(aggregator *DiffAggregator) {
		aggregator.sweepInterval = interval
	}
}

func WithEmitter(emitter DiffEmitter) AggregatorOption {
	return func(aggregator *DiffAggregator) {
		aggregator.emitter = emitter
	}
}

func (a *DiffAggregator) Start() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running || a.sweepInterval <= 0 {
		return
	}
	a.stopCh = make(chan struct{})
	a.doneCh = make(chan struct{})
	a.running = true
	go a.runSweeper(a.stopCh, a.doneCh)
}

func (a *DiffAggregator) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if !a.running {
		a.sessions = make(map[string]*agentDiffSession)
		a.mu.Unlock()
		return
	}
	stopCh := a.stopCh
	doneCh := a.doneCh
	a.stopCh = nil
	a.doneCh = nil
	a.running = false
	a.sessions = make(map[string]*agentDiffSession)
	a.mu.Unlock()
	close(stopCh)
	<-doneCh
}

func (a *DiffAggregator) Merge(ctx context.Context, agentID, callID, toolName string, result any, resolver WorkDirResolver) error {
	req, err := a.buildMergeRequest(ctx, agentID, callID, toolName, result, resolver)
	if err != nil {
		req, err = fallbackGitMergeRequest(ctx, agentID, callID, toolName, resolver, err)
	}
	if err != nil || req == nil {
		return err
	}
	merged, changed, err := a.mergeRequest(*req)
	if err != nil || !changed || merged == nil || a.emitter == nil {
		return err
	}
	return a.emitter(ctx, *merged)
}

func fallbackGitMergeRequest(ctx context.Context, agentID, callID, toolName string, resolver WorkDirResolver, cause error) (*MergeRequest, error) {
	if !errors.Is(cause, ErrReplaceRangePatchNotFound) {
		return nil, cause
	}
	pkglogger.Warn("difftracker: hook diff extraction failed, falling back to git diff",
		"agent_id", strings.TrimSpace(agentID),
		"call_id", strings.TrimSpace(callID),
		"tool", strings.TrimSpace(toolName),
		"error", cause,
	)
	meta := readToolCallContext(ctx)
	if meta.Snapshot == nil {
		snapshot, err := beginFallbackGitSnapshot(ctx, resolver, agentID)
		if err != nil {
			return nil, err
		}
		meta.Snapshot = snapshot
	}
	return buildGitMergeRequest(ctx, agentID, callID, toolName, meta), nil
}

func beginFallbackGitSnapshot(ctx context.Context, resolver WorkDirResolver, agentID string) (*Snapshot, error) {
	if resolver == nil || strings.TrimSpace(agentID) == "" {
		return nil, nil
	}
	cwd, err := resolveAgentCWD(ctx, resolver, agentID)
	if err != nil || strings.TrimSpace(cwd) == "" {
		return nil, err
	}
	snapshot, err := BeginSnapshot(ctx, cwd)
	if err != nil {
		if errors.Is(err, ErrNotGitRepository) {
			return nil, nil
		}
		return nil, err
	}
	snapshot.beforeFiles = nil
	snapshot.DirtyFiles = nil
	return snapshot, nil
}

func (a *DiffAggregator) buildMergeRequest(
	ctx context.Context,
	agentID, callID, toolName string,
	result any,
	resolver WorkDirResolver,
) (*MergeRequest, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, ErrMissingAgentID
	}
	meta := readToolCallContext(ctx)
	req, err := buildHookMergeRequest(ctx, agentID, callID, toolName, result, resolver, meta)
	if req != nil || err != nil {
		return req, err
	}
	return buildGitMergeRequest(ctx, agentID, callID, toolName, meta), nil
}

func (a *DiffAggregator) mergeRequest(req MergeRequest) (*DiffResult, bool, error) {
	if req.AgentID == "" {
		return nil, false, ErrMissingAgentID
	}
	session := a.sessionFor(req)
	defer a.releaseSession(session)

	session.mu.Lock()
	defer session.mu.Unlock()
	session.threadID = req.ThreadID

	if req.CallID != "" && session.processedIDs[req.CallID] {
		return session.snapshot(req), false, nil
	}
	if req.CallID != "" {
		session.processedIDs[req.CallID] = true
	}
	changed := mergeIntoSession(session, req.DiffText, req.Files)
	if changed {
		session.revision++
	}
	return session.snapshot(req), changed, nil
}

func (a *DiffAggregator) CleanupAgent(agentID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, agentID)
}

func (a *DiffAggregator) sessionFor(req MergeRequest) *agentDiffSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.sessions[req.AgentID]
	if current == nil || current.repoRoot != req.RepoRoot {
		current = &agentDiffSession{
			agentID:      req.AgentID,
			threadID:     req.ThreadID,
			repoRoot:     req.RepoRoot,
			files:        make(map[string]*fileDiff),
			processedIDs: make(map[string]bool),
		}
		a.sessions[req.AgentID] = current
	}
	current.lastActivity = a.now()
	current.refCount++
	return current
}

func (a *DiffAggregator) releaseSession(session *agentDiffSession) {
	if a == nil || session == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if session.refCount > 0 {
		session.refCount--
	}
	session.lastActivity = a.now()
}

func (a *DiffAggregator) runSweeper(stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer close(doneCh)
	ticker := time.NewTicker(a.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			a.sweepExpired()
		case <-stopCh:
			return
		}
	}
}

func (a *DiffAggregator) sweepExpired() {
	if a.ttl <= 0 {
		return
	}
	cutoff := a.now().Add(-a.ttl)
	a.mu.Lock()
	defer a.mu.Unlock()
	for agentID, session := range a.sessions {
		if session.refCount > 0 {
			continue
		}
		if session.lastActivity.Before(cutoff) {
			delete(a.sessions, agentID)
		}
	}
}

func (s *agentDiffSession) snapshot(req MergeRequest) *DiffResult {
	return &DiffResult{
		AgentID:  s.agentID,
		ThreadID: s.threadID,
		CallID:   req.CallID,
		ToolName: req.ToolName,
		RepoRoot: s.repoRoot,
		DiffText: buildCumulativeDiff(s),
		Files:    sessionFilePaths(s),
		Revision: s.revision,
	}
}
