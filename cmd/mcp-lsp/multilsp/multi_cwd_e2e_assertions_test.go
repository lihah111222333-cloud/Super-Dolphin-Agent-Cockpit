package multilsp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

func TestMultiCWD_ManagerContractCoverage(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, root, "contract")
	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctx := ctxWithCWD(root, "agent-contract", "thread-contract")
	uri := fileURIFromPath(filepath.Join(root, "main.go"))
	pos := protocol.Position{Line: 2, Character: 5}
	rng := protocol.Range{Start: pos, End: pos}
	runAllLSPMethods(t, ctx, mgr, uri, pos, rng)
	if err := mgr.BootstrapDocumentOpenOnly(ctx, uri); err != nil {
		t.Fatalf("BootstrapDocumentOpenOnly(%s): %v", uri, err)
	}
	if _, err := mgr.EnsureClient(ctx, uri, "go"); err != nil {
		t.Fatalf("EnsureClient(file): %v", err)
	}
	if _, err := mgr.EnsureClient(ctx, "", "go"); err != nil {
		t.Fatalf("EnsureClient(language): %v", err)
	}
	if runner := mgr.BackgroundRunner(); runner == nil {
		t.Fatal("BackgroundRunner() = nil, want pool recycler runner")
	}

	assertE2ERequests(t, factory, map[string][]string{
		root: allRoutedMethodsForURI(uri),
	})
	assertE2ELifecycle(t, factory, map[string]string{root: uri})
}

func TestMultiWorktree_AllLSPMethodsRouteToExpectedModuleClients(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	setupWorktreeProject(t, root, []string{"alpha", "beta"})
	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctx := ctxWithCWD(root, "agent-wt-routed", "thread-1")
	fileAlpha := filepath.Join(root, "alpha", "main.go")
	fileBeta := filepath.Join(root, "beta", "main.go")
	for _, file := range []string{fileAlpha, fileBeta} {
		if err := mgr.BootstrapDocument(ctx, file); err != nil {
			t.Fatalf("BootstrapDocument(%s): %v", file, err)
		}
	}

	pos := protocol.Position{Line: 2, Character: 5}
	rng := protocol.Range{Start: pos, End: pos}
	uriAlpha := fileURIFromPath(fileAlpha)
	uriBeta := fileURIFromPath(fileBeta)
	runAllLSPMethods(t, ctx, mgr, uriAlpha, pos, rng)
	runAllLSPMethods(t, ctx, mgr, uriBeta, pos, rng)

	assertE2ERequests(t, factory, map[string][]string{
		root: append(allRoutedMethodsForURI(uriAlpha), allRoutedMethodsForURI(uriBeta)...),
	})
	assertE2ELifecycleMany(t, factory, map[string][]string{
		root: {uriAlpha, uriBeta},
	})
}

func TestMultiCWD_AllLSPMethodsRouteToExpectedProjectClients(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, rootX, "projX")
	setupStandaloneGoProject(t, rootY, "projY")
	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctxX := ctxWithCWD(rootX, "agent-routed-X", "thread-X")
	ctxY := ctxWithCWD(rootY, "agent-routed-Y", "thread-Y")
	fileX := filepath.Join(rootX, "main.go")
	fileY := filepath.Join(rootY, "main.go")
	for _, item := range []struct {
		ctx  context.Context
		file string
	}{{ctxX, fileX}, {ctxY, fileY}} {
		if err := mgr.BootstrapDocument(item.ctx, item.file); err != nil {
			t.Fatalf("BootstrapDocument(%s): %v", item.file, err)
		}
	}

	pos := protocol.Position{Line: 2, Character: 5}
	rng := protocol.Range{Start: pos, End: pos}
	uriX := fileURIFromPath(fileX)
	uriY := fileURIFromPath(fileY)
	runAllLSPMethods(t, ctxX, mgr, uriX, pos, rng)
	runAllLSPMethods(t, ctxY, mgr, uriY, pos, rng)

	assertE2ERequests(t, factory, map[string][]string{
		rootX: allRoutedMethodsForURI(uriX),
		rootY: allRoutedMethodsForURI(uriY),
	})
	assertE2ELifecycle(t, factory, map[string]string{
		rootX: uriX,
		rootY: uriY,
	})
}

func TestMultiCWD_MultipleWorkDirectoriesPerCWDRouteIndependently(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupWorktreeProject(t, rootX, []string{"api", "worker"})
	setupWorktreeProject(t, rootY, []string{"cli", "service"})
	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	ctxX := ctxWithCWD(rootX, "agent-matrix-X", "thread-X")
	ctxY := ctxWithCWD(rootY, "agent-matrix-Y", "thread-Y")
	targets := []struct {
		ctx    context.Context
		root   string
		module string
	}{
		{ctx: ctxX, root: rootX, module: "api"},
		{ctx: ctxX, root: rootX, module: "worker"},
		{ctx: ctxY, root: rootY, module: "cli"},
		{ctx: ctxY, root: rootY, module: "service"},
	}

	pos := protocol.Position{Line: 2, Character: 5}
	rng := protocol.Range{Start: pos, End: pos}
	wantRequests := map[string][]string{}
	wantLifecycle := map[string][]string{}
	for _, target := range targets {
		file := filepath.Join(target.root, target.module, "main.go")
		uri := fileURIFromPath(file)
		if err := mgr.BootstrapDocument(target.ctx, file); err != nil {
			t.Fatalf("BootstrapDocument(%s): %v", file, err)
		}
		runAllLSPMethods(t, target.ctx, mgr, uri, pos, rng)
		wantRequests[target.root] = append(wantRequests[target.root], allRoutedMethodsForURI(uri)...)
		wantLifecycle[target.root] = append(wantLifecycle[target.root], uri)
	}

	assertE2ERequests(t, factory, wantRequests)
	assertE2ELifecycleMany(t, factory, wantLifecycle)
}

func allRoutedMethodsForURI(uri string) []string {
	return []string{
		protocol.MethodDefinition + "\t" + uri,
		protocol.MethodImplementation + "\t" + uri,
		protocol.MethodTypeDefinition + "\t" + uri,
		protocol.MethodHover + "\t" + uri,
		protocol.MethodSignatureHelp + "\t" + uri,
		protocol.MethodReferences + "\t" + uri,
		protocol.MethodPrepareCallHierarchy + "\t" + uri,
		protocol.MethodCallHierarchyIncoming + "\t" + uri,
		protocol.MethodCallHierarchyOutgoing + "\t" + uri,
		protocol.MethodPrepareTypeHierarchy + "\t" + uri,
		protocol.MethodTypeHierarchySupertypes + "\t" + uri,
		protocol.MethodTypeHierarchySubtypes + "\t" + uri,
		protocol.MethodDocumentSymbol + "\t" + uri,
		protocol.MethodWorkspaceSymbol + "\t",
		protocol.MethodFoldingRange + "\t" + uri,
		protocol.MethodSemanticTokensFull + "\t" + uri,
		protocol.MethodCompletion + "\t" + uri,
		protocol.MethodRename + "\t" + uri,
		protocol.MethodCodeAction + "\t" + uri,
		protocol.MethodFormatting + "\t" + uri,
	}
}

func assertE2ERequests(t *testing.T, factory *e2eFactory, want map[string][]string) {
	t.Helper()
	byRoot := e2eRequestsByRoot(factory.snapshot())
	for root, wantRequests := range want {
		got := byRoot[root]
		if len(got) == 0 {
			t.Fatalf("root %s recorded no LSP requests; all roots=%v", root, sortedKeys(byRoot))
		}
		for _, wantRequest := range wantRequests {
			if !slices.Contains(got, wantRequest) {
				t.Fatalf("root %s missing request %s; got=%v", root, wantRequest, got)
			}
		}
	}
	for root, requests := range byRoot {
		for _, request := range requests {
			uri := requestURIFromRecordedKey(request)
			if uri == "" {
				continue
			}
			path, err := absolutePathFromURI(uri)
			if err != nil {
				t.Fatalf("request %s has invalid URI: %v", request, err)
			}
			if !pathWithinRoot(path, root) {
				t.Fatalf("request %s recorded under root %s, want URI under that root", request, root)
			}
		}
	}
}

func e2eRequestsByRoot(snapshots []e2eFactorySnapshot) map[string][]string {
	byRoot := map[string][]string{}
	for _, snapshot := range snapshots {
		for _, request := range snapshot.requests {
			byRoot[snapshot.rootDir] = append(byRoot[snapshot.rootDir], recordedRequestKey(request))
		}
	}
	for root := range byRoot {
		slices.Sort(byRoot[root])
	}
	return byRoot
}

func recordedRequestKey(request e2eRecordedRequest) string {
	return request.method + "\t" + requestURI(request.params)
}

func requestURI(params any) string {
	payload, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	var envelope struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Item struct {
			URI string `json:"uri"`
		} `json:"item"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	if envelope.TextDocument.URI != "" {
		return envelope.TextDocument.URI
	}
	return envelope.Item.URI
}

func requestURIFromRecordedKey(key string) string {
	_, uri, _ := strings.Cut(key, "\t")
	return uri
}

func assertE2ELifecycle(t *testing.T, factory *e2eFactory, want map[string]string) {
	t.Helper()
	many := make(map[string][]string, len(want))
	for root, uri := range want {
		many[root] = []string{uri}
	}
	assertE2ELifecycleMany(t, factory, many)
}

func assertE2ELifecycleMany(t *testing.T, factory *e2eFactory, want map[string][]string) {
	t.Helper()
	byRoot := map[string]e2eLifecycleSnapshot{}
	for _, snapshot := range factory.snapshot() {
		aggregated := byRoot[snapshot.rootDir]
		aggregated.opens = append(aggregated.opens, snapshot.opens...)
		aggregated.changes = append(aggregated.changes, snapshot.changes...)
		aggregated.closes = append(aggregated.closes, snapshot.closes...)
		byRoot[snapshot.rootDir] = aggregated
	}
	for root, uris := range want {
		snapshot, ok := byRoot[root]
		if !ok {
			t.Fatalf("root %s had no client snapshot; roots=%v", root, sortedKeys(byRoot))
		}
		for _, uri := range uris {
			if !hasOpenEvent(snapshot.opens, uri, "go") {
				t.Fatalf("root %s missing DidOpen(%s); opens=%v", root, uri, snapshot.opens)
			}
			if !slices.Contains(snapshot.changes, uri) {
				t.Fatalf("root %s missing DidChange(%s); changes=%v", root, uri, snapshot.changes)
			}
			if !slices.Contains(snapshot.closes, uri) {
				t.Fatalf("root %s missing DidClose(%s); closes=%v", root, uri, snapshot.closes)
			}
		}
	}
}

type e2eLifecycleSnapshot struct {
	opens   []genericOpenEvent
	changes []string
	closes  []string
}

func hasOpenEvent(events []genericOpenEvent, uri, languageID string) bool {
	return slices.ContainsFunc(events, func(event genericOpenEvent) bool {
		return event.uri == uri && event.language == languageID
	})
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestMultiCWD_WorkspaceSymbolUsesExpectedRoots(t *testing.T) {
	rootX := canonicalScopePath(t.TempDir(), "")
	rootY := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, rootX, "symStrictX")
	setupStandaloneGoProject(t, rootY, "symStrictY")
	factory := &e2eFactory{}
	mgr := NewManager(Config{WorkspaceRoot: rootX, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	if _, err := mgr.WorkspaceSymbol(ctxWithCWD(rootX, "agX", "thX"), "Main", "go"); err != nil {
		t.Fatalf("WorkspaceSymbol(X): %v", err)
	}
	if _, err := mgr.WorkspaceSymbol(ctxWithCWD(rootY, "agY", "thY"), "Main", "go"); err != nil {
		t.Fatalf("WorkspaceSymbol(Y): %v", err)
	}

	assertE2ERequests(t, factory, map[string][]string{
		rootX: {protocol.MethodWorkspaceSymbol + "\t"},
		rootY: {protocol.MethodWorkspaceSymbol + "\t"},
	})
}

func TestMultiCWD_StrictContextEnforcement_MissingToolScopeCWD(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	setupStandaloneGoProject(t, root, "strict-background")
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: &e2eFactory{}}).(*manager)
	defer func() { _ = mgr.Close() }()

	_, err := mgr.effectiveWorkspaceRoot(context.Background())
	if err == nil {
		t.Fatal("context without tool scope CWD should fail strict enforcement")
	}
	if !strings.Contains(fmt.Sprint(err), "missing") {
		t.Fatalf("strict enforcement error = %v, want missing-CWD context", err)
	}
}
