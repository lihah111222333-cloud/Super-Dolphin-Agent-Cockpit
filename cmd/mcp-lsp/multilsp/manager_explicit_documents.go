package multilsp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// explicitDocumentState 是 manager 内显式打开文档的唯一运行时真值源。
// 它只属于 concrete multilsp manager，不进入公共 Manager、DTO 或持久化契约。
type explicitDocumentState struct {
	uri              string
	absPath          string
	languageID       string
	configKey        string
	configRoot       string
	scopeKey         string
	workspaceKey     string
	clientGeneration uint64
	lspVersion       int
	text             string
	textFingerprint  string
	diskFingerprint  string
	fileBacked       bool
	diskReadable     bool
	diskClean        bool
	fullTextKnown    bool
	userOpened       bool
	wireOpen         bool
}

type workspaceClientIdentity struct {
	key        string
	generation uint64
}

type languageBootstrapDocumentKey struct {
	workspaceKey string
	generation   uint64
	uri          string
}

type languageBootstrapDocumentState struct {
	done  chan struct{}
	ready bool
	err   error
}

type explicitDocumentSyncUpdate struct {
	before explicitDocumentState
	after  explicitDocumentState
	text   string
	open   bool
}

type explicitDocumentOpenPlan struct {
	before    explicitDocumentState
	hadBefore bool
	version   int
	change    bool
}

type explicitDocumentMembershipSnapshot struct {
	states []explicitDocumentState
	key    string
	epoch  uint64
	busy   int
}

func (m *manager) workspaceClientIdentityForConfig(cfg workspaceConfig, expected Client) (workspaceClientIdentity, error) {
	if m == nil || expected == nil {
		return workspaceClientIdentity{}, ErrClientNotBound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.retiring {
		return workspaceClientIdentity{}, ErrManagerClosed
	}
	workspace := m.workspaces[cfg.key]
	if workspace == nil || workspace.client != expected || workspace.generation == 0 {
		return workspaceClientIdentity{}, ErrClientNotBound
	}
	return workspaceClientIdentity{key: cfg.key, generation: workspace.generation}, nil
}

func (m *manager) workspaceClientConfigAndIdentity(expected Client) (workspaceConfig, workspaceClientIdentity, error) {
	if m == nil || expected == nil {
		return workspaceConfig{}, workspaceClientIdentity{}, ErrClientNotBound
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key, workspace := range m.workspaces {
		if workspace == nil || workspace.client != expected || workspace.generation == 0 {
			continue
		}
		return workspaceConfigFromClient(*workspace), workspaceClientIdentity{
			key: key, generation: workspace.generation,
		}, nil
	}
	return workspaceConfig{}, workspaceClientIdentity{}, ErrClientNotBound
}

func explicitDocumentStateForOpen(
	ref documentRef,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
	version int,
	text string,
) explicitDocumentState {
	snapshot, snapshotErr := readDocumentSnapshot(ref)
	state := explicitDocumentStateForText(ref, cfg, scope, identity, version, text)
	if snapshotErr == nil {
		state.diskFingerprint = snapshot.fingerprint
		state.diskReadable = true
		state.diskClean = snapshot.text == text
	}
	if !state.fileBacked || !state.diskClean {
		state.text = text
		state.fullTextKnown = true
	}
	return state
}

func explicitDocumentStateForText(
	ref documentRef,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
	version int,
	text string,
) explicitDocumentState {
	return explicitDocumentState{
		uri:              ref.uri,
		absPath:          ref.absPath,
		languageID:       ref.languageID,
		configKey:        cfg.key,
		configRoot:       cfg.rootPath,
		scopeKey:         scope.ScopeKey,
		workspaceKey:     scope.WorkspaceKey,
		clientGeneration: identity.generation,
		lspVersion:       version,
		textFingerprint:  hashDocument([]byte(text)),
		fileBacked:       strings.HasPrefix(ref.uri, "file://") && ref.absPath != "",
		wireOpen:         true,
	}
}

func explicitDocumentStateForSnapshot(
	snapshot documentSnapshot,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
	version int,
) explicitDocumentState {
	state := explicitDocumentStateForText(snapshot.ref, cfg, scope, identity, version, snapshot.text)
	if !state.fileBacked {
		state.text = snapshot.text
		state.fullTextKnown = true
		return state
	}
	state.textFingerprint = snapshot.fingerprint
	state.diskFingerprint = snapshot.fingerprint
	state.diskReadable = true
	state.diskClean = true
	return state
}

func (m *manager) planExplicitDocumentOpen(
	uri string,
	cfg workspaceConfig,
	identity workspaceClientIdentity,
) explicitDocumentOpenPlan {
	m.explicitOpenMu.RLock()
	defer m.explicitOpenMu.RUnlock()
	state, ok := m.explicitDocuments[uri]
	plan := explicitDocumentOpenPlan{before: state, hadBefore: ok}
	if ok && state.configKey == cfg.key && state.clientGeneration == identity.generation &&
		state.languageID == cfg.languageID && state.wireOpen {
		plan.version = state.lspVersion
		plan.change = true
		return plan
	}
	if ok {
		plan.version = state.lspVersion
		return plan
	}
	key := languageBootstrapDocumentKey{workspaceKey: cfg.key, generation: identity.generation, uri: uri}
	bootstrap := m.languageBootstrapDocs[key]
	if bootstrap != nil && bootstrap.ready {
		plan.change = true
	}
	return plan
}

func (m *manager) prepareExplicitDocumentOpen(
	ref documentRef,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
	requestedVersion int,
	text string,
) (explicitDocumentState, bool, *explicitDocumentReservation, error) {
	plan := m.planExplicitDocumentOpen(ref.uri, cfg, identity)
	version := requestedVersion
	if version <= plan.version {
		version = plan.version + 1
	}
	state := explicitDocumentStateForOpen(ref, cfg, scope, identity, version, text)
	state.userOpened = true
	reservation, err := m.reserveExplicitDocumentState(plan.before, plan.hadBefore, state)
	if err != nil {
		return explicitDocumentState{}, false, nil, err
	}
	return state, plan.change, reservation, nil
}

func sendExplicitDocumentOpen(
	ctx context.Context,
	client Client,
	state explicitDocumentState,
	change bool,
	text string,
) error {
	if !change {
		return client.DidOpen(ctx, state.uri, state.languageID, state.lspVersion, text)
	}
	return client.DidChange(ctx, state.uri, state.lspVersion, []protocol.TextDocumentContentChangeEvent{{Text: text}})
}

func (m *manager) prepareExplicitDocumentChange(
	ref documentRef,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
	requestedVersion int,
	text string,
	full bool,
) (*explicitDocumentReservation, int, bool, error) {
	m.explicitOpenMu.RLock()
	state, ok := m.explicitDocuments[ref.uri]
	m.explicitOpenMu.RUnlock()
	if !ok {
		return nil, requestedVersion, false, nil
	}
	if state.configKey != cfg.key || state.languageID != cfg.languageID || state.clientGeneration != identity.generation {
		return nil, 0, true, fmt.Errorf(
			"managed document %s belongs to client generation %d, current generation %d",
			ref.uri,
			state.clientGeneration,
			identity.generation,
		)
	}
	if !state.wireOpen {
		return nil, 0, true, fmt.Errorf("managed document wire is closed; DidOpen is required: %s", ref.uri)
	}
	version := requestedVersion
	if version <= state.lspVersion {
		if !full {
			return nil, 0, true, fmt.Errorf(
				"incremental change version %d for managed document %s is not newer than current version %d",
				requestedVersion,
				ref.uri,
				state.lspVersion,
			)
		}
		version = state.lspVersion + 1
	}
	after := explicitDocumentStateAfterChange(state, ref, cfg, scope, identity, version, text, full)
	reservation, err := m.reserveExplicitDocumentState(state, true, after)
	if err != nil {
		return nil, 0, true, err
	}
	return reservation, version, true, nil
}

func explicitDocumentStateAfterChange(
	state explicitDocumentState,
	ref documentRef,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
	version int,
	text string,
	full bool,
) explicitDocumentState {
	state.configKey = cfg.key
	state.configRoot = cfg.rootPath
	state.scopeKey = scope.ScopeKey
	state.workspaceKey = scope.WorkspaceKey
	state.clientGeneration = identity.generation
	state.lspVersion = version
	if full {
		return explicitDocumentStateAfterFullChange(state, ref, text)
	}
	state.diskClean = false
	state.fullTextKnown = false
	state.text = ""
	state.textFingerprint = ""
	return state
}

func explicitDocumentStateAfterFullChange(
	state explicitDocumentState,
	ref documentRef,
	text string,
) explicitDocumentState {
	state.textFingerprint = hashDocument([]byte(text))
	if snapshot, err := readDocumentSnapshot(ref); err == nil {
		state.diskReadable = true
		state.diskFingerprint = snapshot.fingerprint
		state.diskClean = snapshot.text == text
	} else {
		state.diskReadable = false
		state.diskFingerprint = ""
		state.diskClean = false
	}
	if state.fileBacked && state.diskClean {
		state.text = ""
		state.fullTextKnown = false
		return state
	}
	state.text = text
	state.fullTextKnown = true
	return state
}

func (m *manager) removeExplicitDocument(uri string) {
	if m == nil || strings.TrimSpace(uri) == "" {
		return
	}
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	delete(m.explicitDocuments, uri)
	for key, state := range m.languageBootstrapDocs {
		if key.uri == uri && state != nil && state.ready {
			delete(m.languageBootstrapDocs, key)
		}
	}
}

func (m *manager) isExplicitDocumentOpen(uri string) bool {
	if m == nil || strings.TrimSpace(uri) == "" {
		return false
	}
	m.explicitOpenMu.RLock()
	defer m.explicitOpenMu.RUnlock()
	state, ok := m.explicitDocuments[uri]
	return ok && state.userOpened
}

func (m *manager) explicitDocumentForURI(uri string) (explicitDocumentState, bool) {
	if m == nil || strings.TrimSpace(uri) == "" {
		return explicitDocumentState{}, false
	}
	m.explicitOpenMu.RLock()
	defer m.explicitOpenMu.RUnlock()
	state, ok := m.explicitDocuments[uri]
	return state, ok
}

func (m *manager) clientForExplicitDocument(state explicitDocumentState) (Client, bool) {
	if m == nil || state.configKey == "" || state.clientGeneration == 0 {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	workspace := m.workspaces[state.configKey]
	if workspace == nil || workspace.client == nil || workspace.generation != state.clientGeneration {
		return nil, false
	}
	return workspace.client, true
}

func (m *manager) explicitDocumentsForWorkspaceSymbol(
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
) explicitDocumentMembershipSnapshot {
	m.explicitOpenMu.RLock()
	defer m.explicitOpenMu.RUnlock()
	states := make([]explicitDocumentState, 0, len(m.explicitDocuments))
	for _, state := range m.explicitDocuments {
		if state.languageID != cfg.languageID || state.configKey != cfg.key || state.configRoot != cfg.rootPath {
			continue
		}
		if state.scopeKey != scope.ScopeKey || state.workspaceKey != scope.WorkspaceKey {
			continue
		}
		states = append(states, state)
	}
	sort.Slice(states, func(left, right int) bool { return states[left].uri < states[right].uri })
	key := explicitDocumentMembershipKeyForScope(cfg, scope)
	return explicitDocumentMembershipSnapshot{
		states: states,
		key:    key,
		epoch:  m.explicitMembershipEpoch[key],
		busy:   m.explicitMembershipBusy[key],
	}
}

// syncExplicitDocumentsForWorkspaceSymbol 在 workspace/symbol 前同步当前 manager 的精确显式文档集合。
// 磁盘读取、LSP 通知和等待均不持有 manager/cache/ensure/显式文档锁。
func (m *manager) syncExplicitDocumentsForWorkspaceSymbol(
	ctx context.Context,
	cfg workspaceConfig,
	client Client,
) (*workspaceSymbolDocumentGuard, error) {
	identity, err := m.workspaceClientIdentityForConfig(cfg, client)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace symbol client identity: %w", err)
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace symbol document scope: %w", err)
	}
	membership := m.explicitDocumentsForWorkspaceSymbol(cfg, scope)
	if membership.busy != 0 {
		return nil, fmt.Errorf("managed document membership is changing during workspace symbol preparation")
	}
	updates, err := m.prepareExplicitDocumentSyncUpdates(ctx, membership.states, identity)
	if err != nil {
		return nil, err
	}
	guard, err := m.acquireWorkspaceSymbolDocumentGuard(ctx, membership)
	if err != nil {
		return nil, err
	}
	if err := m.validateWorkspaceSymbolDocumentGuard(cfg, client, membership.states, guard); err != nil {
		guard.release()
		return nil, err
	}
	if err := m.applyExplicitDocumentSyncUpdates(ctx, client, updates); err != nil {
		guard.release()
		return nil, err
	}
	return guard, nil
}

func (m *manager) acquireWorkspaceSymbolDocumentGuard(
	ctx context.Context,
	membership explicitDocumentMembershipSnapshot,
) (*workspaceSymbolDocumentGuard, error) {
	guard := &workspaceSymbolDocumentGuard{
		manager: m, membershipKey: membership.key, membershipEpoch: membership.epoch,
		tokens: make([]*documentOperationToken, 0, len(membership.states)),
	}
	for _, state := range membership.states {
		token, err := m.beginDocumentOperation(ctx, state.uri, documentOperationObserveSync)
		if err != nil {
			guard.release()
			return nil, err
		}
		guard.tokens = append(guard.tokens, token)
	}
	return guard, nil
}

func (m *manager) validateWorkspaceSymbolDocumentGuard(
	cfg workspaceConfig,
	client Client,
	states []explicitDocumentState,
	guard *workspaceSymbolDocumentGuard,
) error {
	if _, err := m.workspaceClientIdentityForConfig(cfg, client); err != nil {
		return fmt.Errorf("revalidate workspace symbol client identity: %w", err)
	}
	for _, state := range states {
		if !m.explicitDocumentStateUnchanged(state) {
			return fmt.Errorf("explicit document changed during workspace symbol preparation: %s", state.uri)
		}
	}
	if !guard.membershipStillCurrent() {
		return fmt.Errorf("managed document membership changed during workspace symbol preparation")
	}
	return nil
}

func (m *manager) applyExplicitDocumentSyncUpdates(
	ctx context.Context,
	client Client,
	updates []explicitDocumentSyncUpdate,
) error {
	sent := make([]explicitDocumentSyncUpdate, 0, len(updates))
	callbackErr, releaseErr := m.callWithPooledClient(client, func() error {
		for _, update := range updates {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !m.explicitDocumentStateUnchanged(update.before) {
				return fmt.Errorf("explicit document changed during workspace symbol sync: %s", update.before.uri)
			}
			if err := applyExplicitDocumentSyncUpdate(ctx, client, update); err != nil {
				return fmt.Errorf("sync explicit workspace document %s: %w", update.before.uri, err)
			}
			sent = append(sent, update)
		}
		return nil
	})
	if releaseErr != nil {
		return errors.Join(callbackErr, releaseErr)
	}
	return errors.Join(callbackErr, m.commitExplicitDocumentSyncUpdatesForRecipient(client, sent))
}

func (m *manager) prepareExplicitDocumentSyncUpdates(
	ctx context.Context,
	states []explicitDocumentState,
	identity workspaceClientIdentity,
) ([]explicitDocumentSyncUpdate, error) {
	if err := m.preflightExplicitDocumentSyncReads(states); err != nil {
		return nil, err
	}
	return m.prepareExplicitDocumentSyncUpdatesAfterPreflight(ctx, states, identity)
}

func (m *manager) prepareExplicitDocumentSyncUpdatesAfterPreflight(
	ctx context.Context,
	states []explicitDocumentState,
	identity workspaceClientIdentity,
) ([]explicitDocumentSyncUpdate, error) {
	updates := make([]explicitDocumentSyncUpdate, 0, len(states))
	var readBytes int64
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		update, needed, size, err := prepareExplicitDocumentSyncUpdate(
			state, identity.generation, m.effectiveCleanDocumentByteLimit(),
		)
		if err != nil {
			return nil, err
		}
		readBytes += size
		if readBytes > m.effectiveCleanRefreshByteLimit() {
			return nil, fmt.Errorf("clean workspace document actual aggregate exceeds read limit %d", m.effectiveCleanRefreshByteLimit())
		}
		if needed {
			updates = append(updates, update)
		}
	}
	return updates, nil
}

func prepareExplicitDocumentSyncUpdate(
	state explicitDocumentState,
	currentGeneration uint64,
	byteLimit int64,
) (explicitDocumentSyncUpdate, bool, int64, error) {
	if state.clientGeneration != currentGeneration {
		return prepareExplicitDocumentReplacementOpenWithLimit(state, currentGeneration, byteLimit)
	}
	if !state.wireOpen {
		return prepareExplicitDocumentReplacementOpenWithLimit(state, currentGeneration, byteLimit)
	}
	if state.fileBacked && !state.diskReadable {
		return explicitDocumentSyncUpdate{}, false, 0, fmt.Errorf("explicit workspace document disk state is unreadable: %s", state.uri)
	}
	if !state.fileBacked || !state.diskClean {
		return explicitDocumentSyncUpdate{}, false, 0, nil
	}
	snapshot, err := readDocumentSnapshotWithLimit(documentRef{
		raw: state.uri, uri: state.uri, absPath: state.absPath, languageID: state.languageID,
	}, byteLimit)
	if err != nil {
		return explicitDocumentSyncUpdate{}, false, 0, fmt.Errorf("read explicit workspace document %s: %w", state.uri, err)
	}
	if snapshot.fingerprint == state.diskFingerprint {
		return explicitDocumentSyncUpdate{}, false, snapshot.size, nil
	}
	after := state
	after.lspVersion++
	after.text = ""
	after.textFingerprint = snapshot.fingerprint
	after.diskFingerprint = snapshot.fingerprint
	after.diskReadable = true
	after.diskClean = true
	after.fullTextKnown = false
	return explicitDocumentSyncUpdate{before: state, after: after, text: snapshot.text}, true, snapshot.size, nil
}

func prepareExplicitDocumentReplacementOpen(
	state explicitDocumentState,
	currentGeneration uint64,
) (explicitDocumentSyncUpdate, bool, error) {
	update, needed, _, err := prepareExplicitDocumentReplacementOpenWithLimit(
		state, currentGeneration, defaultCleanDocumentByteLimit,
	)
	return update, needed, err
}

func prepareExplicitDocumentReplacementOpenWithLimit(
	state explicitDocumentState,
	currentGeneration uint64,
	byteLimit int64,
) (explicitDocumentSyncUpdate, bool, int64, error) {
	after := state
	after.clientGeneration = currentGeneration
	after.lspVersion++
	after.wireOpen = true
	if state.fileBacked && !state.diskReadable {
		return explicitDocumentSyncUpdate{}, false, 0, fmt.Errorf("replacement workspace document disk state is unreadable: %s", state.uri)
	}
	text := state.text
	var readBytes int64
	if state.fileBacked && state.diskClean {
		snapshot, err := readDocumentSnapshotWithLimit(documentRef{
			raw: state.uri, uri: state.uri, absPath: state.absPath, languageID: state.languageID,
		}, byteLimit)
		if err != nil {
			return explicitDocumentSyncUpdate{}, false, 0, fmt.Errorf("read replacement workspace document %s: %w", state.uri, err)
		}
		readBytes = snapshot.size
		text = snapshot.text
		after.text = ""
		after.textFingerprint = snapshot.fingerprint
		after.diskFingerprint = snapshot.fingerprint
		after.diskReadable = true
		after.fullTextKnown = false
	} else if !state.fullTextKnown {
		return explicitDocumentSyncUpdate{}, false, 0, fmt.Errorf("cannot restore dirty incremental document %s to replacement client", state.uri)
	}
	return explicitDocumentSyncUpdate{before: state, after: after, text: text, open: true}, true, readBytes, nil
}

func applyExplicitDocumentSyncUpdate(ctx context.Context, client Client, update explicitDocumentSyncUpdate) error {
	if update.open {
		return client.DidOpen(
			ctx,
			update.after.uri,
			update.after.languageID,
			update.after.lspVersion,
			update.text,
		)
	}
	return client.DidChange(ctx, update.after.uri, update.after.lspVersion, []protocol.TextDocumentContentChangeEvent{{
		Text: update.text,
	}})
}

func (m *manager) explicitDocumentStateUnchanged(expected explicitDocumentState) bool {
	m.explicitOpenMu.RLock()
	defer m.explicitOpenMu.RUnlock()
	current, ok := m.explicitDocuments[expected.uri]
	return ok && current == expected
}

func (m *manager) commitExplicitDocumentSyncUpdatesForRecipient(
	client Client,
	updates []explicitDocumentSyncUpdate,
) error {
	if len(updates) == 0 {
		return nil
	}
	m.mu.RLock()
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	defer m.mu.RUnlock()
	expected := updates[0].after
	if err := m.validateExplicitDocumentRecipientLocked(client, expected); err != nil {
		return err
	}
	if err := m.validateExplicitDocumentSyncUpdatesLocked(expected, updates); err != nil {
		return err
	}
	for _, update := range updates {
		m.explicitDocuments[update.after.uri] = update.after
	}
	return nil
}

func (m *manager) validateExplicitDocumentSyncUpdatesLocked(
	expected explicitDocumentState,
	updates []explicitDocumentSyncUpdate,
) error {
	for _, update := range updates {
		current, ok := m.explicitDocuments[update.before.uri]
		ownerChanged := update.after.configKey != expected.configKey ||
			update.after.clientGeneration != expected.clientGeneration
		if !ok || current != update.before || ownerChanged {
			return fmt.Errorf("explicit document changed while committing workspace symbol sync: %s", update.before.uri)
		}
	}
	return nil
}

func (m *manager) bootstrapLanguageDocument(ctx context.Context, client Client, target, languageID string) error {
	cfg, identity, err := m.workspaceClientConfigAndIdentity(client)
	if err != nil {
		return fmt.Errorf("resolve bootstrap %s client identity: %w", languageID, err)
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("resolve bootstrap %s document scope: %w", languageID, err)
	}
	targetURI := fileURIFromPath(target)
	operation, err := m.beginDocumentOperation(ctx, targetURI, documentOperationObserveBootstrap)
	if err != nil {
		return err
	}
	defer operation.release()
	key, shouldOpen, err := m.beginLanguageBootstrapDocument(ctx, identity, targetURI, languageID)
	if err != nil {
		return fmt.Errorf("prepare bootstrap %s document %s: %w", languageID, target, err)
	}
	if !shouldOpen {
		return nil
	}
	return m.openLanguageBootstrapDocument(ctx, client, key, target, languageID, cfg, scope, identity, operation)
}

func (m *manager) openLanguageBootstrapDocument(
	ctx context.Context,
	client Client,
	key languageBootstrapDocumentKey,
	target string,
	languageID string,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
	operation *documentOperationToken,
) error {
	if !m.languageBootstrapStillNeeded(key, languageID) {
		m.finishLanguageBootstrapDocument(key, nil)
		return nil
	}
	if !operation.bootstrapSendAllowed() {
		m.cancelLanguageBootstrapDocument(key)
		return nil
	}
	ref := documentRef{raw: key.uri, uri: key.uri, absPath: target, languageID: languageID}
	state, before, hadBefore, text, err := m.prepareLanguageBootstrapOpen(ref, cfg, scope, identity)
	if err != nil {
		bootstrapErr := fmt.Errorf("prepare bootstrap %s document %s: %w", languageID, target, err)
		m.finishLanguageBootstrapDocument(key, bootstrapErr)
		return bootstrapErr
	}
	reservation, err := m.reserveExplicitDocumentState(before, hadBefore, state)
	if err != nil {
		bootstrapErr := fmt.Errorf("reserve bootstrap %s document %s: %w", languageID, target, err)
		m.finishLanguageBootstrapDocument(key, bootstrapErr)
		return bootstrapErr
	}
	err = m.withBootstrapPooledClient(client, func() error {
		return client.DidOpen(ctx, key.uri, languageID, state.lspVersion, text)
	})
	if err != nil {
		reservation.cancel()
		bootstrapErr := fmt.Errorf("bootstrap %s DidOpen %s: %w", languageID, target, err)
		m.finishLanguageBootstrapDocument(key, bootstrapErr)
		return bootstrapErr
	}
	if err := reservation.commitForRecipient(client); err != nil {
		closeErr := m.withBootstrapPooledClient(client, func() error { return client.DidClose(ctx, key.uri) })
		bootstrapErr := fmt.Errorf("record bootstrap %s document %s: %w", languageID, target, errors.Join(err, closeErr))
		m.finishLanguageBootstrapDocument(key, bootstrapErr)
		return bootstrapErr
	}
	m.finishLanguageBootstrapDocument(key, nil)
	return nil
}

func (m *manager) prepareLanguageBootstrapOpen(
	ref documentRef,
	cfg workspaceConfig,
	scope ResolvedLSPToolScope,
	identity workspaceClientIdentity,
) (explicitDocumentState, explicitDocumentState, bool, string, error) {
	before, hadBefore := m.explicitDocumentForURI(ref.uri)
	if hadBefore {
		if before.configKey != cfg.key || before.languageID != ref.languageID {
			return explicitDocumentState{}, before, true, "", fmt.Errorf("managed document belongs to another workspace or language")
		}
		update, needed, err := prepareExplicitDocumentReplacementOpen(before, identity.generation)
		if err != nil {
			return explicitDocumentState{}, before, true, "", err
		}
		if !needed {
			return explicitDocumentState{}, before, true, "", ErrStaleClientLease
		}
		return update.after, before, true, update.text, nil
	}
	snapshot, err := readDocumentSnapshot(ref)
	if err != nil {
		return explicitDocumentState{}, explicitDocumentState{}, false, "", err
	}
	text := snapshot.text
	state := explicitDocumentStateForSnapshot(snapshot, cfg, scope, identity, 0)
	return state, explicitDocumentState{}, false, text, nil
}
