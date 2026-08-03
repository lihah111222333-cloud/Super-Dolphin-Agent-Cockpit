package multilsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestGoLanguageAdapterEnvPolicyOverridesForeignBuildContext(t *testing.T) {
	t.Setenv("GOOS", "aix")
	t.Setenv("GOARCH", "ppc64")

	env := envMap(goLanguageAdapter{}.EnvPolicy(ResolvedLanguageScope{}))
	if got := env["GOOS"]; got != runtime.GOOS {
		t.Fatalf("GOOS = %q, want host %q", got, runtime.GOOS)
	}
	if got := env["GOARCH"]; got != runtime.GOARCH {
		t.Fatalf("GOARCH = %q, want host %q", got, runtime.GOARCH)
	}
}

func TestGoLanguageAdapterBoundsSharedDaemonMemory(t *testing.T) {
	t.Setenv(lspGoplsHeapLimitEnv, "3072")
	env := envMap(goLanguageAdapter{}.EnvPolicy(ResolvedLanguageScope{}))
	if got := env["GOMEMLIMIT"]; got != "3072MiB" {
		t.Fatalf("GOMEMLIMIT = %q, want independent shared gopls daemon heap limit", got)
	}
}

func TestGoplsRemoteListenTimeoutArgValidatesConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		idle       time.Duration
		goos       string
		want       []string
		wantErrSub string
	}{
		{name: "default", idle: 15 * time.Minute, goos: "darwin", want: []string{goplsRemoteAutoArg, "-remote.listen.timeout=15m0s"}},
		{name: "custom", idle: 2500 * time.Millisecond, goos: "linux", want: []string{goplsRemoteAutoArg, "-remote.listen.timeout=2.5s"}},
		{name: "windows", idle: 15 * time.Minute, goos: "windows", want: nil},
		{name: "zero", idle: 0, goos: "darwin", wantErrSub: "positive"},
		{name: "negative", idle: -time.Second, goos: "darwin", wantErrSub: "positive"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := goplsServerArgs(test.idle, test.goos)
			if test.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrSub) {
					t.Fatalf("goplsServerArgs() error = %v, want substring %q", err, test.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("goplsServerArgs() error = %v", err)
			}
			if !slices.Equal(got, test.want) {
				t.Fatalf("goplsServerArgs() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func envMap(entries []string) map[string]string {
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}

func hostGoEnv(extra ...string) []string {
	return append([]string{
		"GOOS=" + runtime.GOOS,
		"GOARCH=" + runtime.GOARCH,
		"GOMEMLIMIT=3584MiB",
	}, extra...)
}

func TestGenericLanguageServicesMatrixCoversDefaultLSPClients(t *testing.T) {
	registry := NewDefaultLanguageAdapterRegistry()
	want := []string{
		"c",
		"cpp",
		"css",
		"csharp",
		"dart",
		"dockerfile",
		"go",
		"gomod",
		"gosum",
		"gowork",
		"graphql",
		"html",
		"java",
		"javascript",
		"javascriptreact",
		"json",
		"kotlin",
		"lua",
		"markdown",
		"objective-c",
		"objective-cpp",
		"php",
		"prisma",
		"python",
		"ruby",
		"rust",
		"shellscript",
		"sql",
		"svelte",
		"swift",
		"terraform",
		"typescript",
		"typescriptreact",
		"vue",
		"yaml",
	}
	slices.Sort(want)
	got := defaultLSPClientLanguageIDs(t)
	if !slices.Equal(got, want) {
		t.Fatalf("default LSP client languages = %#v, want %#v", got, want)
	}
	for _, languageID := range want {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("default adapter registry missing %q", languageID)
		}
		if !adapter.CapabilityPolicy().RequiresLSPClient {
			t.Fatalf("%s adapter should require an LSP client", languageID)
		}
	}
}

func TestGenericLanguageServicesDefaultDocumentLanguagesUseLSPClients(t *testing.T) {
	registry := NewDefaultLanguageAdapterRegistry()
	for _, languageID := range []string{"markdown", "json", "yaml"} {
		adapter, ok := registry.AdapterForLanguage(languageID)
		if !ok {
			t.Fatalf("default adapter registry missing %q", languageID)
		}
		policy := adapter.CapabilityPolicy()
		if !policy.RequiresLSPClient || policy.DocumentSymbolFallback {
			t.Fatalf("%s policy = %#v, want real LSP client without document fallback", languageID, policy)
		}
	}
}

func TestLanguageAdapterRegistryOwnsRootEnvBootstrapPolicy(t *testing.T) {
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: ""})
	root := canonicalScopePath(t.TempDir(), "")
	goDir := filepath.Join(root, "goapp")
	writeGenericTestFile(t, filepath.Join(goDir, "go.mod"), "module example.test/goapp\n\ngo 1.25.0\n")
	writeGenericTestFile(t, filepath.Join(goDir, "main.go"), "package main\n")
	goWorkPath := filepath.Join(root, "go.work")
	writeGenericTestFile(t, goWorkPath, "go 1.25.0\n\nuse ./goapp\n")

	jsRoot := filepath.Join(root, "web")
	writeGenericTestFile(t, filepath.Join(jsRoot, "package.json"), `{"name":"web"}`)
	writeGenericTestFile(t, filepath.Join(jsRoot, "src", "app.ts"), "export const app = 1\n")

	registry := NewDefaultLanguageAdapterRegistry()
	assertGoAdapterPolicies(t, ctx, registry, root, goDir, goWorkPath)
	assertTypeScriptAdapterPolicies(t, ctx, registry, root, jsRoot)
}

func assertGoAdapterPolicies(t *testing.T, ctx context.Context, registry *LanguageAdapterRegistry, root, goDir, goWorkPath string) {
	t.Helper()
	goAdapter, ok := registry.AdapterForLanguage("go")
	if !ok {
		t.Fatal("missing go adapter")
	}
	goScope, err := goAdapter.ResolveRoot(ctx, LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "go",
		TargetPath: filepath.Join(goDir, "main.go"),
	}, filepath.Join(goDir, "main.go"))
	if err != nil {
		t.Fatalf("go ResolveRoot: %v", err)
	}
	if goScope.RootKind != goRootKindGoWork || goScope.WorkspaceRoot != root || goScope.LanguageWorkspaceRoot != goDir {
		t.Fatalf("go resolved scope = %#v, want go.work root %q and module root %q", goScope, root, goDir)
	}
	if got, want := goAdapter.EnvPolicy(goScope), hostGoEnv("GOWORK="+goWorkPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("go EnvPolicy = %#v, want %#v", got, want)
	}
	if policy := goAdapter.BootstrapPolicy(goScope); policy.OpenSiblingDocuments || len(policy.SiblingExtensions) != 0 {
		t.Fatalf("go BootstrapPolicy = %#v, want no sibling bootstrap", policy)
	}
	if policy := goAdapter.BootstrapPolicy(goScope); !policy.TreatMissingDiagnosticsAsEmpty {
		t.Fatalf("go BootstrapPolicy = %#v, want missing empty diagnostics accepted after ready bootstrap", policy)
	}
}

func TestGoAdapterUsesTargetBuildTagsInEnvAndInitOptions(t *testing.T) {
	t.Setenv("GOFLAGS", "-mod=mod")
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module example.test/e2e\n\ngo 1.25.0\n")
	target := filepath.Join(root, "lsp_binary_e2e_probe_test.go")
	writeGenericTestFile(t, target, strings.Join([]string{
		"//go:build e2e",
		"",
		"package main",
		"",
		"func TestProbe(t testingT) {}",
		"",
		"type testingT interface { Helper() }",
		"",
	}, "\n"))

	registry := NewDefaultLanguageAdapterRegistry()
	goAdapter, ok := registry.AdapterForLanguage("go")
	if !ok {
		t.Fatal("missing go adapter")
	}
	scope, err := goAdapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "go",
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("go ResolveRoot: %v", err)
	}
	if got := scope.LanguageSpecific[goBuildTagsLanguageSpecificKey]; got != "e2e" {
		t.Fatalf("go build tags = %q, want e2e", got)
	}
	if got, want := goAdapter.EnvPolicy(scope), hostGoEnv("GOFLAGS=-mod=mod -tags=e2e"); !reflect.DeepEqual(got, want) {
		t.Fatalf("go EnvPolicy = %#v, want %#v", got, want)
	}
	initOptions := goAdapter.InitOptions(scope)
	if got, want := initOptions["buildFlags"], []string{"-tags=e2e"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("go InitOptions buildFlags = %#v, want %#v", got, want)
	}
}

func TestGoAdapterLeavesStandaloneIgnoreTagToGopls(t *testing.T) {
	t.Setenv("GOFLAGS", "-mod=mod")
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module example.test/standalone\n\ngo 1.25.0\n")
	target := filepath.Join(root, "standalone.go")
	writeGenericTestFile(t, target, strings.Join([]string{
		"//go:build ignore",
		"",
		"package main",
		"",
		"func main() {}",
		"",
	}, "\n"))

	registry := NewDefaultLanguageAdapterRegistry()
	goAdapter, ok := registry.AdapterForLanguage("go")
	if !ok {
		t.Fatal("missing go adapter")
	}
	scope, err := goAdapter.ResolveRoot(context.Background(), LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "go",
		TargetPath: target,
	}, target)
	if err != nil {
		t.Fatalf("go ResolveRoot: %v", err)
	}
	if got := scope.LanguageSpecific[goBuildTagsLanguageSpecificKey]; got != "" {
		t.Fatalf("standalone go build tags = %q, want empty", got)
	}
	if got, want := goAdapter.EnvPolicy(scope), hostGoEnv(); !reflect.DeepEqual(got, want) {
		t.Fatalf("standalone go EnvPolicy = %#v, want %#v", got, want)
	}
	if _, ok := goAdapter.InitOptions(scope)["buildFlags"]; ok {
		t.Fatal("standalone go InitOptions unexpectedly contains buildFlags")
	}
}

func TestDefaultGoStandaloneMainSource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{name: "go build ignore main", source: "//go:build ignore\n\npackage main\n", want: true},
		{name: "plus build ignore main", source: "// +build ignore\n\npackage main\n", want: true},
		{name: "compound constraint", source: "//go:build ignore && linux\n\npackage main\n", want: false},
		{name: "non main package", source: "//go:build ignore\n\npackage helper\n", want: false},
		{name: "ordinary tag", source: "//go:build e2e\n\npackage main\n", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDefaultGoStandaloneMainSource(tt.source); got != tt.want {
				t.Fatalf("IsDefaultGoStandaloneMainSource() = %v, want %v", got, tt.want)
			}
		})
	}
}

func assertTypeScriptAdapterPolicies(t *testing.T, ctx context.Context, registry *LanguageAdapterRegistry, root, jsRoot string) {
	t.Helper()
	tsAdapter, ok := registry.AdapterForLanguage("typescript")
	if !ok {
		t.Fatal("missing typescript adapter")
	}
	tsTarget := filepath.Join(jsRoot, "src", "app.ts")
	tsScope, err := tsAdapter.ResolveRoot(ctx, LSPToolScope{
		Family:     defaultLSPToolFamily,
		CWD:        root,
		LanguageID: "typescript",
		TargetPath: tsTarget,
	}, tsTarget)
	if err != nil {
		t.Fatalf("typescript ResolveRoot: %v", err)
	}
	if tsScope.WorkspaceRoot != jsRoot || tsScope.LanguageWorkspaceRoot != jsRoot {
		t.Fatalf("typescript resolved scope = %#v, want package root %q", tsScope, jsRoot)
	}
	if env := tsAdapter.EnvPolicy(tsScope); containsEnvKey(env, "GOWORK") {
		t.Fatalf("typescript EnvPolicy leaked GOWORK: %#v", env)
	}
	if policy := tsAdapter.BootstrapPolicy(tsScope); len(policy.FirstSourceExtensions) == 0 {
		t.Fatalf("typescript BootstrapPolicy = %#v, want adapter-owned first source bootstrap", policy)
	}
}

func TestTypeScriptEnsureClientDoesNotLeakGOWORK(t *testing.T) {
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "external.go.work"))
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")

	factory := &genericMatrixClientFactory{}
	manager := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory})
	defer func() { _ = manager.Close() }()

	if _, err := manager.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript"); err != nil {
		t.Fatalf("EnsureClient(typescript): %v", err)
	}
	call := factory.callAt(t, 0)
	if call.rootDir != root {
		t.Fatalf("typescript rootDir = %q, want %q", call.rootDir, root)
	}
	if containsEnvKey(call.env, "GOWORK") {
		t.Fatalf("typescript env leaked GOWORK: %#v", call.env)
	}
}

func TestAdapterLanguageSpecificHashFeedsWorkspaceKey(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "example.fake")
	writeGenericTestFile(t, target, "content\n")

	adapters := NewLanguageAdapterRegistry()
	adapters.Register(hashOnlyAdapter{languageIDs: []string{"fake"}, workspaceRoot: root, hash: "one"})
	manager := NewManager(Config{WorkspaceRoot: root, LanguageAdapters: adapters}).(*manager)
	resolver := NewRegistryScopedResolver(manager)
	if resolver == nil {
		t.Fatal("NewRegistryScopedResolver returned nil")
	}
	scope := LSPToolScope{Family: defaultLSPToolFamily, AgentID: "agent", ThreadID: "thread", CWD: root, LanguageID: "fake", TargetPath: target}
	first, err := resolver.ForToolScope(managerToolScope(scope))
	if err != nil {
		t.Fatalf("ForToolScope(first): %v", err)
	}
	adapters.Register(hashOnlyAdapter{languageIDs: []string{"fake"}, workspaceRoot: root, hash: "two"})
	second, err := resolver.ForToolScope(managerToolScope(scope))
	if err != nil {
		t.Fatalf("ForToolScope(second): %v", err)
	}
	if first.ResolvedScope.WorkspaceKey == second.ResolvedScope.WorkspaceKey {
		t.Fatalf("WorkspaceKey did not include adapter CacheKeyParts: %q", first.ResolvedScope.WorkspaceKey)
	}
	firstKey := resolvedLSPToolScopeFromManagerScope(first.ResolvedScope).cacheKey("fake", fileURIFromPath(target))
	secondKey := resolvedLSPToolScopeFromManagerScope(second.ResolvedScope).cacheKey("fake", fileURIFromPath(target))
	if firstKey.LanguageSpecificHash == "" || firstKey.LanguageSpecificHash == secondKey.LanguageSpecificHash {
		t.Fatalf("cache LanguageSpecificHash did not track adapter hash: first=%q second=%q", firstKey.LanguageSpecificHash, secondKey.LanguageSpecificHash)
	}
}

func TestCacheKeySeparatesLanguageIDAcrossSameURI(t *testing.T) {
	uri := "file:///repo/shared"
	scope := ResolvedLSPToolScope{ScopeKey: "scope", WorkspaceKey: "workspace", LSPToolScope: LSPToolScope{LanguageID: "go"}}
	goKey := scope.cacheKey("go", uri)
	tsKey := scope.cacheKey("typescript", uri)
	if goKey.String() == tsKey.String() {
		t.Fatalf("cache key did not separate language IDs: %q", goKey.String())
	}
}

func TestDiagnosticsAllNoCrossLanguageCacheLeak(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	sharedPath := filepath.Join(root, "shared.txt")
	writeGenericTestFile(t, sharedPath, "shared\n")
	uri := fileURIFromPath(sharedPath)
	mgr := NewManager(Config{WorkspaceRoot: root}).(*manager)
	gen := mgr.CurrentDiagnosticGeneration()
	goScope := ResolvedLSPToolScope{
		LSPToolScope: LSPToolScope{Family: defaultLSPToolFamily, AgentID: "agent", ThreadID: "thread", CWD: root, LanguageID: "go", WorkspaceRoot: root, LanguageWorkspaceRoot: root, ProjectRoot: root, RootKind: "go_mod"},
		ScopeKey:     "lsp\x00agent\x00thread",
		WorkspaceKey: "go-workspace",
		ManagerKey:   "go-manager",
	}
	tsScope := goScope
	tsScope.LanguageID = "typescript"
	tsScope.RootKind = "project_marker"
	tsScope.WorkspaceKey = "ts-workspace"
	tsScope.ManagerKey = "ts-manager"

	mgr.diagnostics[diagnosticStoreKeyFor(goScope, uri).String()] = diagnosticSnapshot{scopeKey: goScope.ScopeKey, workspaceKey: goScope.WorkspaceKey, language: "go", uri: uri, generation: gen, state: diagnosticStateReady, params: protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "go diag"}}}}
	mgr.diagnostics[diagnosticStoreKeyFor(tsScope, uri).String()] = diagnosticSnapshot{scopeKey: tsScope.ScopeKey, workspaceKey: tsScope.WorkspaceKey, language: "typescript", uri: uri, generation: gen, state: diagnosticStateReady, params: protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{{Message: "ts diag"}}}}

	ctx := WithResolvedLSPToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), goScope)
	got, err := mgr.Diagnostics(ctx, nil)
	if err != nil {
		t.Fatalf("Diagnostics(all): %v", err)
	}
	if len(got) != 1 || got[0].Diagnostics[0].Message != "go diag" {
		t.Fatalf("Diagnostics(all) = %#v, want only current language/workspace diagnostics", got)
	}

	cache := mustLSPCacheStore(t, lspCacheConfig{})
	cache.Upsert(lspCacheValue{Key: goScope.cacheKey("go", uri), Version: 1, UpdatedAt: cache.now()})
	if _, ok := cache.Load(tsScope.cacheKey("typescript", uri)); ok {
		t.Fatalf("cache loaded go document through typescript scope/language")
	}
}

type genericBootstrapMatrixCase struct {
	languageID string
	fileName   string
	markerName string
	markerBody string
}

func TestGenericLanguageServicesBootstrapCacheDiagnosticsMatrix(t *testing.T) {
	for _, tc := range []genericBootstrapMatrixCase{
		{languageID: "go", fileName: "main.go", markerName: "go.mod", markerBody: "module example.test/matrix\n\ngo 1.25.0\n"},
		{languageID: "javascript", fileName: "app.js", markerName: "package.json", markerBody: `{"name":"matrix"}`},
		{languageID: "typescript", fileName: "app.ts", markerName: "tsconfig.json", markerBody: `{"compilerOptions":{}}`},
		{languageID: "python", fileName: "app.py", markerName: "pyproject.toml", markerBody: "[project]\nname = \"matrix\"\n"},
		{languageID: "rust", fileName: "main.rs", markerName: "Cargo.toml", markerBody: "[package]\nname = \"matrix\"\nversion = \"0.1.0\"\n"},
		{languageID: "java", fileName: filepath.Join("src", "Main.java"), markerName: "pom.xml", markerBody: "<project></project>\n"},
		{languageID: "css", fileName: "style.css", markerName: "package.json", markerBody: `{"name":"matrix-css"}`},
	} {
		t.Run(tc.languageID, func(t *testing.T) {
			runGenericBootstrapCacheDiagnosticsCase(t, tc)
		})
	}
}

func runGenericBootstrapCacheDiagnosticsCase(t *testing.T, tc genericBootstrapMatrixCase) {
	t.Helper()
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, tc.markerName), tc.markerBody)
	target := filepath.Join(root, tc.fileName)
	writeGenericTestFile(t, target, "symbol\n")
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx := common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{AgentID: "agent-" + tc.languageID, ThreadID: "thread", Family: "lsp", CWD: root})

	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument(%s): %v", tc.languageID, err)
	}
	client := factory.clientAt(t, 0)
	if !client.opened(fileURIFromPath(target), tc.languageID) {
		t.Fatalf("%s target was not opened during bootstrap; opens=%#v", tc.languageID, client.openEvents())
	}
	indexed, ok := mustBootstrapCoordinator(t, mgr).cache.LastResolvedScope(fileURIFromPath(target))
	if !ok {
		t.Fatalf("%s bootstrap did not remember resolved scope", tc.languageID)
	}
	if indexed.LastResolvedScope.LanguageID != tc.languageID || indexed.LastResolvedScope.WorkspaceKey == "" {
		t.Fatalf("%s resolved scope = %#v", tc.languageID, indexed.LastResolvedScope)
	}
	if err := client.publishDiagnostic(protocol.PublishDiagnosticsParams{URI: fileURIFromPath(target), Diagnostics: []protocol.Diagnostic{{Message: tc.languageID + " diag"}}}); err != nil {
		t.Fatalf("publish diagnostic: %v", err)
	}
	diagnostics, err := mgr.Diagnostics(WithResolvedLSPToolScope(ctx, indexed.LastResolvedScope), nil)
	if err != nil {
		t.Fatalf("Diagnostics(%s): %v", tc.languageID, err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Diagnostics[0].Message != tc.languageID+" diag" {
		t.Fatalf("Diagnostics(%s) = %#v", tc.languageID, diagnostics)
	}
}

func TestDeadClientRestartRebootstrapForRegisteredLanguageIDs(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const app = 1\n")
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx := common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{AgentID: "agent", ThreadID: "thread", Family: "lsp", CWD: root})
	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("initial bootstrap: %v", err)
	}
	first := factory.clientAt(t, 0)
	if !first.opened(fileURIFromPath(target), "typescript") {
		t.Fatalf("initial typescript bootstrap opens=%#v", first.openEvents())
	}
	workspace := snapshotWorkspaceClients(mgr)
	if len(workspace) != 1 {
		t.Fatalf("workspace clients = %d, want 1", len(workspace))
	}
	ageWorkspaceForLifecycleTest(t, mgr, workspace[0].client)
	resolved, ok := mustBootstrapCoordinator(t, mgr).cache.LastResolvedScope(fileURIFromPath(target))
	if !ok {
		t.Fatal("missing resolved scope after initial bootstrap")
	}
	if _, err := recycleWorkspaceClient(mgr, resolved.LastResolvedScope, workspace[0]); err != nil {
		t.Fatalf("recycleWorkspaceClient: %v", err)
	}
	second := factory.clientAt(t, 1)
	if !first.closed {
		t.Fatalf("old client was not closed during recycle")
	}
	if second.initLanguageID != "typescript" {
		t.Fatalf("recycled client language = %q, want typescript", second.initLanguageID)
	}
	if !second.opened(fileURIFromPath(target), "typescript") {
		t.Fatalf("recycled typescript client did not rebootstrap target; opens=%#v", second.openEvents())
	}
	if containsEnvKey(factory.callAt(t, 1).env, "GOWORK") {
		t.Fatalf("recycled typescript env leaked GOWORK: %#v", factory.callAt(t, 1).env)
	}
}

func TestGenericManagerHasNoLanguageSpecificRootBranches(t *testing.T) {
	for _, file := range []string{
		"manager.go",
		"manager_lifecycle.go",
		"registry_scope.go",
		"recycler.go",
		"manager_symbols.go",
	} {
		content, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, banned := range []string{"shouldUseGoWorkspace", "shouldUseJSTSWorkspace", "shouldUseJavaWorkspace", "ResolveGoRoot", "findJSTS", "findJava", "GOWORK", `languageID = "go"`} {
			if strings.Contains(string(content), banned) {
				t.Fatalf("%s still contains generic-layer language branch %q", file, banned)
			}
		}
	}
}

func TestConfiguredDocumentFallbackLanguagesUseCapabilityFallback(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{
		WorkspaceRoot:    root,
		ClientFactory:    factory,
		LanguageAdapters: NewLanguageAdapterRegistry(documentFallbackAdapter{languageIDs: []string{"markdown", "json", "yaml"}}),
	}).(*manager)
	defer func() { _ = mgr.Close() }()
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "README.md", body: "# Title\n", want: "Title"},
		{name: "config.json", body: "{\n  \"name\": \"demo\"\n}\n", want: "name"},
		{name: "config.yaml", body: "name: demo\n", want: "name"},
	} {
		target := filepath.Join(root, tc.name)
		writeGenericTestFile(t, target, tc.body)
		symbols, err := mgr.DocumentSymbol(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target)
		if err != nil {
			t.Fatalf("DocumentSymbol(%s): %v", tc.name, err)
		}
		if len(symbols) == 0 || symbols[0].Name != tc.want {
			t.Fatalf("DocumentSymbol(%s) = %#v, want first symbol %q", tc.name, symbols, tc.want)
		}
	}
	if got := factory.callCount(); got != 0 {
		t.Fatalf("fallback document languages started %d LSP clients", got)
	}
}

func TestGoBootstrapDocumentDoesNotOpenSiblingFiles(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module example.test/go-no-siblings\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	sibling := filepath.Join(root, "helper.go")
	writeGenericTestFile(t, target, "package main\nfunc main() {}\n")
	writeGenericTestFile(t, sibling, "package main\nfunc helper() {}\n")
	factory := &genericMatrixClientFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()
	ctx := common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{AgentID: "agent-go", ThreadID: "thread-go", Family: "lsp", CWD: root})

	if err := mgr.BootstrapDocument(ctx, target); err != nil {
		t.Fatalf("BootstrapDocument(go): %v", err)
	}
	client := factory.clientAt(t, 0)
	targetURI := fileURIFromPath(target)
	siblingURI := fileURIFromPath(sibling)
	if !client.opened(targetURI, "go") {
		t.Fatalf("go target was not opened; opens=%#v", client.openEvents())
	}
	if client.opened(siblingURI, "go") {
		t.Fatalf("go sibling should not be opened during bootstrap; opens=%#v", client.openEvents())
	}
}

type hashOnlyAdapter struct {
	languageIDs   []string
	workspaceRoot string
	hash          string
}

func (a hashOnlyAdapter) LanguageIDs() []string { return append([]string(nil), a.languageIDs...) }
func (a hashOnlyAdapter) ResolveRoot(context.Context, LSPToolScope, string) (ResolvedLanguageScope, error) {
	return ResolvedLanguageScope{LanguageID: a.languageIDs[0], WorkspaceRoot: a.workspaceRoot, LanguageWorkspaceRoot: a.workspaceRoot, ProjectRoot: a.workspaceRoot, RootKind: "fake"}, nil
}
func (a hashOnlyAdapter) ServerCommand(context.Context, ResolvedLanguageScope) (ServerCommand, error) {
	return ServerCommand{}, nil
}
func (a hashOnlyAdapter) InitOptions(ResolvedLanguageScope) map[string]any { return nil }
func (a hashOnlyAdapter) EnvPolicy(ResolvedLanguageScope) []string         { return nil }
func (a hashOnlyAdapter) BootstrapPolicy(ResolvedLanguageScope) BootstrapPolicy {
	return BootstrapPolicy{OpenTarget: true}
}
func (a hashOnlyAdapter) CacheKeyParts(ResolvedLanguageScope) map[string]string {
	return map[string]string{"adapterHash": a.hash}
}
func (a hashOnlyAdapter) CapabilityPolicy() ToolCapabilityPolicy {
	return ToolCapabilityPolicy{RequiresLSPClient: true}
}

type genericMatrixClientFactory struct {
	mu      sync.Mutex
	calls   []genericMatrixFactoryCall
	clients []*genericMatrixClient
}

type genericMatrixFactoryCall struct {
	rootDir string
	env     []string
}

func (f *genericMatrixClientFactory) NewClient(rootDir string, handler protocol.NotificationHandler) (Client, error) {
	return f.NewClientWithEnv(rootDir, nil, handler)
}

func (f *genericMatrixClientFactory) NewClientWithEnv(rootDir string, env []string, handler protocol.NotificationHandler) (Client, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	client := &genericMatrixClient{handler: handler, documents: map[string]string{}}
	f.calls = append(f.calls, genericMatrixFactoryCall{rootDir: rootDir, env: append([]string(nil), env...)})
	f.clients = append(f.clients, client)
	return client, nil
}

func (f *genericMatrixClientFactory) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *genericMatrixClientFactory) callAt(t *testing.T, index int) genericMatrixFactoryCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.calls) {
		t.Fatalf("factory call %d out of range; calls=%d", index, len(f.calls))
	}
	return f.calls[index]
}

func (f *genericMatrixClientFactory) clientAt(t *testing.T, index int) *genericMatrixClient {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index < 0 || index >= len(f.clients) {
		t.Fatalf("factory client %d out of range; clients=%d", index, len(f.clients))
	}
	return f.clients[index]
}

type genericOpenEvent struct {
	uri      string
	language string
}

type genericMatrixClient struct {
	mu             sync.Mutex
	handler        protocol.NotificationHandler
	documents      map[string]string
	opens          []genericOpenEvent
	closed         bool
	initLanguageID string
}

func (c *genericMatrixClient) setWorkspaceFolders(folders []protocol.WorkspaceFolder) {
	_ = c
	_ = folders
}

func (c *genericMatrixClient) Initialize(ctx context.Context, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if resolved, ok := resolvedLSPToolScopeFromContext(ctx); ok {
		c.initLanguageID = resolved.LanguageID
	}
	return nil
}
func (c *genericMatrixClient) Shutdown(context.Context) error { return nil }
func (c *genericMatrixClient) Request(context.Context, string, any) (json.RawMessage, error) {
	return json.RawMessage("null"), nil
}
func (c *genericMatrixClient) Notify(context.Context, string, any) error { return nil }
func (c *genericMatrixClient) DidOpen(_ context.Context, uri, languageID string, _ int, text string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opens = append(c.opens, genericOpenEvent{uri: uri, language: languageID})
	c.documents[uri] = text
	return nil
}
func (c *genericMatrixClient) DidChange(_ context.Context, uri string, _ int, changes []protocol.TextDocumentContentChangeEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(changes) > 0 {
		c.documents[uri] = changes[len(changes)-1].Text
	}
	return nil
}
func (c *genericMatrixClient) DidClose(_ context.Context, uri string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.documents, uri)
	return nil
}
func (c *genericMatrixClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *genericMatrixClient) publishDiagnostic(params protocol.PublishDiagnosticsParams) error {
	if c.handler == nil {
		return nil
	}
	return c.handler.PublishDiagnostics(params)
}

func (c *genericMatrixClient) opened(uri, languageID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range c.opens {
		if event.uri == uri && event.language == languageID {
			return true
		}
	}
	return false
}

func (c *genericMatrixClient) openEvents() []genericOpenEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]genericOpenEvent(nil), c.opens...)
}

func writeGenericTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func containsEnvKey(env []string, key string) bool {
	prefix := key + "="
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}
