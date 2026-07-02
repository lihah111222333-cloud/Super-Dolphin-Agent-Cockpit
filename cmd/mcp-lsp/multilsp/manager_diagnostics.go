package multilsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
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
	workspaceKey  string
	workspaceRoot string
	all           bool
}

type diagnosticMetadata struct {
	fingerprint string
	mtimeNS     int64
	size        int64
}

type resolvedLSPToolScopeContextKey struct{}

// WithResolvedLSPToolScope 将 ManagerPool 解析出的规范 scope 放入 context。
// diagnostics/cache/bootstrap 复用该 scope，避免重复拼接 manager key 时产生不一致。
func WithResolvedLSPToolScope(ctx context.Context, scope ResolvedLSPToolScope) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if scope.WorkspaceKey == "" && scope.ManagerKey == "" {
		return ctx
	}
	return context.WithValue(ctx, resolvedLSPToolScopeContextKey{}, scope)
}

// PublishDiagnostics 将 LSP publishDiagnostics 通知转交给 manager。
// handler 本身不缓存状态，缓存边界由 manager 的诊断代际控制。
func (h managerNotificationHandler) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	return h.publishDiagnostics(params)
}

// LogMessage 将 LSP window/logMessage 通知转交给 manager 的日志分级处理。
func (h managerNotificationHandler) LogMessage(params protocol.LogMessageParams) error {
	return h.logMessage(params)
}

// Diagnostics 返回当前 scope 或指定 URI 的诊断快照。
// 读取前会刷新仍存在的文件并清理已删除文档的缓存，保证返回值不跨 workspace 或陈旧文件泄漏。
func (m *manager) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	filter, err := m.normalizeDiagnosticFilter(ctx, uris)
	if err != nil {
		return nil, err
	}
	if err := m.refreshExistingDiagnosticTargets(ctx, uris, filter); err != nil {
		return nil, err
	}
	if err := m.cleanupDeletedDiagnostics(ctx, filter); err != nil {
		return nil, err
	}

	items := m.currentDiagnostics(filter)
	sort.Slice(items, func(i, j int) bool {
		return items[i].URI < items[j].URI
	})
	return items, nil
}

// refreshExistingDiagnosticTargets 对仍存在的诊断目标触发一次按需刷新。
// 显式 URI 会逐个解析并校验文件存在；空 URI 列表表示刷新当前 filter 覆盖的全部目标。
func (m *manager) refreshExistingDiagnosticTargets(ctx context.Context, uris []string, filter diagnosticFilter) error {
	if m.factory == nil {
		return nil
	}
	if len(uris) == 0 {
		return m.refreshAllDiagnosticTargets(ctx, filter)
	}
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		ref, err := m.resolveDocumentRef(ctx, uri, "")
		if err != nil {
			return err
		}
		if !m.shouldUseClientForLanguage(ref.languageID) || !fileExists(ref.absPath) {
			continue
		}
		if err := m.refreshDiagnosticRef(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

// WaitDiagnosticsStable 等待目标诊断在当前代际内稳定。
// 它先刷新候选文件，再用内部最大等待时间包住轮询，避免 MCP 调用被语言服务器拖到全局超时。
func (m *manager) WaitDiagnosticsStable(ctx context.Context, uris []string) error {
	if err := sleepContext(ctx, m.diagInitial); err != nil {
		return err
	}
	filter, err := m.normalizeDiagnosticFilter(ctx, uris)
	if err != nil {
		return err
	}
	if err := m.refreshExistingDiagnosticTargets(ctx, uris, filter); err != nil {
		return err
	}
	waitCtx, cancel := m.diagnosticsStableWaitContext(ctx)
	defer cancel()
	waiter, err := m.newDiagnosticStableWait(waitCtx, filter, uris)
	if err != nil {
		return err
	}
	return waiter.wait()
}

// diagnosticsStableWaitContext 给诊断稳定等待设置内部上限，避免外层MCP调用被拖到全局超时。
func (m *manager) diagnosticsStableWaitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	maxWait := m.diagMaxWait
	if maxWait <= 0 {
		return ctx, func() {}
	}
	if m.diagPoll > 0 && maxWait < m.diagPoll {
		maxWait = m.diagPoll
	}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) <= maxWait {
		return ctx, func() {}
	}
	return ctxutil.WithTimeout(ctx, maxWait)
}

// CurrentDiagnosticGeneration 返回当前诊断代际编号。
// 代际用于丢弃重建 client 前捕获的异步 publishDiagnostics。
func (m *manager) CurrentDiagnosticGeneration() uint64 {
	return m.diagGeneration.Load()
}

// AdvanceDiagnosticGeneration 推进诊断代际，防止旧结果覆盖新结果。
func (m *manager) AdvanceDiagnosticGeneration() uint64 {
	next := m.diagGeneration.Add(1)
	m.diagMu.Lock()
	clear(m.diagnostics)
	m.diagMu.Unlock()
	return next
}

// PublishDiagnostics 记录当前代际收到的 LSP 诊断推送。
func (m *manager) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	return m.publishDiagnosticsForGeneration(params, m.CurrentDiagnosticGeneration())
}

// publishDiagnosticsForGeneration 在指定代际内写入诊断快照。
// 如果 client 已重建导致代际过期，旧通知会被忽略而不是覆盖新 workspace 状态。
func (m *manager) publishDiagnosticsForGeneration(params protocol.PublishDiagnosticsParams, capturedGen uint64) error {
	if capturedGen < m.CurrentDiagnosticGeneration() {
		return nil
	}
	uri, err := canonicalDiagnosticURI(params.URI)
	if err != nil {
		return err
	}
	params.URI = uri

	scope := m.scopeForPublishedDiagnostics(params.URI)
	staleVersion, err := m.diagnosticVersionOlderThanCached(params, scope)
	if err != nil {
		return err
	}
	if staleVersion {
		return nil
	}
	key := diagnosticStoreKeyFor(scope, params.URI)

	metadata := diagnosticMetadataForURI(params.URI)
	m.diagMu.Lock()
	defer m.diagMu.Unlock()
	if capturedGen < m.CurrentDiagnosticGeneration() {
		return nil
	}
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

// diagnosticVersionOlderThanCached 判断 LSP 推送是否落后于当前已同步文档版本。
// 延迟到达的旧版本诊断不能写成当前磁盘 fingerprint，否则后续 diagnostics 会误认为旧结果仍有效。
func (m *manager) diagnosticVersionOlderThanCached(params protocol.PublishDiagnosticsParams, scope ResolvedLSPToolScope) (bool, error) {
	if params.Version == nil {
		return false, nil
	}
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return false, err
	}
	record, ok := coordinator.cache.Load(scope.cacheKey(scope.LanguageID, params.URI))
	if !ok || record.Version <= 0 {
		return false, nil
	}
	return *params.Version < record.Version, nil
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

// normalizeDiagnosticFilter 规范化诊断过滤条件。
func (m *manager) normalizeDiagnosticFilter(ctx context.Context, uris []string) (diagnosticFilter, error) {
	if len(uris) == 0 {
		if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
			return diagnosticFilter{
				scopeKey:      resolved.ScopeKey,
				workspaceKey:  resolved.WorkspaceKey,
				workspaceRoot: resolved.WorkspaceRoot,
				all:           true,
			}, nil
		}
		return diagnosticFilter{
			scopeKey: lspScopeKeyFromContext(ctx),
			// 空 workspaceKey 需要由 matches 结合 scopeKey 和 workspaceRoot 继续过滤。
			all: true,
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
		keys[diagnosticStoreKey{uri: ref.uri}.String()] = struct{}{}
		if _, err := os.Stat(ref.absPath); err != nil && os.IsNotExist(err) {
			if err := m.cleanupDeletedDocument(ref, scope); err != nil {
				return diagnosticFilter{}, err
			}
		}
	}
	return diagnosticFilter{keys: keys}, nil
}

func (m *manager) currentDiagnostics(filter diagnosticFilter) []protocol.PublishDiagnosticsParams {
	items := make([]protocol.PublishDiagnosticsParams, 0)
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		items = append(items, snapshot.params)
	})
	return items
}

// forEachCurrentDiagnosticSnapshot 在读锁内遍历当前代际且匹配 filter 的诊断快照。
// 回调只接收复制出的 snapshot，调用方不能借此修改内部 map。
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

// matches 判断诊断快照是否属于当前过滤条件。
// 显式 keys 优先；scope 查询还会用 workspaceRoot containment 防止不同项目同 scopeKey 的诊断混入。
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
	if f.workspaceKey != "" && snapshot.workspaceKey != f.workspaceKey {
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

// cleanupDeletedDiagnostics 移除当前 filter 下已经不存在的文件诊断。
// 删除会同步清理诊断 map、bootstrap 状态和 scope cache，避免下一次查询拿到墓碑前的结果。
func (m *manager) cleanupDeletedDiagnostics(ctx context.Context, filter diagnosticFilter) error {
	snapshots := make([]diagnosticSnapshot, 0)
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		if snapshot.uri == "" {
			return
		}
		if _, err := os.Stat(pathFromDiagnosticURI(snapshot.uri)); err != nil && os.IsNotExist(err) {
			snapshots = append(snapshots, snapshot)
		}
	})
	var errs []error
	for _, snapshot := range snapshots {
		current := ResolvedLSPToolScope{
			LSPToolScope: LSPToolScope{
				WorkspaceRoot: pathOrEmpty(snapshot.uri),
				LanguageID:    snapshot.language,
			},
			ScopeKey:     snapshot.scopeKey,
			WorkspaceKey: snapshot.workspaceKey,
		}
		if _, _, scope, err := m.resolvedScopeForURI(ctx, snapshot.uri, snapshot.language); err == nil {
			current = scope
		}
		if err := m.cleanupDocumentForScopes(snapshot.uri, current); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cleanupDeletedDocument 清理指定已删除文档在当前和最近一次解析 scope 下的诊断状态。
// 原始 file URI 与规范 URI 都会尝试处理，覆盖调用方传入旧 URI 形态的兼容边界。
func (m *manager) cleanupDeletedDocument(ref documentRef, current ResolvedLSPToolScope) error {
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	var errs []error
	for _, uri := range deletedDocumentURIs(ref) {
		scopes := []ResolvedLSPToolScope{current}
		if indexed, ok := coordinator.cache.LastResolvedScope(uri); ok {
			scopes = append(scopes, indexed.LastResolvedScope)
		}
		if err := m.cleanupDocumentForScopesWithCoordinator(coordinator, uri, scopes...); err != nil {
			errs = append(errs, err)
		}
		if err := coordinator.cache.RememberDocumentScope(uri, current, ""); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func deletedDocumentURIs(ref documentRef) []string {
	uris := []string{ref.uri}
	raw := strings.TrimSpace(ref.raw)
	if strings.HasPrefix(raw, "file://") && raw != ref.uri {
		uris = append(uris, raw)
	}
	return uris
}

func (m *manager) cleanupDocumentForScopes(uri string, scopes ...ResolvedLSPToolScope) error {
	if strings.TrimSpace(uri) == "" {
		return nil
	}
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	return m.cleanupDocumentForScopesWithCoordinator(coordinator, uri, scopes...)
}

// cleanupDocumentForScopesWithCoordinator 在共享 coordinator 下清理文档诊断和 bootstrap 状态。
// 它会删除传入 scopes 的精确 key，并兜底移除同 URI 的遗留快照，避免多 scope 重命名后残留。
func (m *manager) cleanupDocumentForScopesWithCoordinator(coordinator *bootstrapCoordinator, uri string, scopes ...ResolvedLSPToolScope) error {
	seen := map[string]struct{}{}
	var errs []error

	m.diagMu.Lock()
	for _, scope := range scopes {
		key := diagnosticStoreKeyFor(scope, uri)
		delete(m.diagnostics, key.String())
		seen[key.String()] = struct{}{}
		if err := coordinator.cache.Tombstone(scope.cacheKey(scope.LanguageID, uri)); err != nil {
			errs = append(errs, err)
		}
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
	return errors.Join(errs...)
}

func (m *manager) scopeForPublishedDiagnostics(uri string) ResolvedLSPToolScope {
	if coordinator, err := bootstrapCoordinatorFor(m); err == nil {
		if indexed, ok := coordinator.cache.LastResolvedScope(uri); ok {
			return indexed.LastResolvedScope
		}
	}
	return ResolvedLSPToolScope{LSPToolScope: LSPToolScope{LanguageID: languageFromURI(uri)}}
}

func (m *manager) resolvedScopeForURI(ctx context.Context, uri, languageID string) (documentRef, workspaceConfig, ResolvedLSPToolScope, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, languageID)
	if err != nil {
		return documentRef{}, workspaceConfig{}, ResolvedLSPToolScope{}, err
	}
	cfg, err := m.workspaceConfigForDiagnosticRef(ctx, ref)
	if err != nil {
		return documentRef{}, workspaceConfig{}, ResolvedLSPToolScope{}, err
	}
	scope, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return documentRef{}, workspaceConfig{}, ResolvedLSPToolScope{}, err
	}
	return ref, cfg, scope, nil
}

func (m *manager) workspaceConfigForDiagnosticRef(ctx context.Context, ref documentRef) (workspaceConfig, error) {
	if m.shouldUseClientForLanguage(ref.languageID) {
		return m.resolveWorkspaceForDocument(ctx, ref)
	}
	root, err := m.effectiveWorkspaceRoot(ctx)
	if err != nil {
		return workspaceConfig{}, err
	}
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

func (m *manager) resolvedScopeForConfig(ctx context.Context, cfg workspaceConfig) (ResolvedLSPToolScope, error) {
	if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
		return resolved, nil
	}
	lspScope, err := m.lspToolScopeForConfig(ctx, cfg)
	if err != nil {
		return ResolvedLSPToolScope{}, err
	}
	return ResolveLSPToolScope(lspScope)
}

func lspScopeKeyFromContext(ctx context.Context) string {
	if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
		return resolved.ScopeKey
	}
	return buildScopeKey(lspToolScopeFromContext(ctx))
}

// resolvedLSPToolScopeFromContext 从 context 读取已解析的 LSP 工具 scope。
// 优先使用 multilsp 自己写入的 scope；没有时兼容 manager 包传入的通用 ResolvedToolScope。
func resolvedLSPToolScopeFromContext(ctx context.Context) (ResolvedLSPToolScope, bool) {
	if ctx == nil {
		return ResolvedLSPToolScope{}, false
	}
	scope, ok := ctx.Value(resolvedLSPToolScopeContextKey{}).(ResolvedLSPToolScope)
	if ok && (scope.WorkspaceKey != "" || scope.ManagerKey != "") {
		return scope, true
	}
	if generic, ok := lspmanager.ResolvedToolScopeFromContext(ctx); ok {
		return resolvedLSPToolScopeFromManagerScope(generic), true
	}
	return ResolvedLSPToolScope{}, false
}

func resolvedLSPToolScopeFromManagerScope(scope lspmanager.ResolvedToolScope) ResolvedLSPToolScope {
	return ResolvedLSPToolScope{
		LSPToolScope: LSPToolScope{
			AgentID:               scope.AgentID,
			ThreadID:              scope.ThreadID,
			TurnID:                scope.TurnID,
			CallID:                scope.CallID,
			CWD:                   scope.CWD,
			WorkspaceRoots:        append([]string(nil), scope.WorkspaceRoots...),
			Family:                scope.Family,
			LanguageID:            scope.LanguageID,
			TargetPath:            scope.TargetPath,
			TargetURI:             scope.TargetURI,
			WorkspaceRoot:         scope.WorkspaceRoot,
			RootKind:              scope.RootKind,
			LanguageWorkspaceRoot: scope.LanguageWorkspaceRoot,
			ProjectRoot:           scope.ProjectRoot,
			LanguageSpecific:      copyLanguageSpecific(scope.LanguageSpecific),
		},
		ScopeKey:     scope.ScopeKey,
		WorkspaceKey: scope.WorkspaceKey,
		ShardKey:     scope.ShardKey,
		ManagerKey:   scope.ManagerKey,
	}
}

func (m *manager) lspToolScopeForConfig(ctx context.Context, cfg workspaceConfig) (LSPToolScope, error) {
	scope := lspToolScopeFromContext(ctx)
	if scope.CWD == "" {
		root, err := m.effectiveWorkspaceRoot(ctx)
		if err != nil {
			return LSPToolScope{}, err
		}
		scope.CWD = root
	}
	scope.LanguageID = normalizeLanguageID(cfg.languageID)
	scope.WorkspaceRoot = cfg.rootPath
	scope.LanguageWorkspaceRoot = cfg.rootPath
	scope.ProjectRoot = cfg.rootPath
	if parsed, ok := lspScopeWorkspacePartsFromConfig(cfg); ok {
		scope.LanguageID = parsed.LanguageID
		scope.WorkspaceRoot = parsed.WorkspaceRoot
		scope.RootKind = parsed.RootKind
		scope.LanguageWorkspaceRoot = parsed.LanguageWorkspaceRoot
		scope.ProjectRoot = parsed.ProjectRoot
		scope.LanguageSpecific = parsed.LanguageSpecific
	}
	return scope, nil
}

func lspToolScopeFromContext(ctx context.Context) LSPToolScope {
	if trusted, ok := common.ToolScopeFromContext(ctx); ok {
		return LSPToolScope{
			AgentID:        trusted.AgentID,
			ThreadID:       trusted.ThreadID,
			TurnID:         trusted.TurnID,
			CallID:         trusted.CallID,
			CWD:            trusted.CWD,
			WorkspaceRoots: append([]string(nil), trusted.WorkspaceRoots...),
			Family:         normalizeScopeFamily(trusted.Family),
		}
	}
	return LSPToolScope{Family: defaultLSPToolFamily}
}

func lspScopeWorkspacePartsFromConfig(cfg workspaceConfig) (LSPToolScope, bool) {
	parts := strings.Split(cfg.key, scopeKeySeparator)
	if len(parts) != 6 {
		return LSPToolScope{}, false
	}
	languageID := normalizeLanguageID(parts[0])
	if languageID == "" || languageID != normalizeLanguageID(cfg.languageID) {
		return LSPToolScope{}, false
	}
	return LSPToolScope{
		LanguageID:            languageID,
		RootKind:              parts[1],
		WorkspaceRoot:         parts[2],
		LanguageWorkspaceRoot: parts[3],
		ProjectRoot:           parts[4],
		LanguageSpecific:      parseLanguageSpecificParts(parts[5]),
	}, true
}

// parseLanguageSpecificParts 解析 manager key 中的语言专属维度。
// 非 key=value 片段会被忽略，空结果返回 nil，保持旧 key 的兼容读取。
func parseLanguageSpecificParts(encoded string) map[string]string {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil
	}
	parts := strings.Split(encoded, "\x1f")
	values := make(map[string]string, len(parts))
	for _, part := range parts {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = strings.TrimSpace(value)
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func diagnosticStoreKeyFor(scope ResolvedLSPToolScope, uri string) diagnosticStoreKey {
	return diagnosticStoreKey{scopeKey: scope.ScopeKey, workspaceKey: scope.WorkspaceKey, uri: uri}
}

// String 将诊断存储 key 编码为内部 map key。
// 分隔符使用 NUL，避免普通路径或 URI 中的斜杠、冒号影响匹配。
func (k diagnosticStoreKey) String() string {
	return k.scopeKey + "\x00" + k.workspaceKey + "\x00" + k.uri
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
