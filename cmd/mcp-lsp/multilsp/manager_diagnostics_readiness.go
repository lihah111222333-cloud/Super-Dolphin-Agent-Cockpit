package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

const (
	documentDiagnosticKindFull      = "full"
	documentDiagnosticKindUnchanged = "unchanged"
)

type diagnosticReadiness struct {
	latest  time.Time
	missing []string
}

type diagnosticStableWait struct {
	manager   *manager
	ctx       context.Context
	filter    diagnosticFilter
	uris      []string
	readiness diagnosticReadiness
	last      time.Time
	lastPull  time.Time
	missingAt time.Time
}

func (m *manager) newDiagnosticStableWait(ctx context.Context, filter diagnosticFilter, uris []string) (*diagnosticStableWait, error) {
	readiness, err := m.diagnosticReadiness(ctx, filter, uris)
	if err != nil {
		return nil, err
	}
	wait := &diagnosticStableWait{
		manager:   m,
		ctx:       ctx,
		filter:    filter,
		uris:      uris,
		readiness: readiness,
		last:      readiness.latest,
	}
	wait.trackMissingSince(time.Now())
	return wait, nil
}

// wait 等待LSP。
func (w *diagnosticStableWait) wait() error {
	for {
		if err := w.contextError(w.ctx.Err()); err != nil {
			return err
		}
		if len(w.readiness.missing) > 0 {
			if err := w.refresh(true); err != nil {
				return err
			}
			continue
		}
		if w.last.IsZero() || time.Since(w.last) >= w.manager.diagPoll {
			return nil
		}
		if err := w.refresh(false); err != nil {
			return err
		}
	}
}

func (w *diagnosticStableWait) contextError(err error) error {
	if err == nil {
		return nil
	}
	if len(w.readiness.missing) > 0 {
		return fmt.Errorf("%w: diagnostics did not publish for requested targets before context finished: %s: %w", lspmanager.ErrDiagnosticsNotReady, strings.Join(w.readiness.missing, ", "), err)
	}
	return fmt.Errorf("%w: diagnostics did not stabilize before context finished: %w", lspmanager.ErrDiagnosticsNotReady, err)
}

// refresh 刷新LSP。
func (w *diagnosticStableWait) refresh(resetLast bool) error {
	waitFor := w.manager.diagPoll
	if deadline, ok := w.ctx.Deadline(); ok {
		waitFor = minDuration(waitFor, time.Until(deadline))
	}
	if err := sleepContext(w.ctx, waitFor); err != nil {
		return w.contextError(err)
	}
	if len(w.readiness.missing) > 0 {
		if err := w.pullMissingDiagnosticsIfDue(); err != nil {
			return err
		}
		if w.missingDiagnosticsMayBeEmpty(time.Now()) {
			if err := w.manager.markReadyForOmittedEmptyDiagnostics(w.ctx, w.filter, w.uris); err != nil {
				return err
			}
		}
	}
	readiness, err := w.manager.diagnosticReadiness(w.ctx, w.filter, w.uris)
	if err != nil {
		return err
	}
	w.readiness = readiness
	w.trackMissingSince(time.Now())
	if resetLast || readiness.latest.After(w.last) {
		w.last = readiness.latest
	}
	return nil
}

func (w *diagnosticStableWait) trackMissingSince(now time.Time) {
	if len(w.readiness.missing) == 0 {
		w.missingAt = time.Time{}
		return
	}
	if w.missingAt.IsZero() {
		w.missingAt = now
	}
}

func (w *diagnosticStableWait) missingDiagnosticsMayBeEmpty(now time.Time) bool {
	return len(w.readiness.missing) > 0 &&
		!w.missingAt.IsZero() &&
		now.Sub(w.missingAt) >= diagnosticsMissingEmptyGrace(w.manager.diagPoll)
}

// refreshAllDiagnosticTargets 刷新当前 filter 下已知的诊断目标。
// 候选数量受 maxRefreshFiles 限制；缺失文件会同步走删除清理，不再请求 LSP。
func (m *manager) refreshAllDiagnosticTargets(ctx context.Context, filter diagnosticFilter) error {
	refs, err := m.allDiagnosticRefreshCandidates(ctx, filter)
	if err != nil {
		return err
	}
	if len(refs) > maxRefreshFiles {
		refs = refs[:maxRefreshFiles]
	}
	for _, ref := range refs {
		if ref.uri == "" {
			continue
		}
		if !fileExists(ref.absPath) {
			current := ResolvedLSPToolScope{
				LSPToolScope: LSPToolScope{
					WorkspaceRoot: filepath.Dir(ref.absPath),
					LanguageID:    ref.languageID,
				},
				ScopeKey:     filter.scopeKey,
				WorkspaceKey: filter.workspaceKey,
			}
			if _, _, scope, err := m.resolvedScopeForURI(ctx, ref.uri, ref.languageID); err == nil {
				current = scope
			}
			if err := m.cleanupDeletedDocument(ref, current); err != nil {
				return err
			}
			continue
		}
		if !m.shouldUseClientForLanguage(ref.languageID) {
			continue
		}
		if err := m.refreshDiagnosticRef(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

// allDiagnosticRefreshCandidates 汇总当前诊断快照和 scope cache 中的刷新候选。
// URI 会按 language 去重，解析失败的旧记录会被跳过，避免一次 refresh 被陈旧缓存阻断。
func (m *manager) allDiagnosticRefreshCandidates(ctx context.Context, filter diagnosticFilter) ([]documentRef, error) {
	seen := map[string]struct{}{}
	var refs []documentRef
	add := func(uri, languageID string) {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			return
		}
		key := normalizeLanguageID(languageID) + "\x00" + uri
		if _, ok := seen[key]; ok {
			return
		}
		ref, err := m.resolveDocumentRef(ctx, uri, languageID)
		if err != nil {
			return
		}
		seen[key] = struct{}{}
		refs = append(refs, ref)
	}
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		add(snapshot.uri, snapshot.language)
	})
	if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
		coordinator, err := bootstrapCoordinatorFor(m)
		if err != nil {
			return nil, err
		}
		for _, record := range coordinator.cache.ScopeDocuments(resolved) {
			add(record.Key.URI, cacheKeyLanguage(record.Key))
		}
	}
	return refs, nil
}

func (m *manager) refreshDiagnosticRef(ctx context.Context, ref documentRef) error {
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return err
	}
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return err
	}
	return coordinator.syncDocument(ctx, m, cfg, ref)
}

// pullMissingDiagnosticsIfDue 在等待窗口内节流重试 pull diagnostics。
// 某些语言服务器启动后只发 workspace/diagnostic/refresh，不主动 publish；这里按通用能力重新拉取缺失目标。
func (w *diagnosticStableWait) pullMissingDiagnosticsIfDue() error {
	interval := diagnosticsPullRetryInterval(w.manager.diagPoll)
	now := time.Now()
	if !w.lastPull.IsZero() && now.Sub(w.lastPull) < interval {
		return nil
	}
	w.lastPull = now
	return w.manager.pullMissingDiagnostics(w.ctx, w.filter, w.uris)
}

// diagnosticsPullRetryInterval 根据稳定轮询间隔推导 pull 重试节奏，避免对语言服务器高频打点。
func diagnosticsPullRetryInterval(poll time.Duration) time.Duration {
	if poll <= 0 {
		return 200 * time.Millisecond
	}
	interval := poll * 5
	if interval < 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	if interval > 500*time.Millisecond {
		return 500 * time.Millisecond
	}
	return interval
}

func diagnosticsMissingEmptyGrace(poll time.Duration) time.Duration {
	return diagnosticsPullRetryInterval(poll)
}

func (m *manager) diagnosticReadiness(ctx context.Context, filter diagnosticFilter, uris []string) (diagnosticReadiness, error) {
	if len(uris) == 0 {
		return diagnosticReadiness{latest: m.latestDiagnosticUpdate(filter)}, nil
	}
	return m.requestedDiagnosticReadiness(ctx, filter, uniqueDiagnosticURIs(uris))
}

func (m *manager) requestedDiagnosticReadiness(ctx context.Context, filter diagnosticFilter, uris []string) (diagnosticReadiness, error) {
	ready := m.readyDiagnosticSnapshots(filter)
	result := diagnosticReadiness{}
	for _, uri := range uris {
		ref, ok, err := m.awaitableDiagnosticRef(ctx, uri)
		if err != nil {
			return diagnosticReadiness{}, err
		}
		if !ok {
			continue
		}
		result.observe(uri, ready[ref.uri])
	}
	sort.Strings(result.missing)
	return result, nil
}

// pullMissingDiagnostics 对尚未收到 publishDiagnostics 的显式目标尝试 LSP pull diagnostics。
func (m *manager) pullMissingDiagnostics(ctx context.Context, filter diagnosticFilter, uris []string) error {
	if len(uris) == 0 || m.factory == nil {
		return nil
	}
	ready := m.readyDiagnosticSnapshots(filter)
	for _, uri := range uniqueDiagnosticURIs(uris) {
		ref, ok, err := m.awaitableDiagnosticRef(ctx, uri)
		if err != nil {
			return err
		}
		if !ok || !ready[ref.uri].IsZero() {
			continue
		}
		if err := m.pullDocumentDiagnostics(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

// pullDocumentDiagnostics 从声明 diagnosticProvider 的 LSP server 主动拉取单文档诊断。
func (m *manager) pullDocumentDiagnostics(ctx context.Context, ref documentRef) error {
	client, err := m.ensureClientForFile(ctx, ref.absPath, ref.languageID)
	if err != nil {
		return err
	}
	if !clientSupportsPullDiagnostics(client) {
		return nil
	}
	raw, err := m.request(ctx, client, protocol.MethodTextDocumentDiagnostic, protocol.DocumentDiagnosticParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: ref.uri},
	})
	if err != nil {
		return fmt.Errorf("pull diagnostics %s: %w", ref.uri, err)
	}
	params, ok, err := decodePulledDiagnostics(ref.uri, raw)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	return m.PublishDiagnostics(params)
}

// markReadyForOmittedEmptyDiagnostics 为允许省略空推送的语言写入空诊断快照。
func (m *manager) markReadyForOmittedEmptyDiagnostics(ctx context.Context, filter diagnosticFilter, uris []string) error {
	if len(uris) == 0 || m.factory == nil {
		return nil
	}
	ready := m.readyDiagnosticSnapshots(filter)
	for _, uri := range uniqueDiagnosticURIs(uris) {
		ref, ok, err := m.awaitableDiagnosticRef(ctx, uri)
		if err != nil {
			return err
		}
		if !ok || !ready[ref.uri].IsZero() {
			continue
		}
		allowEmpty, err := m.diagnosticsMayBeEmptyWithoutPublish(ctx, ref)
		if err != nil {
			return err
		}
		if !allowEmpty {
			continue
		}
		if err := m.PublishDiagnostics(protocol.PublishDiagnosticsParams{URI: ref.uri}); err != nil {
			return err
		}
	}
	return nil
}

// diagnosticsMayBeEmptyWithoutPublish 判断目标文档是否已完成启动且允许缺省空诊断。
func (m *manager) diagnosticsMayBeEmptyWithoutPublish(ctx context.Context, ref documentRef) (bool, error) {
	scope, adapter, err := m.resolveLanguageScope(ctx, ref.languageID, ref.absPath, ref.uri)
	if err != nil {
		return false, err
	}
	if !adapter.BootstrapPolicy(scope).TreatMissingDiagnosticsAsEmpty {
		return false, nil
	}
	cfg, err := workspaceConfigForLanguageScope(scope, adapter)
	if err != nil {
		return false, err
	}
	resolved, err := m.resolvedScopeForConfig(ctx, cfg)
	if err != nil {
		return false, err
	}
	coordinator, err := bootstrapCoordinatorFor(m)
	if err != nil {
		return false, err
	}
	return coordinator.states.status(resolved.bootstrapKey(), ref.uri) == bootstrapReady, nil
}

func clientSupportsPullDiagnostics(client Client) bool {
	capClient, ok := client.(ServerCapabilitiesClient)
	if !ok {
		return false
	}
	return serverCapabilityAvailable(capClient.ServerCapabilities().DiagnosticProvider)
}

func serverCapabilityAvailable(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	default:
		return true
	}
}

// decodePulledDiagnostics 把 textDocument/diagnostic 结果转换成统一诊断快照载荷。
func decodePulledDiagnostics(uri string, raw json.RawMessage) (protocol.PublishDiagnosticsParams, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return protocol.PublishDiagnosticsParams{}, false, nil
	}
	var report protocol.DocumentDiagnosticReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return protocol.PublishDiagnosticsParams{}, false, fmt.Errorf("decode pulled diagnostics: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(report.Kind)) {
	case documentDiagnosticKindFull:
		return protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: report.Items}, true, nil
	case documentDiagnosticKindUnchanged:
		return protocol.PublishDiagnosticsParams{}, false, nil
	default:
		return protocol.PublishDiagnosticsParams{}, false, fmt.Errorf("decode pulled diagnostics: unsupported report kind %q", report.Kind)
	}
}

func (m *manager) readyDiagnosticSnapshots(filter diagnosticFilter) map[string]time.Time {
	ready := make(map[string]time.Time)
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		if snapshot.state == diagnosticStateReady {
			ready[snapshot.uri] = snapshot.updatedAt
		}
	})
	return ready
}

func (m *manager) awaitableDiagnosticRef(ctx context.Context, uri string) (documentRef, bool, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return documentRef{}, false, err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) || !fileExists(ref.absPath) {
		return documentRef{}, false, nil
	}
	return ref, true, nil
}

func (r *diagnosticReadiness) observe(uri string, updatedAt time.Time) {
	if updatedAt.IsZero() {
		r.missing = append(r.missing, uri)
		return
	}
	if updatedAt.After(r.latest) {
		r.latest = updatedAt
	}
}

func canonicalDiagnosticURI(uri string) (string, error) {
	path, err := absolutePathFromURI(uri)
	if err != nil {
		return "", err
	}
	return fileURIFromPath(path), nil
}

func uniqueDiagnosticURIs(uris []string) []string {
	out := make([]string, 0, len(uris))
	seen := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		out = append(out, uri)
	}
	return out
}
