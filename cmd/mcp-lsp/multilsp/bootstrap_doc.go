package multilsp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
)

const (
	maxSiblingBootstrap   = 20
	maxRefreshFiles       = 50
	maxRefreshConcurrency = 8
)

type bootstrapCoordinator struct {
	cache  *lspCacheStore
	states *bootstrapStateStore
}

type documentSnapshot struct {
	ref         documentRef
	text        string
	size        int64
	fingerprint string
	modTimeNano int64
}

func (m *manager) hasStaleDiagnosticsForSnapshot(scope ResolvedLSPToolScope, doc documentSnapshot) bool {
	return m.matchingStaleDiagnosticsForSnapshot(scope, doc, nil)
}

func (m *manager) deleteStaleDiagnosticsForSnapshot(scope ResolvedLSPToolScope, doc documentSnapshot) {
	m.matchingStaleDiagnosticsForSnapshot(scope, doc, func(key string) {
		delete(m.diagnostics, key)
	})
}

// matchingStaleDiagnosticsForSnapshot 找出当前文档快照已失效的诊断记录。
// visit 非空时会在持锁状态下回调匹配 key，用于同步删除旧诊断。
func (m *manager) matchingStaleDiagnosticsForSnapshot(scope ResolvedLSPToolScope, doc documentSnapshot, visit func(string)) bool {
	if m == nil || strings.TrimSpace(doc.ref.uri) == "" {
		return false
	}
	return withWriteLock(&m.diagMu, func() bool {
		current := m.CurrentDiagnosticGeneration()
		found := false
		for key, snapshot := range m.diagnostics {
			if snapshot.generation != current || snapshot.uri != doc.ref.uri {
				continue
			}
			if !diagnosticSnapshotMatchesRefreshScope(snapshot, scope) || !diagnosticSnapshotStaleForDocument(snapshot, doc) {
				continue
			}
			found = true
			if visit != nil {
				visit(key)
			}
		}
		return found
	})
}

func diagnosticSnapshotMatchesRefreshScope(snapshot diagnosticSnapshot, scope ResolvedLSPToolScope) bool {
	if snapshot.scopeKey == "" && snapshot.workspaceKey == "" {
		return true
	}
	return snapshot.scopeKey == scope.ScopeKey && snapshot.workspaceKey == scope.WorkspaceKey
}

// diagnosticSnapshotStaleForDocument 比较诊断快照与磁盘文档快照是否仍一致。
// 优先用 fingerprint，其次用 size/mtime，缺少元数据时保持保守不判 stale。
func diagnosticSnapshotStaleForDocument(snapshot diagnosticSnapshot, doc documentSnapshot) bool {
	if snapshot.fingerprint != "" && doc.fingerprint != "" {
		return snapshot.fingerprint != doc.fingerprint
	}
	if snapshot.size > 0 && doc.size > 0 && snapshot.size != doc.size {
		return true
	}
	return snapshot.mtimeNS > 0 && doc.modTimeNano > 0 && snapshot.mtimeNS != doc.modTimeNano
}

func (m *manager) bootstrapDocument(ctx context.Context, uri string) error {
	ref, cfg, err := m.bootstrapTarget(ctx, uri)
	if err != nil {
		return err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	if err := coordinator.syncDocument(ctx, m, cfg, ref); err != nil {
		return err
	}
	coordinator.refreshWorkspace(ctx, m, cfg, ref.uri)
	coordinator.bootstrapSiblings(ctx, m, cfg, ref)
	return nil
}

func (m *manager) bootstrapDocumentOpenOnly(ctx context.Context, uri string) error {
	ref, cfg, err := m.bootstrapTarget(ctx, uri)
	if err != nil {
		return err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	snapshot, err := readDocumentSnapshot(ref)
	if err != nil {
		return err
	}
	return coordinator.openSnapshotIfNeeded(ctx, m, cfg, snapshot)
}

func restoreBootstrappedWorkspace(ctx context.Context, m *manager, cfg workspaceConfig) error {
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	coordinator.states.reset(scope.bootstrapKey(), coordinator.cache.ScopeURIs(scope))
	coordinator.refreshWorkspace(ctx, m, cfg, "")
	return nil
}

func bootstrapCoordinatorFor(m *manager) (*bootstrapCoordinator, error) {
	m.coordinatorMu.Lock()
	defer m.coordinatorMu.Unlock()
	if m.coordinator != nil {
		return m.coordinator, nil
	}
	cache, err := newLSPCacheStoreFromEnv(m.logger)
	if err != nil {
		return nil, err
	}
	m.coordinator = &bootstrapCoordinator{
		cache:  cache,
		states: newBootstrapStateStore(),
	}
	return m.coordinator, nil
}

func closeBootstrapCoordinator(m *manager) {
	if m == nil {
		return
	}
	m.coordinatorMu.Lock()
	c := m.coordinator
	m.coordinator = nil
	m.coordinatorMu.Unlock()
	if c != nil {
		c.cache.Close()
	}
}

func (m *manager) bootstrapTarget(ctx context.Context, uri string) (documentRef, workspaceConfig, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return documentRef{}, workspaceConfig{}, err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) {
		return ref, workspaceConfig{}, nil
	}
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return documentRef{}, workspaceConfig{}, err
	}
	return ref, cfg, nil
}

// syncDocument 读取磁盘文档并同步到对应 LSP client。
// 文件已删除或读取失败时会清理缓存状态并记录失败，避免 stale 文档继续参与诊断。
func (c *bootstrapCoordinator) syncDocument(ctx context.Context, m *manager, cfg workspaceConfig, ref documentRef) error {
	if ref.uri == "" || !m.shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	snapshot, err := readDocumentSnapshot(ref)
	if err != nil {
		joined := errors.Join(err, m.cleanupDeletedDocument(ref, scope))
		c.states.fail(scope.bootstrapKey(), ref.uri, joined)
		return joined
	}
	if err := c.cleanupOldScopeIfChanged(m, ref, scope); err != nil {
		return err
	}
	return c.syncSnapshot(ctx, m, cfg, snapshot)
}

// syncSnapshot 根据 bootstrap 状态决定跳过、等待或发送文档快照。
// 缓存命中时会递增版本号，保证 LSP server 看到单调的文档版本。
func (c *bootstrapCoordinator) syncSnapshot(ctx context.Context, m *manager, cfg workspaceConfig, snapshot documentSnapshot) error {
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	stateKey := scope.bootstrapKey()
	key := scope.cacheKey(snapshot.ref.languageID, snapshot.ref.uri)
	decision := c.states.prepare(stateKey, snapshot.ref.uri, snapshot.fingerprint)
	switch decision.action {
	case bootstrapActionSkip:
		return c.cache.RememberDocumentScope(snapshot.ref.uri, scope, snapshot.fingerprint)
	case bootstrapActionWait:
		return c.states.waitFor(ctx, stateKey, snapshot.ref.uri, decision.wait)
	}

	record, cached := c.cache.Load(key)
	version := 1
	if cached && record.Version > 0 {
		version = record.Version + 1
	}
	if err := c.syncSnapshotToClient(ctx, m, cfg, snapshot, snapshotSyncRequest{
		key:                     key,
		version:                 version,
		cached:                  cached,
		previous:                decision.previous,
		refreshStaleDiagnostics: m.hasStaleDiagnosticsForSnapshot(scope, snapshot),
		scope:                   scope,
	}); err != nil {
		c.states.fail(stateKey, snapshot.ref.uri, err)
		return err
	}
	return nil
}

// openSnapshotIfNeeded 只在文档尚未 ready/stale/bootstrapping 时打开快照。
// 这是 hover/definition 等轻量请求的预热路径，不会重复抢占正在进行的 bootstrap。
func (c *bootstrapCoordinator) openSnapshotIfNeeded(ctx context.Context, m *manager, cfg workspaceConfig, snapshot documentSnapshot) error {
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	stateKey := scope.bootstrapKey()
	status := c.states.status(stateKey, snapshot.ref.uri)
	if status == bootstrapReady || status == bootstrapStale || status == bootstrapBootstrapping {
		return nil
	}
	key := scope.cacheKey(snapshot.ref.languageID, snapshot.ref.uri)
	version, err := c.openSnapshotVersion(key, snapshot)
	if err != nil {
		return err
	}
	if err := c.syncSnapshotToClient(ctx, m, cfg, snapshot, snapshotSyncRequest{
		key:      key,
		version:  version,
		openOnly: true,
		scope:    scope,
	}); err != nil {
		c.states.fail(stateKey, snapshot.ref.uri, err)
		return err
	}
	return nil
}

// reopenSnapshotForDiagnostics 强制 close/open 当前磁盘快照，并在成功后记录新的版本与 ready 状态。
func (c *bootstrapCoordinator) reopenSnapshotForDiagnostics(ctx context.Context, m *manager, cfg workspaceConfig, snapshot documentSnapshot) error {
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return err
	}
	key := scope.cacheKey(snapshot.ref.languageID, snapshot.ref.uri)
	version := 1
	if record, ok := c.cache.Load(key); ok && record.Version > 0 {
		version = record.Version + 1
	}
	m.invalidateDocumentDiagnosticsForReopen(scope, snapshot.ref.uri)
	if err := c.syncSnapshotToClient(ctx, m, cfg, snapshot, snapshotSyncRequest{
		key:         key,
		version:     version,
		forceReopen: true,
		scope:       scope,
	}); err != nil {
		c.states.fail(scope.bootstrapKey(), snapshot.ref.uri, err)
		return err
	}
	m.advanceDocumentDiagnosticEpoch(scope, snapshot.ref.uri)
	return nil
}

// openSnapshotVersion 为 open-only 同步选择下一次文档版本。
// 缓存内容不匹配时会删除旧记录，防止 server 接收与缓存不一致的版本序列。
func (c *bootstrapCoordinator) openSnapshotVersion(key lspCacheKey, snapshot documentSnapshot) (int, error) {
	version := 1
	if record, cached := c.cache.Load(key); cached && cacheValueMatchesSnapshot(record, snapshot) {
		if record.Version > 0 {
			version = record.Version + 1
		}
	} else if cached {
		if err := c.cache.Delete(key); err != nil {
			return 0, err
		}
	}
	return version, nil
}

// applyBootstrapUpdate 按同步模式发送 didOpen、didChange 或诊断专用的 didClose/didOpen。
// forceReopen 必须优先处理，避免普通缓存判断把显式诊断退回到可能保留旧符号的 didChange。
func applyBootstrapUpdate(ctx context.Context, client Client, snapshot documentSnapshot, req snapshotSyncRequest) error {
	if req.forceReopen {
		return reopenSnapshot(ctx, client, snapshot, req.version)
	}
	if req.refreshStaleDiagnostics || req.cached && (req.previous == bootstrapReady || req.previous == bootstrapStale) {
		return client.DidChange(ctx, snapshot.ref.uri, req.version, []protocol.TextDocumentContentChangeEvent{{
			Text: snapshot.text,
		}})
	}
	return client.DidOpen(ctx, snapshot.ref.uri, snapshot.ref.languageID, req.version, snapshot.text)
}

// refreshWorkspace 在目标文档同步后刷新同 scope 下的已缓存文档。
// 刷新数量和并发都有上限，避免一次 bootstrap 扫描拖垮 LSP server。
func (c *bootstrapCoordinator) refreshWorkspace(ctx context.Context, m *manager, cfg workspaceConfig, excludeURI string) {
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		logBootstrapWarning(m, excludeURI, err)
		return
	}
	records := limitRefreshRecords(c.cache.ScopeDocuments(scope))
	if len(records) == 0 {
		return
	}
	c.states.restore(scope.bootstrapKey(), recordsToURIs(records))
	runRefreshTasks(ctx, maxRefreshConcurrency, len(records), func(index int) {
		record := records[index]
		if record.Key.URI == excludeURI {
			return
		}
		ref, err := m.resolveDocumentRef(ctx, record.Key.URI, cacheKeyLanguage(record.Key))
		if err != nil {
			logBootstrapWarning(m, record.Key.URI, err)
			return
		}
		if err := c.syncDocument(ctx, m, cfg, ref); err != nil {
			logBootstrapWarning(m, ref.uri, err)
		}
	})
}

func (c *bootstrapCoordinator) cleanupOldScopeIfChanged(m *manager, ref documentRef, current ResolvedLSPToolScope) error {
	indexed, ok := c.cache.LastResolvedScope(ref.uri)
	if !ok {
		return nil
	}
	previous := indexed.LastResolvedScope
	if previous.ScopeKey == current.ScopeKey && previous.WorkspaceKey == current.WorkspaceKey {
		return nil
	}
	return m.cleanupDocumentForScopes(ref.uri, previous)
}

func (s *bootstrapStateStore) delete(workspace, uri string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := bootstrapKey{workspace: workspace, uri: uri}
	entry := s.entries[key]
	if entry != nil && entry.wait != nil {
		entry.finishWaitLocked(errors.New("deleted bootstrap for " + uri))
	}
	delete(s.entries, key)
}

func (c *bootstrapCoordinator) bootstrapSiblings(ctx context.Context, m *manager, cfg workspaceConfig, target documentRef) {
	scope, adapter, err := m.resolveLanguageScope(ctx, target.languageID, target.absPath, target.uri)
	if err != nil {
		logBootstrapWarning(m, target.uri, err)
		return
	}
	policy := adapter.BootstrapPolicy(scope)
	if !policy.OpenSiblingDocuments {
		return
	}
	refs, err := siblingDocumentRefs(target, policy.SiblingExtensions)
	if err != nil {
		logBootstrapWarning(m, target.uri, err)
		return
	}
	runRefreshTasks(ctx, maxRefreshConcurrency, len(refs), func(index int) {
		if err := c.syncDocument(ctx, m, cfg, refs[index]); err != nil {
			logBootstrapWarning(m, refs[index].uri, err)
		}
	})
}

func readDocumentSnapshot(ref documentRef) (documentSnapshot, error) {
	info, err := os.Stat(ref.absPath)
	if err != nil {
		return documentSnapshot{}, err
	}
	payload, err := os.ReadFile(ref.absPath)
	if err != nil {
		return documentSnapshot{}, err
	}
	return documentSnapshot{
		ref:         ref,
		text:        string(payload),
		size:        info.Size(),
		modTimeNano: info.ModTime().UnixNano(),
		fingerprint: hashDocument(payload),
	}, nil
}

// siblingDocumentRefs 收集目标文件同目录下可预热的同语言兄弟文档。
// 只接受指定扩展名并限制数量，避免打开整个目录造成 LSP 初始化抖动。
func siblingDocumentRefs(target documentRef, extensions []string) ([]documentRef, error) {
	entries, err := os.ReadDir(filepath.Dir(target.absPath))
	if err != nil {
		return nil, err
	}
	allowedExtensions := extensionSet(extensions)
	refs := make([]documentRef, 0, maxSiblingBootstrap)
	for _, entry := range entries {
		if entry.IsDir() || len(refs) >= maxSiblingBootstrap {
			continue
		}
		name := entry.Name()
		if _, ok := allowedExtensions[strings.ToLower(filepath.Ext(name))]; !ok {
			continue
		}
		path := filepath.Join(filepath.Dir(target.absPath), name)
		if path == target.absPath {
			continue
		}
		absPath, err := platformshared.NormalizeAbsolutePath(path)
		if err != nil {
			return nil, err
		}
		refs = append(refs, documentRef{
			raw:        absPath,
			uri:        fileURIFromPath(absPath),
			absPath:    absPath,
			languageID: target.languageID,
		})
	}
	return refs, nil
}

// runRefreshTasks 以固定并发运行 workspace refresh 任务。
// ctx 取消后不再派发新任务，已启动的任务会在 WaitGroup 中自然收尾。
func runRefreshTasks(ctx context.Context, width, count int, fn func(int)) {
	if count == 0 {
		return
	}
	if width <= 0 {
		width = 1
	}
	sem := make(chan struct{}, width)
	var wg sync.WaitGroup
	for index := range count {
		select {
		case <-ctx.Done():
			return
		default:
		}
		wg.Add(1)
		currentIndex := index
		safego.Go(ctx, nil, "mcp-lsp.bootstrap.refresh", func(runCtx context.Context) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-runCtx.Done():
				return
			}
			defer func() { <-sem }()
			fn(currentIndex)
		})
	}
	wg.Wait()
}

func limitRefreshRecords(records []lspCacheValue) []lspCacheValue {
	if len(records) <= maxRefreshFiles {
		return records
	}
	return records[:maxRefreshFiles]
}

func recordsToURIs(records []lspCacheValue) []string {
	uris := make([]string, 0, len(records))
	for _, record := range records {
		uris = append(uris, record.Key.URI)
	}
	return uris
}

func hashDocument(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func logBootstrapWarning(m *manager, uri string, err error) {
	if m == nil || m.logger == nil || err == nil {
		return
	}
	m.logger.Warn("LSP bootstrap skipped document", "uri", uri, "err", err)
}
