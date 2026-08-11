package multilsp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type workspaceBootstrapAttempt struct {
	mu           sync.RWMutex
	done         chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	err          error
	bootstrapped bool
	once         sync.Once
}

func newWorkspaceBootstrapAttempt(parent context.Context) *workspaceBootstrapAttempt {
	ctx, cancel := context.WithCancel(parent)
	return &workspaceBootstrapAttempt{done: make(chan struct{}), ctx: ctx, cancel: cancel}
}

func (a *workspaceBootstrapAttempt) finish(bootstrapped bool, err error) {
	if a == nil {
		return
	}
	a.once.Do(func() {
		a.mu.Lock()
		a.err = err
		a.bootstrapped = bootstrapped
		a.mu.Unlock()
		a.cancel()
		close(a.done)
	})
}

func (a *workspaceBootstrapAttempt) cancelBootstrap() {
	if a != nil && a.cancel != nil {
		a.cancel()
	}
}

func (a *workspaceBootstrapAttempt) wait(ctx context.Context) (bool, error) {
	if a == nil || a.done == nil {
		return false, ErrWorkspaceLifecycleInvalid
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-a.done:
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.bootstrapped, a.err
	}
}

type languageClientBootstrapCandidate struct {
	client  Client
	attempt *workspaceBootstrapAttempt
	owner   bool
	wait    bool
}

func (c languageClientBootstrapCandidate) bootstrapContext(caller context.Context) (context.Context, error) {
	if !c.owner {
		return caller, nil
	}
	if c.attempt == nil || c.attempt.ctx == nil {
		return nil, ErrWorkspaceLifecycleInvalid
	}
	return c.attempt.ctx, nil
}

func (m *manager) prepareLanguageClientBootstrap(
	ctx context.Context,
	cfg workspaceConfig,
) (languageClientBootstrapCandidate, error) {
	m.ensureMu.Lock()
	defer m.ensureMu.Unlock()
	if candidate, ok, err := m.waitingLanguageBootstrapCandidate(cfg); ok || err != nil {
		return candidate, err
	}
	client, err := m.ensureClientLocked(ctx, cfg)
	if err != nil {
		return languageClientBootstrapCandidate{}, err
	}
	return m.languageBootstrapCandidateForClient(cfg, client)
}

func (m *manager) waitingLanguageBootstrapCandidate(
	cfg workspaceConfig,
) (languageClientBootstrapCandidate, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.retiring {
		return languageClientBootstrapCandidate{}, true, ErrManagerClosed
	}
	workspace := m.workspaces[cfg.key]
	if workspace == nil || workspace.client == nil || workspace.state != workspaceStateBootstrapping {
		return languageClientBootstrapCandidate{}, false, nil
	}
	if workspace.bootstrapAttempt == nil {
		return languageClientBootstrapCandidate{}, true, ErrWorkspaceLifecycleInvalid
	}
	return languageClientBootstrapCandidate{
		client: workspace.client, attempt: workspace.bootstrapAttempt, wait: true,
	}, true, nil
}

func (m *manager) languageBootstrapCandidateForClient(
	cfg workspaceConfig,
	client Client,
) (languageClientBootstrapCandidate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	workspace := m.workspaces[cfg.key]
	if workspace == nil || workspace.client != client {
		return languageClientBootstrapCandidate{}, ErrClientNotBound
	}
	candidate := languageClientBootstrapCandidate{
		client: client, attempt: workspace.bootstrapAttempt, owner: workspace.state == workspaceStateBootstrapping,
	}
	if candidate.owner && candidate.attempt == nil {
		return languageClientBootstrapCandidate{}, ErrWorkspaceLifecycleInvalid
	}
	return candidate, nil
}

func (m *manager) completeLanguageClientBootstrap(
	ctx context.Context,
	cfg workspaceConfig,
	candidate languageClientBootstrapCandidate,
) (Client, error) {
	if candidate.wait {
		bootstrapped, err := candidate.attempt.wait(ctx)
		if err != nil {
			return nil, err
		}
		if bootstrapped {
			return candidate.client, m.revalidateLanguageBootstrapClient(cfg, candidate.client)
		}
		return m.ensureClientForLanguageConfig(ctx, cfg)
	}
	bootstrapCtx, err := candidate.bootstrapContext(ctx)
	if err != nil {
		return nil, err
	}
	bootstrapErr := m.bootstrapLanguageClient(bootstrapCtx, candidate.client, cfg.rootPath, cfg.languageID)
	if !candidate.owner {
		return candidate.client, bootstrapErr
	}
	return m.finishOwnedLanguageClientBootstrap(candidate, bootstrapErr)
}

func (m *manager) finishOwnedLanguageClientBootstrap(
	candidate languageClientBootstrapCandidate,
	bootstrapErr error,
) (Client, error) {
	if bootstrapErr != nil {
		result := errors.Join(bootstrapErr, m.abortUnpublishedClient(candidate.client))
		m.finishLanguageBootstrapAttempt(candidate.attempt, false, result)
		return nil, result
	}
	if err := m.publishClient(candidate.client); err != nil {
		result := errors.Join(err, m.abortUnpublishedClient(candidate.client))
		m.finishLanguageBootstrapAttempt(candidate.attempt, false, result)
		return nil, result
	}
	m.finishLanguageBootstrapAttempt(candidate.attempt, true, nil)
	return candidate.client, nil
}

func (m *manager) publishLanguageClientWithoutBootstrap(candidate languageClientBootstrapCandidate) error {
	if !candidate.owner {
		return nil
	}
	if err := m.publishClient(candidate.client); err != nil {
		result := errors.Join(err, m.abortUnpublishedClient(candidate.client))
		m.finishLanguageBootstrapAttempt(candidate.attempt, false, result)
		return result
	}
	m.finishLanguageBootstrapAttempt(candidate.attempt, false, nil)
	return nil
}

func (m *manager) finishLanguageBootstrapAttempt(attempt *workspaceBootstrapAttempt, bootstrapped bool, err error) {
	m.mu.Lock()
	delete(m.bootstrapAttempts, attempt)
	m.mu.Unlock()
	attempt.finish(bootstrapped, err)
}

func (m *manager) closeAndCancelLanguageBootstrapAttempts() []*workspaceBootstrapAttempt {
	m.mu.Lock()
	m.closed = true
	m.retiring = true
	attempts := make([]*workspaceBootstrapAttempt, 0, len(m.bootstrapAttempts))
	for attempt := range m.bootstrapAttempts {
		attempts = append(attempts, attempt)
	}
	m.mu.Unlock()
	for _, attempt := range attempts {
		attempt.cancelBootstrap()
	}
	return attempts
}

func waitForLanguageBootstrapAttempts(attempts []*workspaceBootstrapAttempt, timeout time.Duration) error {
	if len(attempts) == 0 {
		return nil
	}
	if timeout <= 0 {
		return fmt.Errorf("wait for canceled language bootstrap attempt: %w", context.DeadlineExceeded)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, attempt := range attempts {
		select {
		case <-attempt.done:
		case <-timer.C:
			return fmt.Errorf("wait for canceled language bootstrap attempt: %w", context.DeadlineExceeded)
		}
	}
	return nil
}

// lockEnsureMutexWithin 把 client 初始化锁的等待纳入 manager 关闭预算。
// Initialize 尚未注册 bootstrap attempt 时也必须有界返回，避免 Close 永久阻塞。
func lockEnsureMutexWithin(mu *sync.Mutex, timeout time.Duration) bool {
	if mu == nil {
		return false
	}
	if mu.TryLock() {
		return true
	}
	if timeout <= 0 {
		return false
	}
	interval := min(time.Millisecond, timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-ticker.C:
			if mu.TryLock() {
				return true
			}
		case <-timer.C:
			return false
		}
	}
}

func (m *manager) bootstrapAttemptTimeout() time.Duration {
	if m != nil && m.bootstrapAttemptWaitTimeout > 0 {
		return m.bootstrapAttemptWaitTimeout
	}
	return managerShutdownTimeout
}

func (m *manager) revalidateLanguageBootstrapClient(cfg workspaceConfig, client Client) error {
	_, err := m.workspaceClientIdentityForConfig(cfg, client)
	return err
}

func (m *manager) beginLanguageBootstrapDocument(
	ctx context.Context,
	identity workspaceClientIdentity,
	uri string,
	languageID string,
) (languageBootstrapDocumentKey, bool, error) {
	key := languageBootstrapDocumentKey{workspaceKey: identity.key, generation: identity.generation, uri: uri}
	for {
		m.explicitOpenMu.Lock()
		if m.explicitDocumentCoversBootstrapLocked(uri, identity.key, identity.generation, languageID) {
			m.explicitOpenMu.Unlock()
			return key, false, nil
		}
		if m.languageBootstrapDocs == nil {
			m.languageBootstrapDocs = make(map[languageBootstrapDocumentKey]*languageBootstrapDocumentState)
		}
		m.pruneOldLanguageBootstrapDocumentsLocked(key)
		state := m.languageBootstrapDocs[key]
		if state == nil {
			m.languageBootstrapDocs[key] = &languageBootstrapDocumentState{done: make(chan struct{})}
			m.explicitOpenMu.Unlock()
			return key, true, nil
		}
		if state.ready {
			m.explicitOpenMu.Unlock()
			return key, false, nil
		}
		done := state.done
		m.explicitOpenMu.Unlock()
		select {
		case <-ctx.Done():
			return key, false, ctx.Err()
		case <-done:
			if state.err != nil {
				return key, false, state.err
			}
		}
	}
}

func (m *manager) pruneOldLanguageBootstrapDocumentsLocked(current languageBootstrapDocumentKey) {
	for key, state := range m.languageBootstrapDocs {
		if key.workspaceKey != current.workspaceKey || key.uri != current.uri || key.generation == current.generation {
			continue
		}
		if state != nil && state.ready {
			delete(m.languageBootstrapDocs, key)
		}
	}
}

func (m *manager) explicitDocumentCoversBootstrapLocked(
	uri string,
	workspaceKey string,
	generation uint64,
	languageID string,
) bool {
	explicit, ok := m.explicitDocuments[uri]
	if !ok {
		return false
	}
	return explicit.configKey == workspaceKey &&
		explicit.clientGeneration == generation &&
		explicit.wireOpen &&
		explicit.languageID == languageID
}

func (m *manager) finishLanguageBootstrapDocument(key languageBootstrapDocumentKey, bootstrapErr error) {
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	state := m.languageBootstrapDocs[key]
	if state == nil || state.ready {
		return
	}
	state.err = bootstrapErr
	state.ready = bootstrapErr == nil
	close(state.done)
	if bootstrapErr != nil {
		delete(m.languageBootstrapDocs, key)
	}
}

func (m *manager) cancelLanguageBootstrapDocument(key languageBootstrapDocumentKey) {
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	state := m.languageBootstrapDocs[key]
	if state == nil || state.ready {
		return
	}
	close(state.done)
	delete(m.languageBootstrapDocs, key)
}

func (m *manager) languageBootstrapStillNeeded(key languageBootstrapDocumentKey, languageID string) bool {
	m.explicitOpenMu.RLock()
	defer m.explicitOpenMu.RUnlock()
	if explicit, ok := m.explicitDocuments[key.uri]; ok &&
		explicit.configKey == key.workspaceKey &&
		explicit.clientGeneration == key.generation &&
		explicit.wireOpen &&
		explicit.languageID == languageID {
		return false
	}
	state := m.languageBootstrapDocs[key]
	return state != nil && !state.ready
}
