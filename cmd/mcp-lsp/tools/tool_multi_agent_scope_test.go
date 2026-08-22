package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestTwoAgentsSameRepoNoDiagnosticLeak(t *testing.T) {
	repo := writeScopedToolRepo(t)
	evilRoot := t.TempDir()
	resolver := newMultiAgentToolResolver()
	managerA := newMultiAgentToolManager("agent-a", fileURI(filepath.Join(repo, "main.go")))
	managerB := newMultiAgentToolManager("agent-b", fileURI(filepath.Join(repo, "main.go")))
	resolver.setManager("agent-a", "thread-1", repo, managerA)
	resolver.setManager("agent-b", "thread-1", repo, managerB)
	registry := lspmanager.NewRegistry(nil)
	registry.Register("go", newMultiAgentToolManager("singleton", ""), resolver)
	t.Cleanup(func() { _ = registry.Close() })
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: evilRoot, Registry: registry})

	runScopedDiagnostics(t, handler, repo, "agent-a", map[string]any{
		"file_path": "main.go",
	})
	assertToolManagerDiagnostics(t, managerA, 1, "agent-a", repo, evilRoot)
	assertToolManagerDiagnostics(t, managerB, 0, "", "", "")
	assertToolManagerReopenCalls(t, managerA, 1)
	assertToolManagerReopenCalls(t, managerB, 0)

	runScopedDiagnostics(t, handler, repo, "agent-b", map[string]any{
		"file_path": "main.go",
	})
	assertToolManagerDiagnostics(t, managerA, 1, "agent-a", repo, evilRoot)
	assertToolManagerDiagnostics(t, managerB, 1, "agent-b", repo, evilRoot)
	assertToolManagerReopenCalls(t, managerA, 1)
	assertToolManagerReopenCalls(t, managerB, 1)
	if managerA.snapshot().lastDiagnosticsScope.ManagerKey == managerB.snapshot().lastDiagnosticsScope.ManagerKey {
		t.Fatalf("two agents in one repo shared ManagerKey %q", managerA.snapshot().lastDiagnosticsScope.ManagerKey)
	}
}

func TestAgentStopCleansScopeWithoutKillingOtherAgent(t *testing.T) {
	repo := writeScopedToolRepo(t)
	resolver := newMultiAgentToolResolver()
	managerA := newMultiAgentToolManager("agent-a", fileURI(filepath.Join(repo, "main.go")))
	managerB := newMultiAgentToolManager("agent-b", fileURI(filepath.Join(repo, "main.go")))
	resolver.setCurrent("agent-a", "thread-1", repo, managerA)
	resolver.setCurrent("agent-b", "thread-1", repo, managerB)
	resolver.setManager("agent-b", "thread-1", repo, managerB)
	registry := lspmanager.NewRegistry(nil)
	registry.Register("go", newMultiAgentToolManager("singleton", ""), resolver)
	t.Cleanup(func() { _ = registry.Close() })
	handler := NewDiagnosticsHandler(Config{WorkspaceRoot: "/untrusted/root", Registry: registry})

	resolver.clearCurrent("agent-a", "thread-1")
	if err := runScopedDiagnosticsError(t, handler, repo, "agent-a", map[string]any{"file_path": "main.go"}); err == nil || !strings.Contains(err.Error(), "missing scoped manager") {
		t.Fatalf("stopped agent diagnostics error = %v, want missing scoped manager", err)
	}
	assertToolManagerDiagnostics(t, managerA, 0, "", "", "")
	assertToolManagerReopenCalls(t, managerA, 0)

	runScopedDiagnostics(t, handler, repo, "agent-b", map[string]any{"file_path": "main.go"})
	assertToolManagerDiagnostics(t, managerB, 1, "agent-b", repo, "")
	assertToolManagerReopenCalls(t, managerB, 1)
	if got := managerB.snapshot().closeCalls; got != 0 {
		t.Fatalf("other agent manager was closed during stopped-scope cleanup: closeCalls=%d", got)
	}
}

type multiAgentToolResolver struct {
	mu       sync.Mutex
	managers map[string]*multiAgentToolManager
	current  map[string][]lspmanager.ScopedManager
}

func newMultiAgentToolResolver() *multiAgentToolResolver {
	return &multiAgentToolResolver{
		managers: make(map[string]*multiAgentToolManager),
		current:  make(map[string][]lspmanager.ScopedManager),
	}
}

func (r *multiAgentToolResolver) setManager(agentID, threadID, cwd string, mgr *multiAgentToolManager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managers[toolResolverKey(agentID, threadID, cwd)] = mgr
}

func (r *multiAgentToolResolver) setCurrent(agentID, threadID, cwd string, mgr *multiAgentToolManager) {
	scope := lspmanager.ToolScope{AgentID: agentID, ThreadID: threadID, CWD: cwd, Family: "lsp", LanguageID: "go"}
	scoped := lspmanager.ScopedManager{Manager: mgr, ResolvedScope: resolvedToolTestScope(scope)}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current[toolCurrentKey(agentID, threadID)] = []lspmanager.ScopedManager{scoped}
}

func (r *multiAgentToolResolver) clearCurrent(agentID, threadID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.current, toolCurrentKey(agentID, threadID))
}

func (r *multiAgentToolResolver) ForToolScope(scope lspmanager.ToolScope) (lspmanager.ScopedManager, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mgr := r.managers[toolResolverKey(scope.AgentID, scope.ThreadID, scope.CWD)]
	if mgr == nil {
		return lspmanager.ScopedManager{}, fmt.Errorf("missing scoped manager for %s", toolResolverKey(scope.AgentID, scope.ThreadID, scope.CWD))
	}
	return lspmanager.ScopedManager{Manager: mgr, ResolvedScope: resolvedToolTestScope(scope)}, nil
}

func (r *multiAgentToolResolver) CurrentManagersForToolScope(scope lspmanager.ToolScope) ([]lspmanager.ScopedManager, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := r.current[toolCurrentKey(scope.AgentID, scope.ThreadID)]
	return append([]lspmanager.ScopedManager(nil), items...), nil
}

func toolResolverKey(agentID, threadID, cwd string) string {
	return strings.Join([]string{agentID, threadID, filepath.Clean(cwd)}, "\x00")
}

func toolCurrentKey(agentID, threadID string) string {
	return strings.Join([]string{agentID, threadID}, "\x00")
}

func resolvedToolTestScope(scope lspmanager.ToolScope) lspmanager.ResolvedToolScope {
	scope.Family = strings.TrimSpace(scope.Family)
	if scope.Family == "" {
		scope.Family = "lsp"
	}
	scope.CWD = filepath.Clean(scope.CWD)
	scope.WorkspaceRoot = scope.CWD
	scope.LanguageWorkspaceRoot = scope.CWD
	scope.ProjectRoot = scope.CWD
	scope.RootKind = "test"
	scopeKey := strings.Join([]string{scope.Family, scope.AgentID, scope.ThreadID}, "\x00")
	workspaceKey := strings.Join([]string{scope.LanguageID, scope.RootKind, scope.CWD}, "\x00")
	return lspmanager.ResolvedToolScope{
		ToolScope:    scope,
		ScopeKey:     scopeKey,
		WorkspaceKey: workspaceKey,
		ShardKey:     scopeKey,
		ManagerKey:   strings.Join([]string{scopeKey, workspaceKey}, "\x00"),
	}
}

type multiAgentToolManager struct {
	testManagerNavigationNoop
	testManagerSymbolNoop
	testManagerEditNoop

	mu                   sync.Mutex
	name                 string
	defaultURI           string
	diagnosticsCalls     int
	reopenCalls          int
	waitCalls            int
	bootstrapCalls       int
	closeCalls           int
	lastDiagnosticsURI   []string
	lastDiagnosticsOK    bool
	lastDiagnosticsScope lspmanager.ResolvedToolScope
	lastReopenScope      lspmanager.ResolvedToolScope
}

type multiAgentToolManagerSnapshot struct {
	diagnosticsCalls     int
	reopenCalls          int
	waitCalls            int
	bootstrapCalls       int
	closeCalls           int
	lastDiagnosticsURIs  []string
	lastDiagnosticsOK    bool
	lastDiagnosticsScope lspmanager.ResolvedToolScope
	lastReopenScope      lspmanager.ResolvedToolScope
}

func newMultiAgentToolManager(name, defaultURI string) *multiAgentToolManager {
	return &multiAgentToolManager{name: name, defaultURI: defaultURI}
}

func (m *multiAgentToolManager) snapshot() multiAgentToolManagerSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return multiAgentToolManagerSnapshot{
		diagnosticsCalls:     m.diagnosticsCalls,
		reopenCalls:          m.reopenCalls,
		waitCalls:            m.waitCalls,
		bootstrapCalls:       m.bootstrapCalls,
		closeCalls:           m.closeCalls,
		lastDiagnosticsURIs:  append([]string(nil), m.lastDiagnosticsURI...),
		lastDiagnosticsOK:    m.lastDiagnosticsOK,
		lastDiagnosticsScope: m.lastDiagnosticsScope,
		lastReopenScope:      m.lastReopenScope,
	}
}

func (m *multiAgentToolManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeCalls++
	return nil
}

func (m *multiAgentToolManager) BootstrapDocument(context.Context, string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bootstrapCalls++
	return nil
}

func (m *multiAgentToolManager) ReopenDocumentForDiagnostics(ctx context.Context, _ string) error {
	scope, _ := lspmanager.ResolvedToolScopeFromContext(ctx)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reopenCalls++
	m.lastReopenScope = scope
	return nil
}

func (m *multiAgentToolManager) Diagnostics(ctx context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	m.mu.Lock()
	m.diagnosticsCalls++
	m.lastDiagnosticsURI = append([]string(nil), uris...)
	m.lastDiagnosticsScope, m.lastDiagnosticsOK = lspmanager.ResolvedToolScopeFromContext(ctx)
	name := m.name
	defaultURI := m.defaultURI
	m.mu.Unlock()
	targets := append([]string(nil), uris...)
	if len(targets) == 0 && defaultURI != "" {
		targets = []string{defaultURI}
	}
	items := make([]protocol.PublishDiagnosticsParams, 0, len(targets))
	for _, uri := range targets {
		items = append(items, protocol.PublishDiagnosticsParams{
			URI: uri,
			Diagnostics: []protocol.Diagnostic{{
				Range:    protocol.Range{Start: protocol.Position{}, End: protocol.Position{Character: 1}},
				Severity: protocol.SeverityWarning,
				Source:   "multi-agent-test",
				Message:  name + "-only",
			}},
		})
	}
	return items, nil
}

func (m *multiAgentToolManager) WaitDiagnosticsStable(context.Context, []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.waitCalls++
	return nil
}

func (*multiAgentToolManager) CurrentDiagnosticGeneration() uint64 { return 1 }

func (*multiAgentToolManager) AdvanceDiagnosticGeneration() uint64 { return 2 }

func writeScopedToolRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/scoped\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	return root
}

func runScopedDiagnostics(t *testing.T, handler Handler, root, agentID string, payload map[string]any) any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID:  agentID,
		ThreadID: "thread-1",
		CallID:   "call-" + agentID,
		CWD:      root,
		Family:   "lsp",
	})
	result, err := handler(ctx, raw)
	if err != nil {
		t.Fatalf("diagnostics for %s: %v", agentID, err)
	}
	return result
}

func runScopedDiagnosticsError(t *testing.T, handler Handler, root, agentID string, payload map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	ctx := common.WithToolScope(context.Background(), common.ToolScope{
		AgentID: agentID, ThreadID: "thread-1", CallID: "call-" + agentID, CWD: root, Family: "lsp",
	})
	_, err = handler(ctx, raw)
	return err
}

func assertToolManagerDiagnostics(t *testing.T, mgr *multiAgentToolManager, wantCalls int, wantAgent, wantCWD, forgedCWD string) {
	t.Helper()
	snapshot := mgr.snapshot()
	if snapshot.diagnosticsCalls != wantCalls {
		t.Fatalf("%s diagnostics calls = %d, want %d", mgr.name, snapshot.diagnosticsCalls, wantCalls)
	}
	if wantCalls == 0 {
		return
	}
	if !snapshot.lastDiagnosticsOK {
		t.Fatalf("%s diagnostics context missing resolved scope", mgr.name)
	}
	scope := snapshot.lastDiagnosticsScope
	if scope.AgentID != wantAgent || scope.CWD != wantCWD {
		t.Fatalf("%s resolved scope = %#v, want agent=%q cwd=%q", mgr.name, scope, wantAgent, wantCWD)
	}
	if strings.Contains(scope.ManagerKey, "agent-forged") || (forgedCWD != "" && strings.Contains(scope.ManagerKey, forgedCWD)) {
		t.Fatalf("%s ManagerKey includes forged argument data: %q", mgr.name, scope.ManagerKey)
	}
}

func assertToolManagerReopenCalls(t *testing.T, mgr *multiAgentToolManager, want int) {
	t.Helper()
	snapshot := mgr.snapshot()
	if snapshot.reopenCalls != want {
		t.Fatalf("%s diagnostics reopen calls = %d, want %d", mgr.name, snapshot.reopenCalls, want)
	}
	if want > 0 && snapshot.lastReopenScope.ManagerKey != snapshot.lastDiagnosticsScope.ManagerKey {
		t.Fatalf("%s reopen ManagerKey = %q, diagnostics ManagerKey = %q", mgr.name, snapshot.lastReopenScope.ManagerKey, snapshot.lastDiagnosticsScope.ManagerKey)
	}
}
