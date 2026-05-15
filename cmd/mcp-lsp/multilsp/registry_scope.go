package multilsp

import (
	"context"
	"errors"
	"strings"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type registryScopedResolver struct {
	pool *ManagerPool
}

// NewRegistryScopedResolver exposes the manager's pool through the manager
// package's small resolver interface. Keeping the adapter here avoids a
// manager -> multilsp import cycle while ensuring production registry calls
// route through ManagerPool.ForScope.
func NewRegistryScopedResolver(m Manager) lspmanager.ScopedManagerResolver {
	concrete, ok := m.(*manager)
	if !ok || concrete == nil || concrete.pool == nil {
		return nil
	}
	return registryScopedResolver{pool: concrete.pool}
}

func (r registryScopedResolver) ForToolScope(scope lspmanager.ToolScope) (lspmanager.ScopedManager, error) {
	if r.pool == nil {
		return lspmanager.ScopedManager{}, errors.New("LSP manager pool is nil")
	}
	lspScope, err := r.resolveRegistryScope(scope)
	if err != nil {
		return lspmanager.ScopedManager{}, err
	}
	scoped, err := r.pool.ForScope(lspScope)
	if err != nil {
		return lspmanager.ScopedManager{}, err
	}
	return managerScopedManager(scoped), nil
}

func (r registryScopedResolver) CurrentManagersForToolScope(scope lspmanager.ToolScope) ([]lspmanager.ScopedManager, error) {
	if r.pool == nil {
		return nil, errors.New("LSP manager pool is nil")
	}
	scopeKey, err := registryScopeKey(scope)
	if err != nil {
		return nil, err
	}
	seen := map[lspmanager.Manager]struct{}{}
	out := make([]lspmanager.ScopedManager, 0)
	for _, snapshot := range r.pool.SnapshotManagers() {
		if snapshot.base || snapshot.manager == nil {
			continue
		}
		if snapshot.resolvedScope.ScopeKey != scopeKey {
			continue
		}
		if _, ok := seen[snapshot.manager]; ok {
			continue
		}
		seen[snapshot.manager] = struct{}{}
		out = append(out, managerScopedManager(ScopedManager{
			Manager:       snapshot.manager,
			ResolvedScope: snapshot.resolvedScope,
		}))
	}
	return out, nil
}

func (r registryScopedResolver) resolveRegistryScope(scope lspmanager.ToolScope) (LSPToolScope, error) {
	base := r.registryBaseScope(scope)
	adapter, err := r.adapterForLanguage(base.LanguageID)
	if err != nil {
		return LSPToolScope{}, err
	}
	resolved, err := adapter.ResolveRoot(context.Background(), base, base.TargetPath)
	if err != nil {
		return LSPToolScope{}, err
	}
	resolved = completeResolvedLanguageScope(resolved, base)
	resolved.LanguageSpecific = mergeLanguageSpecific(resolved.LanguageSpecific, adapter.CacheKeyParts(resolved))
	base.LanguageID = resolved.LanguageID
	base.RootKind = resolved.RootKind
	base.WorkspaceRoot = resolved.WorkspaceRoot
	base.LanguageWorkspaceRoot = resolved.LanguageWorkspaceRoot
	base.ProjectRoot = resolved.ProjectRoot
	base.LanguageSpecific = copyLanguageSpecific(resolved.LanguageSpecific)
	return base, nil
}

func (r registryScopedResolver) adapterForLanguage(languageID string) (LanguageAdapter, error) {
	if r.pool != nil && r.pool.primary != nil {
		return r.pool.primary.adapterForLanguage(languageID)
	}
	registry := NewDefaultLanguageAdapterRegistry()
	adapter, ok := registry.AdapterForLanguage(languageID)
	if !ok {
		return nil, errors.New("unsupported language adapter " + normalizeLanguageID(languageID))
	}
	return adapter, nil
}

func (r registryScopedResolver) registryBaseScope(scope lspmanager.ToolScope) LSPToolScope {
	cwd := canonicalScopePath(scope.CWD, "")
	if cwd == "" && r.pool != nil && r.pool.primary != nil {
		cwd = r.pool.primary.workspaceRoot
	}
	languageID := normalizeLanguageID(scope.LanguageID)
	targetPath := canonicalScopePath(firstNonEmpty(scope.TargetPath, scope.TargetURI), cwd)
	targetURI := canonicalScopeURI(scope.TargetURI)
	if targetPath == "" {
		targetPath = cwd
	}
	if targetURI == "" && targetPath != "" {
		targetURI = fileURIFromPath(targetPath)
	}
	if languageID == "" {
		languageID = normalizeLanguageID(lspmanager.DetectLanguageID(targetPath))
	}

	return LSPToolScope{
		AgentID:    scope.AgentID,
		ThreadID:   scope.ThreadID,
		TurnID:     scope.TurnID,
		CallID:     scope.CallID,
		CWD:        cwd,
		Family:     scope.Family,
		LanguageID: languageID,
		TargetPath: targetPath,
		TargetURI:  targetURI,
	}
}

func normalizeRegistryWorkspaceRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", ErrWorkspaceRootEmpty
	}
	normalized, err := platformshared.NormalizeAbsolutePath(root)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func registryScopeKey(scope lspmanager.ToolScope) (string, error) {
	family := normalizeScopeFamily(scope.Family)
	if family != defaultLSPToolFamily {
		return "", errors.New("unsupported LSP scope family " + family)
	}
	return buildScopeKey(LSPToolScope{
		AgentID:  strings.TrimSpace(scope.AgentID),
		ThreadID: strings.TrimSpace(scope.ThreadID),
		Family:   family,
	}), nil
}

func managerScopedManager(scoped ScopedManager) lspmanager.ScopedManager {
	return lspmanager.ScopedManager{
		Manager: scoped.Manager,
		ResolvedScope: lspmanager.ResolvedToolScope{
			ToolScope:    managerToolScope(scoped.ResolvedScope.LSPToolScope),
			ScopeKey:     scoped.ResolvedScope.ScopeKey,
			WorkspaceKey: scoped.ResolvedScope.WorkspaceKey,
			ShardKey:     scoped.ResolvedScope.ShardKey,
			ManagerKey:   scoped.ResolvedScope.ManagerKey,
		},
	}
}

func managerToolScope(scope LSPToolScope) lspmanager.ToolScope {
	return lspmanager.ToolScope{
		AgentID:               scope.AgentID,
		ThreadID:              scope.ThreadID,
		TurnID:                scope.TurnID,
		CallID:                scope.CallID,
		CWD:                   scope.CWD,
		Family:                scope.Family,
		LanguageID:            scope.LanguageID,
		TargetPath:            scope.TargetPath,
		TargetURI:             scope.TargetURI,
		WorkspaceRoot:         scope.WorkspaceRoot,
		RootKind:              scope.RootKind,
		LanguageWorkspaceRoot: scope.LanguageWorkspaceRoot,
		ProjectRoot:           scope.ProjectRoot,
		LanguageSpecific:      copyLanguageSpecific(scope.LanguageSpecific),
	}
}
