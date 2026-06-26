package multilsp

import (
	"context"
	"path/filepath"
	"strings"
)

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
