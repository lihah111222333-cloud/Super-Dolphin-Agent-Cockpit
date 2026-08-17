package multilsp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	maxSiblingBootstrap   = 20
	maxRefreshFiles       = 50
	maxRefreshConcurrency = 8
)

type bootstrapCoordinator struct {
	lifecycleMu sync.RWMutex
	closed      atomic.Bool
	cache       *lspCacheStore
	states      *bootstrapStateStore
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
	records := coordinator.cache.ScopeDocuments(scope)
	coordinator.states.reset(scope.bootstrapKey(), recordsToURIs(records))
	client, err := m.ensureClient(ctx, cfg)
	if err != nil {
		return err
	}
	if err := restoreLegacyCachedDocuments(ctx, coordinator, m, cfg, records); err != nil {
		return err
	}
	guard, err := m.syncExplicitDocumentsForWorkspaceSymbol(ctx, cfg, client)
	if err != nil {
		return err
	}
	guard.release()
	return nil
}

func restoreLegacyCachedDocuments(
	ctx context.Context,
	coordinator *bootstrapCoordinator,
	m *manager,
	cfg workspaceConfig,
	records []lspCacheValue,
) error {
	for _, record := range records {
		if _, managed := m.explicitDocumentForURI(record.Key.URI); managed {
			continue
		}
		ref, err := m.resolveDocumentRef(ctx, record.Key.URI, cacheKeyLanguage(record.Key))
		if err != nil {
			return err
		}
		if err := coordinator.syncDocument(ctx, m, cfg, ref); err != nil {
			return err
		}
	}
	return nil
}

func bootstrapCoordinatorFor(m *manager) (*bootstrapCoordinator, error) {
	if m == nil {
		return nil, ErrManagerClosed
	}
	coordinator, err := currentBootstrapCoordinator(m)
	if err != nil || coordinator != nil {
		return coordinator, err
	}
	cache, err := newLSPCacheStoreFromEnv(m.logger)
	if err != nil {
		return nil, err
	}
	candidate := &bootstrapCoordinator{cache: cache, states: newBootstrapStateStore()}
	if m.bootstrapCoordinatorBeforePublish != nil {
		m.bootstrapCoordinatorBeforePublish()
	}
	return publishBootstrapCoordinator(m, candidate)
}

func currentBootstrapCoordinator(m *manager) (*bootstrapCoordinator, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed || m.retiring {
		return nil, ErrManagerClosed
	}
	m.coordinatorMu.Lock()
	defer m.coordinatorMu.Unlock()
	return m.coordinator, nil
}

func publishBootstrapCoordinator(m *manager, candidate *bootstrapCoordinator) (*bootstrapCoordinator, error) {
	m.mu.RLock()
	m.coordinatorMu.Lock()
	closed := m.closed || m.retiring
	if !closed && m.coordinator == nil {
		m.coordinator = candidate
	}
	active := m.coordinator
	m.coordinatorMu.Unlock()
	m.mu.RUnlock()
	if active != candidate {
		candidate.close()
	}
	if closed {
		return nil, ErrManagerClosed
	}
	return active, nil
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
		c.close()
	}
}

func (c *bootstrapCoordinator) withMutation(commit func() error) error {
	if c == nil {
		return ErrManagerClosed
	}
	c.lifecycleMu.RLock()
	defer c.lifecycleMu.RUnlock()
	if c.closed.Load() {
		return ErrManagerClosed
	}
	if err := commit(); err != nil {
		return err
	}
	if c.closed.Load() {
		return ErrManagerClosed
	}
	return nil
}

func (c *bootstrapCoordinator) close() {
	if c == nil || !c.closed.CompareAndSwap(false, true) {
		return
	}
	c.cache.markClosed()
	c.states.markClosed()
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	c.cache.waitForMutations()
	c.states.waitForMutations()
}

func deleteBootstrapStateIfPresent(m *manager, scopeKey, uri string) {
	if m == nil {
		return
	}
	m.coordinatorMu.Lock()
	defer m.coordinatorMu.Unlock()
	if m.coordinator != nil {
		m.coordinator.states.delete(scopeKey, uri)
	}
}

func (m *manager) bootstrapTarget(ctx context.Context, uri string) (documentRef, workspaceConfig, error) {
	languageID := ""
	if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
		languageID = resolved.LanguageID
	}
	ref, err := m.resolveDocumentRef(ctx, uri, languageID)
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
	if decision.action == bootstrapActionSkip && !m.managedBootstrapSnapshotCurrent(cfg, snapshot.ref.uri) {
		c.states.delete(stateKey, snapshot.ref.uri)
		decision = c.states.prepare(stateKey, snapshot.ref.uri, snapshot.fingerprint)
	}
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

func (m *manager) managedBootstrapSnapshotCurrent(cfg workspaceConfig, uri string) bool {
	document, ok := m.explicitDocumentForURI(uri)
	if !ok || document.configKey != cfg.key || !document.wireOpen {
		return false
	}
	m.mu.RLock()
	workspace := m.workspaces[cfg.key]
	if workspace == nil {
		m.mu.RUnlock()
		return false
	}
	client := workspace.client
	generation := workspace.generation
	state := workspace.state
	m.mu.RUnlock()
	if client == nil || generation != document.clientGeneration || state == workspaceStateBootstrapping {
		return false
	}
	return clientHealthy(client)
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
	if status == bootstrapBootstrapping {
		return nil
	}
	if c.states.isReadyAndCurrent(stateKey, snapshot.ref.uri, snapshot.fingerprint) &&
		m.managedBootstrapSnapshotCurrent(cfg, snapshot.ref.uri) {
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
	if s.closed.Load() {
		return
	}

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
	return readDocumentSnapshotWithLimit(ref, defaultCleanDocumentByteLimit)
}

func readDocumentSnapshotWithLimit(ref documentRef, byteLimit int64) (documentSnapshot, error) {
	return readDocumentSnapshotWithLimitAfterOpen(ref, byteLimit, nil)
}

func readDocumentSnapshotWithLimitAfterOpen(
	ref documentRef,
	byteLimit int64,
	afterOpen func(),
) (documentSnapshot, error) {
	before, err := os.Stat(ref.absPath)
	if err != nil {
		return documentSnapshot{}, err
	}
	if !before.Mode().IsRegular() {
		return documentSnapshot{}, fmt.Errorf("document is not a regular file: %s", ref.absPath)
	}
	if before.Size() > byteLimit {
		return documentSnapshot{}, fmt.Errorf("document exceeds read limit %d: %s", byteLimit, ref.absPath)
	}
	file, err := os.Open(ref.absPath)
	if err != nil {
		return documentSnapshot{}, err
	}
	if afterOpen != nil {
		afterOpen()
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, byteLimit+1))
	openedAfter, fileStatErr := file.Stat()
	pathAfter, pathStatErr := os.Stat(ref.absPath)
	closeErr := file.Close()
	if err := errors.Join(readErr, fileStatErr, pathStatErr, closeErr); err != nil {
		return documentSnapshot{}, err
	}
	if err := validateDocumentSnapshotRead(ref, before, openedAfter, pathAfter, payload, byteLimit); err != nil {
		return documentSnapshot{}, err
	}
	return documentSnapshot{
		ref:         ref,
		text:        string(payload),
		size:        openedAfter.Size(),
		modTimeNano: openedAfter.ModTime().UnixNano(),
		fingerprint: hashDocument(payload),
	}, nil
}

func validateDocumentSnapshotRead(
	ref documentRef,
	before os.FileInfo,
	openedAfter os.FileInfo,
	pathAfter os.FileInfo,
	payload []byte,
	byteLimit int64,
) error {
	if int64(len(payload)) > byteLimit {
		return fmt.Errorf("document grew beyond read limit %d: %s", byteLimit, ref.absPath)
	}
	if !os.SameFile(before, openedAfter) || !os.SameFile(openedAfter, pathAfter) {
		return fmt.Errorf("document path was replaced while reading: %s", ref.absPath)
	}
	if before.Size() != openedAfter.Size() || openedAfter.Size() != pathAfter.Size() || int64(len(payload)) != openedAfter.Size() {
		return fmt.Errorf("document changed size while reading: %s", ref.absPath)
	}
	if before.ModTime() != openedAfter.ModTime() || openedAfter.ModTime() != pathAfter.ModTime() {
		return fmt.Errorf("document changed modification time while reading: %s", ref.absPath)
	}
	return nil
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
