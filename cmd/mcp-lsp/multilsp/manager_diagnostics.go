package multilsp

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type managerNotificationHandler struct {
	publishDiagnostics func(protocol.PublishDiagnosticsParams) error
	logMessage         func(protocol.LogMessageParams) error
}

type diagnosticState string

const (
	diagnosticStateReady diagnosticState = "ready"
)

type diagnosticStoreKey struct {
	scopeKey     string
	workspaceKey string
	uri          string
}

type diagnosticFilter struct {
	keys          map[string]struct{}
	scopeKey      string
	workspaceRoot string
	all           bool
}

type diagnosticMetadata struct {
	fingerprint string
	mtimeNS     int64
	size        int64
}

type lspScopeContextKey string

const (
	lspScopeAgentIDContextKey  lspScopeContextKey = "lsp_agent_id"
	lspScopeThreadIDContextKey lspScopeContextKey = "lsp_thread_id"
)

func (h managerNotificationHandler) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	return h.publishDiagnostics(params)
}

func (h managerNotificationHandler) LogMessage(params protocol.LogMessageParams) error {
	return h.logMessage(params)
}

func (m *manager) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	filter, err := m.normalizeDiagnosticFilter(ctx, uris)
	if err != nil {
		return nil, err
	}
	m.cleanupDeletedDiagnostics(ctx, filter)

	items := m.currentDiagnostics(filter)
	sort.Slice(items, func(i, j int) bool {
		return items[i].URI < items[j].URI
	})
	return items, nil
}

func (m *manager) WaitDiagnosticsStable(ctx context.Context, uris []string) error {
	if err := sleepContext(ctx, m.diagInitial); err != nil {
		return err
	}
	filter, err := m.normalizeDiagnosticFilter(ctx, uris)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(m.diagMaxWait)
	lastUpdate := m.latestDiagnosticUpdate(filter)
	for {
		if time.Now().After(deadline) {
			return nil
		}
		if lastUpdate.IsZero() || time.Since(lastUpdate) >= m.diagPoll {
			return nil
		}
		waitFor := minDuration(m.diagPoll, time.Until(deadline))
		if err := sleepContext(ctx, waitFor); err != nil {
			return err
		}
		if next := m.latestDiagnosticUpdate(filter); next.After(lastUpdate) {
			lastUpdate = next
		}
	}
}

func (m *manager) CurrentDiagnosticGeneration() uint64 {
	return m.diagGeneration.Load()
}

func (m *manager) AdvanceDiagnosticGeneration() uint64 {
	next := m.diagGeneration.Add(1)
	m.diagMu.Lock()
	clear(m.diagnostics)
	m.diagMu.Unlock()
	return next
}

func (m *manager) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	return m.publishDiagnosticsForGeneration(params, m.CurrentDiagnosticGeneration())
}

func (m *manager) publishDiagnosticsForGeneration(params protocol.PublishDiagnosticsParams, capturedGen uint64) error {
	m.diagMu.Lock()
	defer m.diagMu.Unlock()
	if capturedGen < m.CurrentDiagnosticGeneration() {
		return nil
	}

	scope := m.scopeForPublishedDiagnostics(params.URI)
	key := diagnosticStoreKeyFor(scope, params.URI)
	if len(params.Diagnostics) == 0 {
		delete(m.diagnostics, key.String())
		if scope.ScopeKey == "" && scope.WorkspaceKey == "" {
			m.deleteDiagnosticsByURI(params.URI)
		}
		return nil
	}

	metadata := diagnosticMetadataForURI(params.URI)
	m.diagnostics[key.String()] = diagnosticSnapshot{
		scopeKey:     scope.ScopeKey,
		workspaceKey: scope.WorkspaceKey,
		language:     scope.LanguageID,
		uri:          params.URI,
		generation:   capturedGen,
		fingerprint:  metadata.fingerprint,
		mtimeNS:      metadata.mtimeNS,
		size:         metadata.size,
		updatedAt:    time.Now(),
		source:       "publish",
		state:        diagnosticStateReady,
		params:       params,
	}
	return nil
}

func (m *manager) latestDiagnosticUpdate(filter diagnosticFilter) time.Time {
	var latest time.Time
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		if snapshot.updatedAt.After(latest) {
			latest = snapshot.updatedAt
		}
	})
	return latest
}

func (m *manager) normalizeDiagnosticFilter(ctx context.Context, uris []string) (diagnosticFilter, error) {
	if len(uris) == 0 {
		return diagnosticFilter{
			scopeKey:      lspScopeKeyFromContext(ctx),
			workspaceRoot: m.effectiveWorkspaceRoot(ctx),
			all:           true,
		}, nil
	}

	keys := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		if strings.TrimSpace(uri) == "" {
			continue
		}
		ref, _, scope, err := m.resolvedScopeForURI(ctx, uri, "")
		if err != nil {
			return diagnosticFilter{}, err
		}
		keys[diagnosticStoreKeyFor(scope, ref.uri).String()] = struct{}{}
		if _, err := os.Stat(ref.absPath); err != nil && os.IsNotExist(err) {
			m.cleanupDeletedDocument(ctx, ref, scope)
		}
	}
	return diagnosticFilter{keys: keys}, nil
}

func (m *manager) currentDiagnostics(filter diagnosticFilter) []protocol.PublishDiagnosticsParams {
	items := make([]protocol.PublishDiagnosticsParams, 0, len(m.diagnostics))
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		items = append(items, snapshot.params)
	})
	return items
}

func (m *manager) forEachCurrentDiagnosticSnapshot(filter diagnosticFilter, fn func(diagnosticSnapshot)) {
	if m == nil || fn == nil {
		return
	}
	withReadLock(&m.diagMu, func() struct{} {
		current := m.CurrentDiagnosticGeneration()
		for key, snapshot := range m.diagnostics {
			if snapshot.generation != current || !filter.matches(key, snapshot) {
				continue
			}
			fn(snapshot)
		}
		return struct{}{}
	})
}

func (f diagnosticFilter) matches(key string, snapshot diagnosticSnapshot) bool {
	if len(f.keys) > 0 {
		_, ok := f.keys[key]
		return ok
	}
	if !f.all {
		return false
	}
	if snapshot.scopeKey != f.scopeKey {
		return false
	}
	if strings.TrimSpace(f.workspaceRoot) == "" {
		return true
	}
	path, err := absolutePathFromURI(snapshot.uri)
	if err != nil {
		return false
	}
	return platformshared.ContainsPath(f.workspaceRoot, path)
}

func (m *manager) cleanupDeletedDiagnostics(ctx context.Context, filter diagnosticFilter) {
	snapshots := make([]diagnosticSnapshot, 0)
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		if snapshot.uri == "" {
			return
		}
		if _, err := os.Stat(pathFromDiagnosticURI(snapshot.uri)); err != nil && os.IsNotExist(err) {
			snapshots = append(snapshots, snapshot)
		}
	})
	for _, snapshot := range snapshots {
		current := lspResolvedScope{
			ScopeKey:      snapshot.scopeKey,
			WorkspaceKey:  snapshot.workspaceKey,
			ManagerKey:    managerKeyFor(snapshot.scopeKey, snapshot.workspaceKey),
			WorkspaceRoot: pathOrEmpty(snapshot.uri),
			LanguageID:    snapshot.language,
		}
		if _, _, scope, err := m.resolvedScopeForURI(ctx, snapshot.uri, snapshot.language); err == nil {
			current = scope
		}
		m.cleanupDocumentForScopes(snapshot.uri, current)
	}
}

func (m *manager) cleanupDeletedDocument(_ context.Context, ref documentRef, current lspResolvedScope) {
	scopes := []lspResolvedScope{current}
	if indexed, ok := bootstrapCoordinatorFor(m).cache.LastResolvedScope(ref.uri); ok {
		scopes = append(scopes, indexed.LastResolvedScope)
	}
	m.cleanupDocumentForScopes(ref.uri, scopes...)
	bootstrapCoordinatorFor(m).cache.RememberDocumentScope(ref.uri, current, "")
}

func (m *manager) cleanupDocumentForScopes(uri string, scopes ...lspResolvedScope) {
	if strings.TrimSpace(uri) == "" {
		return
	}
	coordinator := bootstrapCoordinatorFor(m)
	seen := map[string]struct{}{}

	m.diagMu.Lock()
	for _, scope := range scopes {
		key := diagnosticStoreKeyFor(scope, uri)
		delete(m.diagnostics, key.String())
		seen[key.String()] = struct{}{}
		coordinator.cache.Tombstone(scope.cacheKey(scope.LanguageID, uri))
		coordinator.states.delete(scope.bootstrapKey(), uri)
	}
	for key, snapshot := range m.diagnostics {
		if snapshot.uri != uri {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		delete(m.diagnostics, key)
	}
	m.diagMu.Unlock()
}

func (m *manager) scopeForPublishedDiagnostics(uri string) lspResolvedScope {
	if indexed, ok := bootstrapCoordinatorFor(m).cache.LastResolvedScope(uri); ok {
		return indexed.LastResolvedScope
	}
	_, _, scope, err := m.resolvedScopeForURI(context.TODO(), uri, "")
	if err != nil {
		return lspResolvedScope{LanguageID: languageFromURI(uri)}
	}
	return scope
}

func (m *manager) resolvedScopeForURI(ctx context.Context, uri, languageID string) (documentRef, workspaceConfig, lspResolvedScope, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, languageID)
	if err != nil {
		return documentRef{}, workspaceConfig{}, lspResolvedScope{}, err
	}
	cfg, err := m.workspaceConfigForDiagnosticRef(ctx, ref)
	if err != nil {
		return documentRef{}, workspaceConfig{}, lspResolvedScope{}, err
	}
	return ref, cfg, m.resolvedScopeForConfig(ctx, cfg), nil
}

func (m *manager) workspaceConfigForDiagnosticRef(ctx context.Context, ref documentRef) (workspaceConfig, error) {
	if shouldUseClientForLanguage(ref.languageID) {
		return m.resolveWorkspaceForDocument(ctx, ref)
	}
	root := m.effectiveWorkspaceRoot(ctx)
	if root == "" {
		root = filepath.Dir(ref.absPath)
	}
	normalized, err := platformshared.NormalizeAbsolutePath(root)
	if err != nil {
		return workspaceConfig{}, err
	}
	return workspaceConfig{
		key:        normalized,
		rootPath:   normalized,
		rootURI:    fileURIFromPath(normalized),
		languageID: ref.languageID,
	}, nil
}

func (m *manager) resolvedScopeForConfig(ctx context.Context, cfg workspaceConfig) lspResolvedScope {
	scopeKey := lspScopeKeyFromContext(ctx)
	workspaceKey := workspaceKeyForConfig(cfg)
	return lspResolvedScope{
		ScopeKey:      scopeKey,
		WorkspaceKey:  workspaceKey,
		ManagerKey:    managerKeyFor(scopeKey, workspaceKey),
		WorkspaceRoot: cfg.rootPath,
		LanguageID:    normalizeLanguageID(cfg.languageID),
	}
}

func workspaceKeyForConfig(cfg workspaceConfig) string {
	return strings.Join([]string{
		normalizeLanguageID(cfg.languageID),
		filepath.Clean(cfg.key),
		filepath.Clean(cfg.rootPath),
		cfg.rootURI,
	}, "\x00")
}

func managerKeyFor(scopeKey, workspaceKey string) string {
	if scopeKey == "" {
		return workspaceKey
	}
	return scopeKey + "\x00" + workspaceKey
}

func lspScopeKeyFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if scope, ok := common.ToolScopeFromContext(ctx); ok {
		if scope.AgentID == "" && scope.ThreadID == "" {
			return ""
		}
		family := normalizeScopeFamily(scope.Family)
		return family + "\x00" + scope.AgentID + "\x00" + scope.ThreadID
	}
	agentID := firstContextString(ctx, lspScopeAgentIDContextKey, "_agentId", "agent_id")
	threadID := firstContextString(ctx, lspScopeThreadIDContextKey, "_threadId", "thread_id")
	if agentID == "" && threadID == "" {
		return ""
	}
	return "lsp\x00" + agentID + "\x00" + threadID
}

func firstContextString(ctx context.Context, keys ...any) string {
	for _, key := range keys {
		value, _ := ctx.Value(key).(string)
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func diagnosticStoreKeyFor(scope lspResolvedScope, uri string) diagnosticStoreKey {
	return diagnosticStoreKey{scopeKey: scope.ScopeKey, workspaceKey: scope.WorkspaceKey, uri: uri}
}

func (k diagnosticStoreKey) String() string {
	return k.scopeKey + "\x00" + k.workspaceKey + "\x00" + k.uri
}

func (m *manager) deleteDiagnosticsByURI(uri string) {
	for key, snapshot := range m.diagnostics {
		if snapshot.uri == uri || snapshot.params.URI == uri {
			delete(m.diagnostics, key)
		}
	}
}

func diagnosticMetadataForURI(uri string) diagnosticMetadata {
	path := pathFromDiagnosticURI(uri)
	info, err := os.Stat(path)
	if err != nil {
		return diagnosticMetadata{}
	}
	payload, err := os.ReadFile(path)
	fingerprint := ""
	if err == nil {
		fingerprint = hashDocument(payload)
	}
	return diagnosticMetadata{
		fingerprint: fingerprint,
		mtimeNS:     info.ModTime().UnixNano(),
		size:        info.Size(),
	}
}

func pathFromDiagnosticURI(uri string) string {
	path, err := absolutePathFromURI(uri)
	if err != nil {
		return uri
	}
	return path
}

func pathOrEmpty(uri string) string {
	path, err := absolutePathFromURI(uri)
	if err != nil {
		return ""
	}
	return filepath.Dir(path)
}

func languageFromURI(uri string) string {
	path, err := absolutePathFromURI(uri)
	if err != nil {
		return ""
	}
	return normalizeLanguageID(lspmanager.DetectLanguageID(path))
}

func minDuration(left, right time.Duration) time.Duration {
	if right <= 0 {
		return left
	}
	if left < right {
		return left
	}
	return right
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
