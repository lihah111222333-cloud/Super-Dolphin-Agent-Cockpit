package difftracker

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrMissingAgentID = errors.New("difftracker: agent id is required")

type MergeRequest struct {
	AgentID  string
	ThreadID string
	CallID   string
	ToolName string
	RepoRoot string
	DiffText string
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
}

func New(options ...AggregatorOption) *DiffAggregator {
	aggregator := &DiffAggregator{
		sessions:      make(map[string]*agentDiffSession),
		ttl:           DefaultSessionTTL,
		sweepInterval: DefaultSweepInterval,
		now:           time.Now,
	}
	for _, option := range options {
		option(aggregator)
	}
	aggregator.Start()
	return aggregator
}

func NewDiffAggregator(options ...AggregatorOption) *DiffAggregator {
	return New(options...)
}

func WithSessionTTL(ttl time.Duration) AggregatorOption {
	return func(aggregator *DiffAggregator) {
		aggregator.ttl = ttl
	}
}

func WithSweepInterval(interval time.Duration) AggregatorOption {
	return func(aggregator *DiffAggregator) {
		aggregator.sweepInterval = interval
	}
}

func WithNow(now func() time.Time) AggregatorOption {
	return func(aggregator *DiffAggregator) {
		if now != nil {
			aggregator.now = now
		}
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

func (a *DiffAggregator) Close() {
	a.Stop()
}

func (a *DiffAggregator) Merge(ctx context.Context, agentID, callID, toolName string, result any, resolver WorkDirResolver) error {
	req, err := a.buildMergeRequest(ctx, agentID, callID, toolName, result, resolver)
	if err != nil || req == nil {
		return err
	}
	merged, changed, err := a.mergeRequest(*req)
	if err != nil || merged == nil || !changed || a.emitter == nil {
		return err
	}
	return a.emitter(ctx, *merged)
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
	return buildGitMergeRequest(agentID, callID, toolName, meta), nil
}

func (a *DiffAggregator) mergeRequest(req MergeRequest) (*DiffResult, bool, error) {
	if req.AgentID == "" {
		return nil, false, ErrMissingAgentID
	}
	session := a.sessionFor(req)
	session.mu.Lock()
	defer session.mu.Unlock()
	session.threadID = req.ThreadID
	session.lastActivity = a.now()
	if req.CallID != "" && session.processedIDs[req.CallID] {
		return session.snapshot(req), false, nil
	}
	changed := mergeIntoSession(session, req.DiffText)
	if req.CallID != "" {
		session.processedIDs[req.CallID] = true
	}
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
			lastActivity: a.now(),
		}
		a.sessions[req.AgentID] = current
	}
	return current
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
		session.mu.Lock()
		expired := session.lastActivity.Before(cutoff)
		session.mu.Unlock()
		if expired {
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
