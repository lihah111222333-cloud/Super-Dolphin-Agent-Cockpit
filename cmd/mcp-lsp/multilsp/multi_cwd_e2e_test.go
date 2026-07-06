package multilsp

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
)

// ────────────────────────────────────────────────────────────────────
// Scenario 1: Single CWD, multiple worktrees (go.work with N modules)
// Validates that relative paths resolve to the correct module subtree,
// not to a wrong module or the global workspace root.
// ────────────────────────────────────────────────────────────────────

func TestMultiWorktree_RelativePathResolvesToCorrectModule(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	setupWorktreeProject(t, root, []string{"svcA", "svcB"})
	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctx := ctxWithCWD(root, "agent-wt", "thread-1")
	fileA := filepath.Join(root, "svcA", "main.go")
	fileB := filepath.Join(root, "svcB", "main.go")

	if err := mgr.BootstrapDocument(ctx, fileA); err != nil {
		t.Fatalf("BootstrapDocument(svcA): %v", err)
	}
	if err := mgr.BootstrapDocument(ctx, fileB); err != nil {
		t.Fatalf("BootstrapDocument(svcB): %v", err)
	}

	// relative path "svcA/main.go" must resolve under root, not under svcB
	ref, err := mgr.resolveDocumentRef(ctx, "svcA/main.go", "go")
	if err != nil {
		t.Fatalf("resolveDocumentRef(relative svcA): %v", err)
	}
	if ref.absPath != fileA {
		t.Fatalf("relative path resolved to %q, want %q", ref.absPath, fileA)
	}
}

func TestMultiWorktree_AllLSPMethodsRouteToCorrectModule(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	setupWorktreeProject(t, root, []string{"alpha", "beta"})
	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctx := ctxWithCWD(root, "agent-wt-all", "thread-1")
	fileAlpha := filepath.Join(root, "alpha", "main.go")
	fileBeta := filepath.Join(root, "beta", "main.go")

	// Bootstrap both
	for _, f := range []string{fileAlpha, fileBeta} {
		if err := mgr.BootstrapDocument(ctx, f); err != nil {
			t.Fatalf("BootstrapDocument(%s): %v", f, err)
		}
	}

	uriAlpha := fileURIFromPath(fileAlpha)
	uriBeta := fileURIFromPath(fileBeta)
	pos := protocol.Position{Line: 2, Character: 5}
	rng := protocol.Range{Start: pos, End: pos}

	// Exercise ALL LSP methods on alpha
	runAllLSPMethods(t, ctx, mgr, uriAlpha, pos, rng)
	// Exercise ALL LSP methods on beta
	runAllLSPMethods(t, ctx, mgr, uriBeta, pos, rng)
}

// ────────────────────────────────────────────────────────────────────
// Scenario 2: Multiple CWDs (completely different project roots)
// Validates tenant isolation: Agent A on projectX and Agent B on
// projectY must never cross-pollinate diagnostics or LSP clients.
// ────────────────────────────────────────────────────────────────────

func TestMultiCWD_IsolatedManagersPerProject(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, rootX, "projectX")
	setupStandaloneGoProject(t, rootY, "projectY")

	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctxX := ctxWithCWD(rootX, "agent-X", "thread-X")
	ctxY := ctxWithCWD(rootY, "agent-Y", "thread-Y")
	fileX := filepath.Join(rootX, "main.go")
	fileY := filepath.Join(rootY, "main.go")

	// Bootstrap files under different CWDs
	if err := mgr.BootstrapDocument(ctxX, fileX); err != nil {
		t.Fatalf("BootstrapDocument(X): %v", err)
	}
	if err := mgr.BootstrapDocument(ctxY, fileY); err != nil {
		t.Fatalf("BootstrapDocument(Y): %v", err)
	}

	// Pool must have created separate scoped managers
	scopedX, err := mgr.pool.ForScope(testLSPToolScope(rootX, "agent-X", "thread-X"))
	if err != nil {
		t.Fatalf("ForScope(X): %v", err)
	}
	scopedY, err := mgr.pool.ForScope(testLSPToolScope(rootY, "agent-Y", "thread-Y"))
	if err != nil {
		t.Fatalf("ForScope(Y): %v", err)
	}
	if scopedX.Manager == scopedY.Manager {
		t.Fatalf("different CWD projects share the same manager instance")
	}
	if scopedX.ResolvedScope.WorkspaceKey == scopedY.ResolvedScope.WorkspaceKey {
		t.Fatalf("different CWD projects share WorkspaceKey")
	}
}

func TestMultiCWD_DiagnosticsDoNotLeak(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, rootX, "projX")
	setupStandaloneGoProject(t, rootY, "projY")

	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctxX := ctxWithCWD(rootX, "agX", "thX")
	ctxY := ctxWithCWD(rootY, "agY", "thY")
	fileX := filepath.Join(rootX, "main.go")
	fileY := filepath.Join(rootY, "main.go")
	uriX := fileURIFromPath(fileX)
	uriY := fileURIFromPath(fileY)

	if err := mgr.BootstrapDocument(ctxX, fileX); err != nil {
		t.Fatalf("BootstrapDocument(X): %v", err)
	}
	if err := mgr.BootstrapDocument(ctxY, fileY); err != nil {
		t.Fatalf("BootstrapDocument(Y): %v", err)
	}

	// Inject diagnostics for X
	gen := mgr.CurrentDiagnosticGeneration()
	scopeX := buildTestResolvedScope(t, rootX, "agX", "thX", "go")
	scopeY := buildTestResolvedScope(t, rootY, "agY", "thY", "go")

	mgr.diagMu.Lock()
	mgr.diagnostics[diagnosticStoreKeyFor(scopeX, uriX).String()] = diagnosticSnapshot{
		scopeKey: scopeX.ScopeKey, workspaceKey: scopeX.WorkspaceKey,
		language: "go", uri: uriX, generation: gen,
		state: diagnosticStateReady,
		params: protocol.PublishDiagnosticsParams{
			URI:         uriX,
			Diagnostics: []protocol.Diagnostic{{Message: "X error"}},
		},
	}
	mgr.diagnostics[diagnosticStoreKeyFor(scopeY, uriY).String()] = diagnosticSnapshot{
		scopeKey: scopeY.ScopeKey, workspaceKey: scopeY.WorkspaceKey,
		language: "go", uri: uriY, generation: gen,
		state: diagnosticStateReady,
		params: protocol.PublishDiagnosticsParams{
			URI:         uriY,
			Diagnostics: []protocol.Diagnostic{{Message: "Y error"}},
		},
	}
	mgr.diagMu.Unlock()

	// Agent X must only see X diagnostics
	gotX, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctxX, scopeX), nil)
	if err != nil {
		t.Fatalf("Diagnostics(X): %v", err)
	}
	for _, d := range gotX {
		if d.URI == uriY {
			t.Fatalf("Agent X received Agent Y's diagnostics")
		}
	}

	// Agent Y must only see Y diagnostics
	gotY, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctxY, scopeY), nil)
	if err != nil {
		t.Fatalf("Diagnostics(Y): %v", err)
	}
	for _, d := range gotY {
		if d.URI == uriX {
			t.Fatalf("Agent Y received Agent X's diagnostics")
		}
	}
}

func TestMultiCWD_AllLSPMethodsOnBothProjects(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, rootX, "projX")
	setupStandaloneGoProject(t, rootY, "projY")

	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctxX := ctxWithCWD(rootX, "agentX", "threadX")
	ctxY := ctxWithCWD(rootY, "agentY", "threadY")
	fileX := filepath.Join(rootX, "main.go")
	fileY := filepath.Join(rootY, "main.go")

	for _, f := range []string{fileX, fileY} {
		ctx := ctxX
		if f == fileY {
			ctx = ctxY
		}
		if err := mgr.BootstrapDocument(ctx, f); err != nil {
			t.Fatalf("BootstrapDocument(%s): %v", f, err)
		}
	}

	pos := protocol.Position{Line: 2, Character: 5}
	rng := protocol.Range{Start: pos, End: pos}

	runAllLSPMethods(t, ctxX, mgr, fileURIFromPath(fileX), pos, rng)
	runAllLSPMethods(t, ctxY, mgr, fileURIFromPath(fileY), pos, rng)
}

// ────────────────────────────────────────────────────────────────────
// Scenario 3: Concurrent multi-CWD stress
// Multiple goroutines hammer different CWD projects simultaneously.
// ────────────────────────────────────────────────────────────────────

func TestMultiCWD_ConcurrentAllLSPMethods(t *testing.T) {
	roots := make([]string, 4)
	for i := range roots {
		roots[i] = canonicalScopePath(t.TempDir(), "")
		setupStandaloneGoProject(t, roots[i], "concurrent"+string(rune('A'+i)))
	}

	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: roots[0], ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	// Bootstrap all
	for i, root := range roots {
		ctx := ctxWithCWD(root, "agent-c"+string(rune('A'+i)), "thread-c")
		f := filepath.Join(root, "main.go")
		if err := mgr.BootstrapDocument(ctx, f); err != nil {
			t.Fatalf("BootstrapDocument(%d): %v", i, err)
		}
	}

	pos := protocol.Position{Line: 2, Character: 5}
	rng := protocol.Range{Start: pos, End: pos}
	errCh := make(chan string, len(roots)*30)

	var wg sync.WaitGroup
	for i, root := range roots {
		idx, r := i, root
		wg.Go(func() {
			ctx := ctxWithCWD(r, "agent-c"+string(rune('A'+idx)), "thread-c")
			uri := fileURIFromPath(filepath.Join(r, "main.go"))
			collectAllLSPMethodErrors(ctx, mgr, uri, pos, rng, errCh)
		})
	}
	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Error(msg)
	}
}

func TestMultiCWD_StrictContextEnforcement_MissingCWD(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, root, "strict")

	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	// Context without CWD must fail strict enforcement
	var missing context.Context
	_, err := mgr.effectiveWorkspaceRoot(missing)
	if err == nil {
		t.Fatal("nil context should fail strict enforcement")
	}
}

func TestMultiCWD_WorkspaceSymbolUsesCorrectCWD(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, rootX, "symX")
	setupStandaloneGoProject(t, rootY, "symY")

	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctxX := ctxWithCWD(rootX, "agX", "thX")
	ctxY := ctxWithCWD(rootY, "agY", "thY")

	// WorkspaceSymbol for X
	_, _ = mgr.WorkspaceSymbol(ctxX, "Main", "go")
	// WorkspaceSymbol for Y
	_, _ = mgr.WorkspaceSymbol(ctxY, "Main", "go")

	// Factory must have created clients bound to different roots
	if factory.clientCount() < 2 {
		t.Fatalf("expected ≥2 clients for different CWDs, got %d", factory.clientCount())
	}
}

func TestMultiCWD_PoolReleaseScopeIsolation(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, rootX, "relX")
	setupStandaloneGoProject(t, rootY, "relY")

	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	// Route through ForScope to register agents in the pool clone map
	// (ReleaseScope operates on pool clones, not bootstrap-only managers).
	scopedX, err := mgr.pool.ForScope(testLSPToolScope(rootX, "agRelX", "thRelX"))
	if err != nil {
		t.Fatalf("ForScope(X): %v", err)
	}
	scopedY, err := mgr.pool.ForScope(testLSPToolScope(rootY, "agRelY", "thRelY"))
	if err != nil {
		t.Fatalf("ForScope(Y): %v", err)
	}
	if scopedX.Manager == scopedY.Manager {
		t.Fatal("agents on different roots share manager before release")
	}

	// Release agent X scope
	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentAllThreads,
		AgentID:   "agRelX",
		Drain:     true,
		Reason:    "test/release",
	})
	if err != nil {
		t.Fatalf("ReleaseScope(X): %v", err)
	}
	if result.MatchedManagers == 0 {
		t.Fatal("ReleaseScope(X) matched 0 managers")
	}

	// Agent Y must still work after X is released
	scopedYAfter, err := mgr.pool.ForScope(testLSPToolScope(rootY, "agRelY", "thRelY"))
	if err != nil {
		t.Fatalf("ForScope(Y) after X release: %v", err)
	}
	if scopedYAfter.Manager == nil {
		t.Fatal("Agent Y manager is nil after Agent X release")
	}
}

// runDiagnosticsMethods exercises Diagnostics interface.
func runDiagnosticsMethods(t *testing.T, ctx context.Context, mgr *manager, uri string) {
	t.Helper()
	if _, err := mgr.Diagnostics(ctx, []string{uri}); err != nil {
		t.Errorf("Diagnostics(%s): %v", uri, err)
	}
	if err := mgr.WaitDiagnosticsStable(ctx, []string{uri}); err != nil {
		t.Errorf("WaitDiagnosticsStable(%s): %v", uri, err)
	}
	_ = mgr.CurrentDiagnosticGeneration()
}

func runNavigationMethods(t *testing.T, ctx context.Context, mgr *manager, uri string, pos protocol.Position) {
	t.Helper()
	if _, err := mgr.Definition(ctx, uri, pos); err != nil {
		t.Errorf("Definition(%s): %v", uri, err)
	}
	if _, err := mgr.Implementation(ctx, uri, pos); err != nil {
		t.Errorf("Implementation(%s): %v", uri, err)
	}
	if _, err := mgr.TypeDefinition(ctx, uri, pos); err != nil {
		t.Errorf("TypeDefinition(%s): %v", uri, err)
	}
	if _, err := mgr.Hover(ctx, uri, pos); err != nil {
		t.Errorf("Hover(%s): %v", uri, err)
	}
	if _, err := mgr.SignatureHelp(ctx, uri, pos); err != nil {
		t.Errorf("SignatureHelp(%s): %v", uri, err)
	}
}

func runXRefMethods(t *testing.T, ctx context.Context, mgr *manager, uri string, pos protocol.Position) {
	t.Helper()
	if _, err := mgr.References(ctx, uri, pos, true); err != nil {
		t.Errorf("References(%s): %v", uri, err)
	}
	if _, err := mgr.CallHierarchy(ctx, uri, pos, "both"); err != nil {
		t.Errorf("CallHierarchy(%s): %v", uri, err)
	}
	if _, err := mgr.TypeHierarchy(ctx, uri, pos, ""); err != nil {
		t.Errorf("TypeHierarchy(%s): %v", uri, err)
	}
}

func runStructureAndCompletionMethods(t *testing.T, ctx context.Context, mgr *manager, uri string, pos protocol.Position) {
	t.Helper()
	if _, err := mgr.DocumentSymbol(ctx, uri); err != nil {
		t.Errorf("DocumentSymbol(%s): %v", uri, err)
	}
	if _, err := mgr.WorkspaceSymbol(ctx, "Main", "go"); err != nil {
		t.Errorf("WorkspaceSymbol(%s): %v", uri, err)
	}
	if _, err := mgr.FoldingRange(ctx, uri); err != nil {
		t.Errorf("FoldingRange(%s): %v", uri, err)
	}
	if _, err := mgr.SemanticTokens(ctx, uri); err != nil {
		t.Errorf("SemanticTokens(%s): %v", uri, err)
	}
	if _, err := mgr.Completion(ctx, uri, pos); err != nil {
		t.Errorf("Completion(%s): %v", uri, err)
	}
}

func runEditMethods(t *testing.T, ctx context.Context, mgr *manager, uri string, pos protocol.Position, rng protocol.Range) {
	t.Helper()
	if _, err := mgr.Rename(ctx, uri, pos, "newName"); err != nil {
		t.Errorf("Rename(%s): %v", uri, err)
	}
	if _, err := mgr.CodeAction(ctx, uri, rng, nil); err != nil {
		t.Errorf("CodeAction(%s): %v", uri, err)
	}
	if _, err := mgr.Format(ctx, uri, protocol.FormattingOptions{}); err != nil {
		t.Errorf("Format(%s): %v", uri, err)
	}
}

func runOpenChangeMethods(t *testing.T, ctx context.Context, mgr *manager, uri string) {
	t.Helper()
	if err := mgr.DidOpen(ctx, uri, "go", 1, "package main\n"); err != nil {
		t.Errorf("DidOpen(%s): %v", uri, err)
	}
	if err := mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{
		{Text: "package main\n// changed\n"},
	}); err != nil {
		t.Errorf("DidChange(%s): %v", uri, err)
	}
}

func runCloseMethod(t *testing.T, ctx context.Context, mgr *manager, uri string) {
	t.Helper()
	if err := mgr.DidClose(ctx, uri); err != nil {
		t.Errorf("DidClose(%s): %v", uri, err)
	}
}

// runAllLSPMethods exercises every Manager interface method on a URI.
func runAllLSPMethods(t *testing.T, ctx context.Context, mgr *manager, uri string, pos protocol.Position, rng protocol.Range) {
	t.Helper()
	runNavigationMethods(t, ctx, mgr, uri, pos)
	runXRefMethods(t, ctx, mgr, uri, pos)
	runStructureAndCompletionMethods(t, ctx, mgr, uri, pos)
	runEditMethods(t, ctx, mgr, uri, pos, rng)
	runOpenChangeMethods(t, ctx, mgr, uri)
	runDiagnosticsMethods(t, ctx, mgr, uri)
	runCloseMethod(t, ctx, mgr, uri)
}

// collectAllLSPMethodErrors is the goroutine-safe variant of runAllLSPMethods.
// Instead of calling t.Errorf (unsafe from goroutines), it sends error messages
// to a channel for the caller to report on the main test goroutine.
func collectAllLSPMethodErrors(ctx context.Context, mgr *manager, uri string, pos protocol.Position, rng protocol.Range, errCh chan<- string) {
	check := func(label string, err error) {
		if err != nil {
			errCh <- fmt.Sprintf("%s(%s): %v", label, uri, err)
		}
	}
	_, err := mgr.Definition(ctx, uri, pos)
	check("Definition", err)
	_, err = mgr.Implementation(ctx, uri, pos)
	check("Implementation", err)
	_, err = mgr.TypeDefinition(ctx, uri, pos)
	check("TypeDefinition", err)
	_, err = mgr.Hover(ctx, uri, pos)
	check("Hover", err)
	_, err = mgr.SignatureHelp(ctx, uri, pos)
	check("SignatureHelp", err)
	_, err = mgr.References(ctx, uri, pos, true)
	check("References", err)
	_, err = mgr.CallHierarchy(ctx, uri, pos, "both")
	check("CallHierarchy", err)
	_, err = mgr.TypeHierarchy(ctx, uri, pos, "")
	check("TypeHierarchy", err)
	_, err = mgr.DocumentSymbol(ctx, uri)
	check("DocumentSymbol", err)
	_, err = mgr.WorkspaceSymbol(ctx, "Main", "go")
	check("WorkspaceSymbol", err)
	_, err = mgr.FoldingRange(ctx, uri)
	check("FoldingRange", err)
	_, err = mgr.SemanticTokens(ctx, uri)
	check("SemanticTokens", err)
	_, err = mgr.Completion(ctx, uri, pos)
	check("Completion", err)
	_, err = mgr.Rename(ctx, uri, pos, "newName")
	check("Rename", err)
	_, err = mgr.CodeAction(ctx, uri, rng, nil)
	check("CodeAction", err)
	_, err = mgr.Format(ctx, uri, protocol.FormattingOptions{})
	check("Format", err)
	check("DidOpen", mgr.DidOpen(ctx, uri, "go", 1, "package main\n"))
	check("DidChange", mgr.DidChange(ctx, uri, 2, []protocol.TextDocumentContentChangeEvent{
		{Text: "package main\n// changed\n"},
	}))
	_, err = mgr.Diagnostics(ctx, []string{uri})
	check("Diagnostics", err)
	check("WaitDiagnosticsStable", mgr.WaitDiagnosticsStable(ctx, []string{uri}))
	check("DidClose", mgr.DidClose(ctx, uri))
}

func buildTestResolvedScope(t *testing.T, root, agentID, threadID, lang string) ResolvedLSPToolScope {
	t.Helper()
	scope := LSPToolScope{
		AgentID:               agentID,
		ThreadID:              threadID,
		LanguageID:            lang,
		WorkspaceRoot:         root,
		LanguageWorkspaceRoot: root,
		ProjectRoot:           root,
		RootKind:              "go_mod",
		LanguageSpecific:      map[string]string{"moduleRoot": root},
	}
	resolved, err := ResolveLSPToolScope(scope)
	if err != nil {
		t.Fatalf("ResolveLSPToolScope: %v", err)
	}
	return resolved
}
