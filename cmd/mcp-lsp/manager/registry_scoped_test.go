package manager

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestRegistryResolveManagerForFileUsesTrustedToolScope(t *testing.T) {
	singleton := &registryDiagnosticsManager{}
	scopedMgr := &registryDiagnosticsManager{}
	resolver := &recordingScopedResolver{manager: scopedMgr}
	registry := NewRegistry(nil)
	registry.Register("go", singleton, resolver)

	ctx := common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), common.ToolScope{
		AgentID:  "agent-trusted",
		ThreadID: "thread-trusted",
		CallID:   "call-trusted",
		CWD:      "/trusted/worktree",
		Family:   "lsp",
	})
	scoped, err := registry.ResolveManagerForFile(ctx, "main.go")
	if err != nil {
		t.Fatalf("ResolveManagerForFile: %v", err)
	}
	if scoped.Manager == scopedMgr {
		t.Fatalf("resolved manager returned bare scoped manager; want resolved-scope wrapper")
	}
	if _, err := scoped.Manager.Diagnostics(ctx, nil); err != nil {
		t.Fatalf("wrapped Diagnostics: %v", err)
	}
	assertResolvedManagerKey(t, scopedMgr.diagnosticsContext, "manager-key")
	assertResolverScopeForTrustedFile(t, resolver.lastScope)
}

func TestRegistryResolveManagerForFileForwardsTrustedWorkspaceRoots(t *testing.T) {
	singleton := &registryDiagnosticsManager{}
	scopedMgr := &registryDiagnosticsManager{}
	resolver := &recordingScopedResolver{manager: scopedMgr}
	registry := NewRegistry(nil)
	registry.Register("go", singleton, resolver)

	tmp := t.TempDir()
	primary := filepath.Join(tmp, "repo")
	extra := filepath.Join(tmp, "other")
	target := filepath.Join(extra, "main.go")
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:        "agent-trusted",
		ThreadID:       "thread-trusted",
		CWD:            primary,
		WorkspaceRoots: []string{extra},
		Family:         "lsp",
	})

	if _, err := registry.ResolveManagerForFile(ctx, target); err != nil {
		t.Fatalf("ResolveManagerForFile: %v", err)
	}
	want := []string{filepath.Clean(primary), filepath.Clean(extra)}
	if !reflect.DeepEqual(resolver.lastScope.WorkspaceRoots, want) {
		t.Fatalf("resolver WorkspaceRoots = %#v, want %#v", resolver.lastScope.WorkspaceRoots, want)
	}
}

func TestRegistryDiagnosticsAllUsesCurrentScopedManagers(t *testing.T) {
	singleton := &registryDiagnosticsManager{}
	scopedMgr := &registryDiagnosticsManager{}
	resolver := &recordingScopedResolver{
		current: []ScopedManager{{
			Manager: scopedMgr,
			ResolvedScope: ResolvedToolScope{
				ScopeKey:     "lsp\x00agent-a\x00thread-a",
				WorkspaceKey: "workspace-a",
				ManagerKey:   "manager-a",
			},
		}},
	}
	registry := NewRegistry(nil)
	registry.Register("go", singleton, resolver)

	ctx := common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), common.ToolScope{
		AgentID:  "agent-a",
		ThreadID: "thread-a",
		CWD:      "/trusted/worktree",
		Family:   "lsp",
	})
	if _, err := registry.Diagnostics(ctx, nil); err != nil {
		t.Fatalf("Diagnostics(ctx, nil): %v", err)
	}
	if singleton.diagnosticsContext != nil {
		t.Fatalf("Diagnostics(ctx,nil) called singleton manager; want current scoped managers only")
	}
	if scopedMgr.diagnosticsContext == nil {
		t.Fatalf("Diagnostics(ctx,nil) did not call scoped manager")
	}
	if resolved, ok := ResolvedToolScopeFromContext(scopedMgr.diagnosticsContext); !ok || resolved.ManagerKey != "manager-a" {
		t.Fatalf("Diagnostics(ctx,nil) resolved scope = %#v ok=%v, want manager-a", resolved, ok)
	}
	if resolver.currentScope.AgentID != "agent-a" || resolver.currentScope.ThreadID != "thread-a" {
		t.Fatalf("current scope = %#v, want trusted scope", resolver.currentScope)
	}
}

func TestDiagnosticsAllUsesCallerScopeOnly(t *testing.T) {
	singleton := &scopedRecordingDiagnosticsManager{}
	callerMgr := &scopedRecordingDiagnosticsManager{}
	otherMgr := &scopedRecordingDiagnosticsManager{}
	resolver := &recordingScopedResolver{
		currentByScope: map[string][]ScopedManager{
			recordingScopeIdentity("agent-a", "thread-a"): {{
				Manager: callerMgr,
				ResolvedScope: ResolvedToolScope{
					ToolScope: ToolScope{
						AgentID:    "agent-a",
						ThreadID:   "thread-a",
						CWD:        "/trusted/worktree",
						Family:     "lsp",
						LanguageID: "go",
					},
					ScopeKey:     "lsp\x00agent-a\x00thread-a",
					WorkspaceKey: "workspace-a",
					ManagerKey:   "manager-a",
				},
			}},
			recordingScopeIdentity("agent-b", "thread-a"): {{
				Manager: otherMgr,
				ResolvedScope: ResolvedToolScope{
					ScopeKey:     "lsp\x00agent-b\x00thread-a",
					WorkspaceKey: "workspace-b",
					ManagerKey:   "manager-b",
				},
			}},
		},
	}
	registry := NewRegistry(nil)
	registry.Register("go", singleton, resolver)

	ctx := common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), common.ToolScope{
		AgentID:  "agent-a",
		ThreadID: "thread-a",
		CWD:      "/trusted/worktree",
		Family:   "lsp",
	})
	if _, err := registry.Diagnostics(ctx, nil); err != nil {
		t.Fatalf("Diagnostics(ctx, nil): %v", err)
	}

	if singleton.diagnosticsCalls != 0 {
		t.Fatalf("Diagnostics(ctx,nil) called singleton %d times", singleton.diagnosticsCalls)
	}
	if callerMgr.diagnosticsCalls != 1 {
		t.Fatalf("caller scoped manager calls = %d, want 1", callerMgr.diagnosticsCalls)
	}
	if otherMgr.diagnosticsCalls != 0 {
		t.Fatalf("other caller scoped manager calls = %d, want 0", otherMgr.diagnosticsCalls)
	}
	if len(callerMgr.lastDiagnosticsURIs) != 0 {
		t.Fatalf("Diagnostics(ctx,nil) uris = %#v, want nil/all-current", callerMgr.lastDiagnosticsURIs)
	}
	if resolved, ok := ResolvedToolScopeFromContext(callerMgr.lastDiagnosticsContext); !ok || resolved.ManagerKey != "manager-a" {
		t.Fatalf("caller diagnostics resolved scope = %#v ok=%v, want manager-a", resolved, ok)
	}
	if resolver.currentScope.AgentID != "agent-a" || resolver.currentScope.ThreadID != "thread-a" {
		t.Fatalf("current scope = %#v, want caller scope only", resolver.currentScope)
	}
}

func TestRegistryGroupURIsUsesCallerContext(t *testing.T) {
	singleton := &scopedRecordingDiagnosticsManager{}
	scopedMgr := &scopedRecordingDiagnosticsManager{}
	resolver := &recordingScopedResolver{manager: scopedMgr}
	registry := NewRegistry(nil)
	registry.Register("go", singleton, resolver)

	const uri = "file:///tmp/registry-group-main.go"
	ctx := common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: "/"}), common.ToolScope{
		AgentID:  "agent-group",
		ThreadID: "thread-group",
		CallID:   "call-group",
		CWD:      "/caller/worktree",
		Family:   "lsp",
	})
	ctx = context.WithValue(ctx, registryScopedCallerKey{}, "caller-value")
	if _, err := registry.Diagnostics(ctx, []string{uri}); err != nil {
		t.Fatalf("Diagnostics(group uri): %v", err)
	}

	if singleton.diagnosticsCalls != 0 {
		t.Fatalf("grouped URI diagnostics called singleton %d times", singleton.diagnosticsCalls)
	}
	if scopedMgr.diagnosticsCalls != 1 {
		t.Fatalf("grouped URI diagnostics calls = %d, want 1", scopedMgr.diagnosticsCalls)
	}
	assertSingleDiagnosticsURI(t, scopedMgr.lastDiagnosticsURIs, uri)
	if got := scopedMgr.lastDiagnosticsContext.Value(registryScopedCallerKey{}); got != "caller-value" {
		t.Fatalf("diagnostics caller context value = %#v, want caller-value", got)
	}
	assertResolvedManagerKey(t, scopedMgr.lastDiagnosticsContext, "manager-key")
	assertResolverScopeForGroupURI(t, resolver.lastScope, uri)
	if resolver.lastScope.TargetPath != "/tmp/registry-group-main.go" {
		t.Fatalf("resolver target path = %q, want URI path", resolver.lastScope.TargetPath)
	}
}

func assertResolvedManagerKey(t *testing.T, ctx context.Context, want string) {
	t.Helper()
	resolved, ok := ResolvedToolScopeFromContext(ctx)
	if !ok || resolved.ManagerKey != want {
		t.Fatalf("resolved scope = %#v ok=%v, want manager key %q", resolved, ok, want)
	}
}

func assertResolverScopeForTrustedFile(t *testing.T, scope ToolScope) {
	t.Helper()
	if scope.AgentID != "agent-trusted" || scope.ThreadID != "thread-trusted" {
		t.Fatalf("resolver scope identity = %#v, want trusted agent/thread", scope)
	}
	if scope.CWD != "/trusted/worktree" {
		t.Fatalf("resolver CWD = %q, want trusted cwd", scope.CWD)
	}
	if scope.LanguageID != "go" || scope.TargetPath != "main.go" {
		t.Fatalf("resolver target scope = %#v", scope)
	}
}

func assertSingleDiagnosticsURI(t *testing.T, got []string, want string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("grouped URI subset = %#v, want [%s]", got, want)
	}
	if got[0] != want {
		t.Fatalf("grouped URI subset = %#v, want [%s]", got, want)
	}
}

func assertResolverScopeForGroupURI(t *testing.T, scope ToolScope, wantURI string) {
	t.Helper()
	if scope.AgentID != "agent-group" || scope.ThreadID != "thread-group" {
		t.Fatalf("resolver identity = %#v, want caller scope", scope)
	}
	if scope.CWD != "/caller/worktree" || scope.TargetURI != wantURI {
		t.Fatalf("resolver target/cwd scope = %#v", scope)
	}
}

type registryScopedCallerKey struct{}

type scopedRecordingDiagnosticsManager struct {
	registryDiagnosticsManager
	lastDiagnosticsContext context.Context
	lastDiagnosticsURIs    []string
	diagnosticsCalls       int
}

func (m *scopedRecordingDiagnosticsManager) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	m.diagnosticsCalls++
	m.lastDiagnosticsContext = ctx
	m.lastDiagnosticsURIs = append([]string(nil), uris...)
	return nil, nil
}

type recordingScopedResolver struct {
	manager        Manager
	current        []ScopedManager
	currentByScope map[string][]ScopedManager
	lastScope      ToolScope
	currentScope   ToolScope
}

func (r *recordingScopedResolver) ForToolScope(scope ToolScope) (ScopedManager, error) {
	r.lastScope = scope
	return ScopedManager{
		Manager: r.manager,
		ResolvedScope: ResolvedToolScope{
			ToolScope:    scope,
			ScopeKey:     "scope-key",
			WorkspaceKey: "workspace-key",
			ShardKey:     "shard-key",
			ManagerKey:   "manager-key",
		},
	}, nil
}

func (r *recordingScopedResolver) CurrentManagersForToolScope(scope ToolScope) ([]ScopedManager, error) {
	r.currentScope = scope
	if r.currentByScope != nil {
		return r.currentByScope[recordingScopeIdentity(scope.AgentID, scope.ThreadID)], nil
	}
	return r.current, nil
}

func recordingScopeIdentity(agentID, threadID string) string {
	return agentID + "\x00" + threadID
}
