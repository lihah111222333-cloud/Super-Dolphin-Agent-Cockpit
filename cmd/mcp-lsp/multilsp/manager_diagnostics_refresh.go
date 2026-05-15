package multilsp

import (
	"context"
	"path/filepath"
	"strings"
)

func (m *manager) refreshAllDiagnosticTargets(ctx context.Context, filter diagnosticFilter) error {
	refs := m.allDiagnosticRefreshCandidates(ctx, filter)
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
			m.cleanupDeletedDocument(ref, current)
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

func (m *manager) allDiagnosticRefreshCandidates(ctx context.Context, filter diagnosticFilter) []documentRef {
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
		for _, record := range bootstrapCoordinatorFor(m).cache.ScopeDocuments(resolved) {
			add(record.Key.URI, cacheKeyLanguage(record.Key))
		}
	}
	return refs
}

func (m *manager) refreshDiagnosticRef(ctx context.Context, ref documentRef) error {
	cfg, err := m.resolveWorkspaceForDocument(ctx, ref)
	if err != nil {
		return err
	}
	return bootstrapCoordinatorFor(m).syncDocument(ctx, m, cfg, ref)
}
