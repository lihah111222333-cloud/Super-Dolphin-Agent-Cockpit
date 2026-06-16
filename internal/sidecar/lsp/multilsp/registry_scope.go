package multilsp

import (
	"context"
	"errors"
	"strings"

	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	lspmanager "github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/manager"
)

type registryScopedResolver struct {
	pool *ManagerPool
}

// NewRegistryScopedResolver exposes the manager's pool through the manager
// package's small resolver interface. Keeping the adapter here avoids a
// manager -> multilsp import cycle while ensuring production registry calls
// route through ManagerPool.ForScope.
// NewRegistryScopedResolver 创建注册表scoped解析器。
func NewRegistryScopedResolver(m Manager) lspmanager.ScopedManagerResolver {
	concrete, ok := m.(*manager)
	if !ok || concrete == nil || concrete.pool == nil {
		return nil
	}
	return registryScopedResolver{pool: concrete.pool}
}

// ForToolScope 为工具作用域处理LSP。
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

// CurrentManagersForToolScope 为工具作用域处理当前managers。
func (r registryScopedResolver) CurrentManagersForToolScope(scope lspmanager.ToolScope) ([]lspmanager.ScopedManager, error) {
	if r.pool == nil {
		return nil, errors.New("LSP manager pool is nil")
	}
	scopeKey, err := registryScopeKey(scope)
	if err != nil {
		return nil, err
	}
	currentRoots := r.currentWorkspaceRoots(scope)
	seen := map[lspmanager.Manager]struct{}{}
	out := make([]lspmanager.ScopedManager, 0)
	for _, snapshot := range r.pool.SnapshotManagers() {
		if snapshot.base || snapshot.manager == nil {
			continue
		}
		if snapshot.resolvedScope.ScopeKey != scopeKey {
			continue
		}
		if len(currentRoots) > 0 && ensureLSPToolScopeWithinWorkspaceRoots(currentRoots, snapshot.resolvedScope.LSPToolScope) != nil {
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

func (r registryScopedResolver) currentWorkspaceRoots(scope lspmanager.ToolScope) []string {
	cwd := canonicalScopePath(scope.CWD, "")
	if cwd == "" && r.pool != nil && r.pool.primary != nil {
		cwd = r.pool.primary.workspaceRoot
	}
	return normalizeScopeWorkspaceRoots(cwd, scope.WorkspaceRoots)
}

func (r registryScopedResolver) resolveRegistryScope(scope lspmanager.ToolScope) (LSPToolScope, error) {
	base, err := r.registryBaseScope(scope)
	if err != nil {
		return LSPToolScope{}, err
	}
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
	if err := ensureResolvedLanguageScopeWithinWorkspaceRoots(base.WorkspaceRoots, resolved); err != nil {
		return LSPToolScope{}, err
	}
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

// registryBaseScope 处理注册表base作用域。
func (r registryScopedResolver) registryBaseScope(scope lspmanager.ToolScope) (LSPToolScope, error) {
	cwd := canonicalScopePath(scope.CWD, "")
	if cwd == "" && r.pool != nil && r.pool.primary != nil {
		cwd = r.pool.primary.workspaceRoot
	}
	workspaceRoots := normalizeScopeWorkspaceRoots(cwd, scope.WorkspaceRoots)
	languageID := normalizeLanguageID(scope.LanguageID)
	target := firstNonEmpty(scope.TargetPath, scope.TargetURI)
	selected, err := selectWorkspaceRootForTarget(workspaceRoots, target)
	if err != nil {
		return LSPToolScope{}, err
	}
	if selected != "" {
		cwd = selected
		workspaceRoots = normalizeScopeWorkspaceRoots(cwd, workspaceRoots)
	}
	targetPath := firstNonEmpty(canonicalScopePath(target, cwd), cwd)
	if err := ensurePathWithinWorkspaceRoots(workspaceRoots, targetPath); err != nil {
		return LSPToolScope{}, err
	}
	targetURI := registryTargetURI(scope.TargetURI, targetPath)
	languageID = registryLanguageID(languageID, targetPath)

	return LSPToolScope{
		AgentID:  scope.AgentID,
		ThreadID: scope.ThreadID,
		TurnID:   scope.TurnID,
		CallID:   scope.CallID,
		CWD:      cwd,
		WorkspaceRoots: append(
			[]string(nil),
			workspaceRoots...,
		),
		Family:     scope.Family,
		LanguageID: languageID,
		TargetPath: targetPath,
		TargetURI:  targetURI,
	}, nil
}

func registryTargetURI(rawURI, targetPath string) string {
	if uri := canonicalScopeURI(rawURI); uri != "" {
		return uri
	}
	if targetPath == "" {
		return ""
	}
	return fileURIFromPath(targetPath)
}

func registryLanguageID(languageID, targetPath string) string {
	if languageID != "" {
		return languageID
	}
	return normalizeLanguageID(lspmanager.DetectLanguageID(targetPath))
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
	}
}
