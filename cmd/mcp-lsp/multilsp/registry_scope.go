package multilsp

import (
	"errors"
	"os"
	"path/filepath"
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
	resolved := r.registryBaseScope(scope)
	if shouldUseGoWorkspace(resolved.LanguageID) {
		return registryGoScope(resolved)
	}

	root, err := registryWorkspaceRoot(resolved.LanguageID, resolved.TargetPath, resolved.CWD)
	if err != nil {
		return LSPToolScope{}, err
	}
	resolved.RootKind = "dir_fallback"
	resolved.WorkspaceRoot = root
	resolved.LanguageWorkspaceRoot = root
	resolved.ProjectRoot = root
	return resolved, nil
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

func registryGoScope(scope LSPToolScope) (LSPToolScope, error) {
	info, err := ResolveGoRoot(GoRootRequest{CWD: scope.CWD, FilePath: scope.TargetPath, Env: os.Environ()})
	if err != nil {
		return LSPToolScope{}, err
	}
	parts := goWorkspaceKeyPartsFor(info)
	scope.RootKind = parts.RootKind
	scope.WorkspaceRoot = parts.WorkspaceRoot
	scope.LanguageWorkspaceRoot = parts.LanguageWorkspaceRoot
	scope.ProjectRoot = parts.ProjectRoot
	scope.LanguageSpecific = parts.LanguageSpecific
	return scope, nil
}

func registryWorkspaceRoot(languageID, targetPath, cwd string) (string, error) {
	root, err := registryProjectRoot(languageID, targetPath, cwd)
	if err != nil {
		return "", err
	}
	root, err = registryLanguageRoot(languageID, root)
	if err != nil {
		return "", err
	}
	return normalizeRegistryWorkspaceRoot(root)
}

func registryProjectRoot(languageID, targetPath, cwd string) (string, error) {
	root := firstNonEmpty(cwd, filepath.Dir(targetPath))
	if targetPath != "" {
		if projectRoot, err := resolveProjectRoot(languageID, targetPath); err != nil {
			return "", err
		} else if projectRoot != "" {
			root = projectRoot
		}
	}
	return root, nil
}

func registryLanguageRoot(languageID, root string) (string, error) {
	switch {
	case shouldUseJSTSWorkspace(languageID):
		return registryJSTSRoot(root)
	case shouldUseJavaWorkspace(languageID):
		return registryJavaRoot(root)
	default:
		return root, nil
	}
}

func registryJSTSRoot(root string) (string, error) {
	jsRoot, err := findJSTSProjectRoot(root)
	if err != nil {
		return "", err
	}
	return firstNonEmpty(jsRoot, findJSTSProjectRootWithin(root), root), nil
}

func registryJavaRoot(root string) (string, error) {
	javaRoot, err := findJavaProjectRoot(root)
	if err != nil {
		return "", err
	}
	return firstNonEmpty(javaRoot, findJavaProjectRootWithin(root), root), nil
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
