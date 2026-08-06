package multilsp

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func recycleRestoreContext(scope ResolvedLSPToolScope, cfg workspaceConfig) context.Context {
	ctx := context.Background()
	scope = recycleResolvedScope(scope, cfg)
	ctx = common.WithToolScope(ctx, recycleToolScope(scope))
	return WithResolvedLSPToolScope(ctx, scope)
}

// recycleResolvedScope 为回收后的重启补齐 ResolvedLSPToolScope。
// 优先复用已有 manager key；缺失时从 workspace 配置重建，失败则保留原 scope 以便日志仍可关联。
func recycleResolvedScope(scope ResolvedLSPToolScope, cfg workspaceConfig) ResolvedLSPToolScope {
	if scope.WorkspaceKey != "" || scope.ManagerKey != "" {
		return scope
	}
	if parsed, ok := lspScopeWorkspacePartsFromConfig(cfg); ok {
		if resolved, err := ResolveLSPToolScope(parsed); err == nil {
			return resolved
		}
	}
	resolved, err := ResolveLSPToolScope(LSPToolScope{
		LanguageID:            cfg.languageID,
		WorkspaceRoot:         cfg.rootPath,
		LanguageWorkspaceRoot: cfg.rootPath,
		ProjectRoot:           cfg.rootPath,
		RootKind:              "dir_fallback",
	})
	if err != nil {
		return scope
	}
	return resolved
}

func recycleToolScope(scope ResolvedLSPToolScope) common.ToolScope {
	return common.ToolScope{
		Family:   scope.Family,
		AgentID:  scope.AgentID,
		ThreadID: scope.ThreadID,
		TurnID:   scope.TurnID,
		CallID:   scope.CallID,
		CWD:      scope.CWD,
		WorkspaceRoots: append(
			[]string(nil),
			scope.WorkspaceRoots...,
		),
	}
}

func snapshotWorkspaceClients(mgr *manager) []workspaceClient {
	if mgr == nil {
		return nil
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	items := make([]workspaceClient, 0, len(mgr.workspaces))
	for _, workspace := range mgr.workspaces {
		if workspace == nil || workspace.client == nil {
			continue
		}
		items = append(items, *workspace)
	}
	return items
}

func managerIsClosed(mgr *manager) bool {
	if mgr == nil {
		return true
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.closed
}
