package multilsp

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestReleaseScopeClearsDiagnosticsBootstrapAndCache(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	scope := testLSPToolScope(root, "agent-clean", "thread-1")
	scoped := scopedManagerForTest(t, mgr, scope)
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope: %v", err)
	}
	uri := fileURIFromPath(filepath.Join(root, "main.go"))
	coordinator := mustBootstrapCoordinator(t, scoped)
	key := resolved.cacheKey("go", uri)
	coordinator.cache.Upsert(lspCacheValue{Key: key, Version: 1, UpdatedAt: time.Now()})
	coordinator.states.complete(resolved.bootstrapKey(), uri, "fp", 1)
	scoped.diagnostics[diagnosticStoreKeyFor(resolved, uri).String()] = diagnosticSnapshot{
		scopeKey:     resolved.ScopeKey,
		workspaceKey: resolved.WorkspaceKey,
		language:     "go",
		uri:          uri,
		generation:   scoped.CurrentDiagnosticGeneration(),
		state:        diagnosticStateReady,
		params:       protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "stale"}}},
	}

	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-clean", ThreadID: "thread-1", Drain: true})
	if err != nil {
		t.Fatalf("ReleaseScope(clean): %v", err)
	}
	if result.ClosedManagers != 1 {
		t.Fatalf("ClosedManagers = %d, want 1", result.ClosedManagers)
	}
	scoped.coordinatorMu.Lock()
	hasCoordinator := scoped.coordinator != nil
	scoped.coordinatorMu.Unlock()
	if hasCoordinator {
		t.Fatalf("bootstrap/cache coordinator survived ReleaseScope close")
	}
	if !managerIsClosed(scoped) {
		t.Fatalf("scoped manager was not closed")
	}
}

func TestReleaseScopeRejectsEmptyIdentityWithoutExplicitScopeKind(t *testing.T) {
	mgr := newManagerPoolTestManager(t, canonicalScopePath(t.TempDir(), ""))
	if _, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{}); err == nil {
		t.Fatalf("ReleaseScope(empty) error = nil, want explicit scope kind/identity rejection")
	}
}
