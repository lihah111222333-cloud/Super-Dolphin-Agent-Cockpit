package multilsp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestTransportClosedDetachesWorkspaceClientAndRebuilds(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	firstClient, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(first): %v", err)
	}
	first := firstClient.(*p2LifecycleClient)
	first.markUnhealthy()

	secondClient, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(second): %v", err)
	}
	if secondClient == firstClient {
		t.Fatalf("dead transport client was reused instead of detached and rebuilt")
	}
	if !first.closed {
		t.Fatalf("dead client was not closed when detached")
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want 2 after rebuild", got)
	}
}

func TestRequestFailureReturnsRetriedResultAfterRebootstrap(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{requestFailures: []error{ErrTransportClosed, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	defer func() { _ = mgr.Close() }()

	before := mgr.CurrentDiagnosticGeneration()
	symbols, err := mgr.DocumentSymbol(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol error = %v, want successful replay after transport rebuild", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "rebuilt" {
		t.Fatalf("DocumentSymbol = %#v, want retried result from rebuilt client", symbols)
	}
	if got := mgr.CurrentDiagnosticGeneration(); got <= before {
		t.Fatalf("diagnostic generation = %d, want advanced beyond %d", got, before)
	}
	first := factory.clientAt(t, 0)
	second := factory.clientAt(t, 1)
	if !first.closed {
		t.Fatalf("failed request client was not closed")
	}
	if !second.opened(fileURIFromPath(target), "typescript") {
		t.Fatalf("rebuilt client did not restore bootstrapped TypeScript document; opens=%#v", second.openEvents())
	}
}

func TestNavigationRequestTimeoutRetriesOnceAfterRebuild(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"timeout-web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{requestFailures: []error{context.DeadlineExceeded, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	symbols, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target)
	if err != nil {
		t.Fatalf("DocumentSymbol error = %v, want one retry after the first step timeout", err)
	}
	if len(symbols) != 1 || symbols[0].Name != "rebuilt" {
		t.Fatalf("DocumentSymbol = %#v, want result from the single retry", symbols)
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want original plus one retry client", got)
	}
}

func TestNavigationRequestSecondTimeoutIsReportedWithoutThirdAttempt(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"timeout-twice-web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{requestFailures: []error{context.DeadlineExceeded, context.DeadlineExceeded, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.DocumentSymbol(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("DocumentSymbol error = %v, want second timeout", err)
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want exactly two attempts", got)
	}
	for index := range 2 {
		if got := factory.clientAt(t, index).requestCount(); got != 1 {
			t.Fatalf("client %d request count = %d, want one", index, got)
		}
	}
}

func TestRequestDeadClientDoesNotAutoReplayRename(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"rename-web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const oldName = 1\n")
	factory := &p2LifecycleFactory{requestFailures: []error{ErrTransportClosed, nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, DiagnosticsMaxWait: 1}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })

	_, err := mgr.Rename(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, protocol.Position{Line: 0, Character: 13}, "newName")
	if err == nil {
		t.Fatalf("Rename error = nil, want retryable dead-client error without auto replay")
	}
	first := factory.clientAt(t, 0)
	if got := first.requestCount(); got != 1 {
		t.Fatalf("first client Request count = %d, want exactly one rename attempt", got)
	}
	if got := first.requestMethods(); len(got) != 1 || got[0] != protocol.MethodRename {
		t.Fatalf("first client Request methods = %#v, want one %s", got, protocol.MethodRename)
	}
	if !first.closed {
		t.Fatalf("dead rename client was not closed/detached")
	}
	if got := factory.callCount(); got != 2 {
		t.Fatalf("factory calls = %d, want rebuild for retryable follow-up without replay", got)
	}
	second := factory.clientAt(t, 1)
	if got := second.requestCount(); got != 0 {
		t.Fatalf("rebuilt client Request count = %d, want no automatic rename replay; methods=%#v", got, second.requestMethods())
	}
	if !second.opened(fileURIFromPath(target), "typescript") {
		t.Fatalf("rebuilt client did not restore bootstrapped document; opens=%#v", second.openEvents())
	}
	if !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Rename error = %v, want retryable ErrClientClosed marker", err)
	}
}

func TestInitializeFailureDoesNotLeaveStaleWorkspaceClient(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "package.json"), `{"name":"web"}`)
	target := filepath.Join(root, "src", "app.ts")
	writeGenericTestFile(t, target, "export const value = 1\n")
	factory := &p2LifecycleFactory{initializeFailures: []error{errors.New("initialize boom"), nil}}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	defer func() { _ = mgr.Close() }()

	if _, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript"); err == nil {
		t.Fatalf("EnsureClient(first) error = nil, want initialize failure")
	}
	if got := len(snapshotWorkspaceClients(mgr)); got != 0 {
		t.Fatalf("workspace clients after initialize failure = %d, want 0 stale clients", got)
	}
	if _, err := mgr.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "typescript"); err != nil {
		t.Fatalf("EnsureClient(second): %v", err)
	}
	if got := len(snapshotWorkspaceClients(mgr)); got != 1 {
		t.Fatalf("workspace clients after successful retry = %d, want 1", got)
	}
}

func TestGoRSSLimitUsesLanguageSpecificDefault(t *testing.T) {
	if got := rssLimitBytesForLanguage("go"); got != defaultGoRSSLimitBytes {
		t.Fatalf("rssLimitBytesForLanguage(go) = %d, want %d", got, defaultGoRSSLimitBytes)
	}
}

func TestGenericRSSLimitUsesDefaultLimit(t *testing.T) {
	if got := rssLimitBytesForLanguage("typescript"); got != defaultCohortHardLimitBytes {
		t.Fatalf("rssLimitBytesForLanguage(typescript) = %d, want %d", got, defaultCohortHardLimitBytes)
	}
}

func TestWindowsGoplsUsesStandaloneFourGiBProcessLimit(t *testing.T) {
	if got := rssLimitBytesForLanguageOnOS("go", "windows"); got != defaultGoWindowsRSSLimitBytes {
		t.Fatalf("Windows standalone gopls RSS limit = %d, want %d", got, defaultGoWindowsRSSLimitBytes)
	}
}

func TestValidateResourceLimitEnvironmentRejectsInvalidAndInconsistentValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "nonnumeric", key: ResourceCohortHardLimitMBEnv, value: "large"},
		{name: "deprecated owner", key: DeprecatedResourceCohortHardLimitMBEnv, value: "15360"},
		{
			name:  "overflow",
			key:   ResourceCohortHardLimitMBEnv,
			value: strconv.FormatUint(^uint64(0)/(1024*1024)+1, 10),
		},
		{name: "local below cohort", key: lspRSSLimitEnv, value: "2048"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearResourceLimitEnvironmentForTest(t)
			t.Setenv(test.key, test.value)
			if err := ValidateResourceLimitEnvironment(); err == nil {
				t.Fatalf("ValidateResourceLimitEnvironment() accepted %s=%q", test.key, test.value)
			}
		})
	}
}

func TestValidateResourceLimitEnvironmentAcceptsConsistentOverrides(t *testing.T) {
	clearResourceLimitEnvironmentForTest(t)
	t.Setenv(lspRSSLimitEnv, "16384")
	t.Setenv(ResourceCohortHardLimitMBEnv, "15360")
	t.Setenv(lspGoplsHeapLimitEnv, "3584")
	if err := ValidateResourceLimitEnvironment(); err != nil {
		t.Fatalf("ValidateResourceLimitEnvironment() error = %v", err)
	}
}

func clearResourceLimitEnvironmentForTest(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		lspRSSLimitEnv,
		lspGoRSSLimitEnv,
		lspGoplsHeapLimitEnv,
		ResourceCohortHardLimitMBEnv,
	} {
		t.Setenv(key, "")
	}
}

func TestManagerStoresConfiguredIdleTimeout(t *testing.T) {
	const want = 37 * time.Minute
	mgr := NewManager(Config{IdleTimeout: want}).(*manager)
	if got := mgr.idleTimeout; got != want {
		t.Fatalf("manager idle timeout = %s, want %s", got, want)
	}
}

func TestDetachIdleWorkspaceRechecksConcurrentActivity(t *testing.T) {
	client := &p2LifecycleClient{}
	key := "workspace"
	mgr := &manager{workspaces: map[string]*workspaceClient{
		key: {
			key:          key,
			client:       client,
			generation:   1,
			state:        workspaceStateActive,
			lastActivity: time.Now(),
		},
	}}
	if detached := detachWorkspaceClientGeneration(mgr, key, client, 1); detached != nil {
		t.Fatal("detachWorkspaceClientGeneration() removed a workspace touched after the stale snapshot")
	}
	if got := mgr.workspaces[key]; got == nil || got.client != client {
		t.Fatal("recently touched workspace was not preserved")
	}
}

func TestPoolRecyclerIdleWorkspaceWinsOverRSSRecycle(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module idle\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, Logger: logger}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scoped := scopedManagerForTest(t, mgr, testLSPToolScopeForLanguage(root, "agent-idle", "thread-1", "typescript"))

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	client, err := scoped.EnsureClient(ctx, target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	forceWorkspaceLastActivity(t, scoped, client, time.Now().Add(-idleTimeoutForTest()-time.Minute))

	originalProbe := mgr.pool.recycler.rssProbe
	mgr.pool.recycler.rssProbe = func(Client) (uint64, int, error) {
		return defaultGoRSSLimitBytes + 1, 4242, nil
	}
	t.Cleanup(func() {
		mgr.pool.recycler.rssProbe = originalProbe
	})

	mgr.pool.recycler.check()

	if got := factory.callCount(); got != 1 {
		t.Fatalf("idle workspace was RSS-recycled and recreated; factory calls = %d, want 1", got)
	}
	if got := len(snapshotWorkspaceClients(scoped)); got != 0 {
		t.Fatalf("idle workspace clients after recycler check = %d, want 0", got)
	}
	if first := factory.clientAt(t, 0); !first.closed {
		t.Fatalf("idle workspace client was not closed")
	}
	assertIdleRecyclerDebugLog(t, logs.String())
}

func TestPoolRecyclerIdleShutdownReportsCleanupFailure(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory, Logger: logger}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scoped := scopedManagerForTest(t, mgr, testLSPToolScopeForLanguage(root, "agent-idle-error", "thread-1", "typescript"))
	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: root})
	client, err := scoped.EnsureClient(ctx, target, "typescript")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	first := factory.clientAt(t, 0)
	first.shutdownFailure = errors.New("shutdown failed at " + root)
	first.closeFailure = errors.New("close failed at " + root)
	forceWorkspaceLastActivity(t, scoped, client, time.Now().Add(-idleTimeoutForTest()-time.Minute))
	mgr.pool.recycler.checkIdleWorkspaces(scoped, ResolvedLSPToolScope{
		ManagerKey: "manager-idle-error",
		ScopeKey:   "scope-idle-error",
		LSPToolScope: LSPToolScope{
			LanguageID: "typescript",
		},
	})
	logText := logs.String()
	assertStructuredLogContains(t, "idle cleanup failure log", logText,
		"LSP idle shutdown cleanup failed",
		`"manager_key_sha256":`,
		`"scope_key_sha256":`,
		`"generation":1`,
		`"active_leases":0`,
		`"action":"shutdown"`,
		`"action_result":"failed"`,
		`"cleanup_error_sha256":`,
		`"cleanup_error_class":"shutdown_or_close"`,
	)
	assertStructuredLogOmits(t, "idle cleanup failure log", logText,
		root, `"manager_key":"`, `"scope_key":"`, `"action_result":"completed"`)
	assertIdleCleanupOwner(t, scoped, client, 1, "after Close failure")
	first.closeFailure = nil
	mgr.pool.recycler.checkIdleWorkspaces(scoped, ResolvedLSPToolScope{
		ManagerKey: "manager-idle-error",
		ScopeKey:   "scope-idle-error",
		LSPToolScope: LSPToolScope{
			LanguageID: "typescript",
		},
	})
	assertIdleCleanupOwner(t, scoped, client, 0, "after successful retry")
}

func TestRecyclerDoesNotStealCleanupOwnerFromClosingManager(t *testing.T) {
	client := &p2LifecycleClient{healthy: true}
	workspace := &workspaceClient{key: "workspace-closing-manager", languageID: "go", client: client, generation: 1, state: workspaceStateActive}
	mgr := &manager{
		closed:     true,
		retiring:   true,
		workspaces: map[string]*workspaceClient{workspace.key: workspace},
	}
	recycled, err := shutdownResourceCohortWorkspace(mgr, *workspace)
	if err != nil {
		t.Fatalf("shutdownResourceCohortWorkspace() error = %v", err)
	}
	if recycled || client.closed {
		t.Fatal("recycler stole cleanup ownership from a closing manager")
	}
	if got := snapshotWorkspaceClients(mgr); len(got) != 1 || got[0].client != client {
		t.Fatalf("closing manager cleanup owner = %#v, want original client", got)
	}
}

func TestRecyclerProbeFailureHealthAndRecovery(t *testing.T) {
	root := t.TempDir()
	secretPath := filepath.Join(root, "private", "workspace")
	probeErr := errors.New("probe failed for " + secretPath)
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	mgr := &manager{logger: logger}
	client := &p2LifecycleClient{}
	workspace := workspaceClient{key: secretPath, languageID: "typescript", client: client, generation: 1, state: workspaceStateActive}
	scope := ResolvedLSPToolScope{ManagerKey: "manager-test", ScopeKey: "scope-test"}
	recycler := newPoolRecycler(nil)
	recycler.rssProbe = func(Client) (uint64, int, error) {
		return 0, 0, probeErr
	}

	for range recyclerProbeDegradedThreshold {
		recycler.recycleIfNeeded(mgr, scope, workspace)
	}
	health := recycler.HealthSnapshot()
	assertRecyclerProbeFailureHealth(t, health)
	if client.closed {
		t.Fatal("probe failure closed client")
	}
	logText := logs.String()
	assertRecyclerProbeFailureLog(t, logText, root)

	recycler.rssProbe = func(Client) (uint64, int, error) { return 1, 42, nil }
	recycler.recycleIfNeeded(mgr, scope, workspace)
	health = recycler.HealthSnapshot()
	assertRecyclerProbeRecoveryHealth(t, health)
}

func TestRecyclerDoesNotProbeOrCloseGopls(t *testing.T) {
	client := &p2LifecycleClient{healthy: true}
	workspace := &workspaceClient{
		key:          "workspace-gopls-root-cohort-owner",
		languageID:   "go",
		client:       client,
		generation:   1,
		state:        workspaceStateIdleCountdown,
		idleSince:    time.Now().Add(-2 * idleTimeoutForTest()),
		lastActivity: time.Now(),
	}
	mgr := &manager{idleTimeout: idleTimeoutForTest(), workspaces: map[string]*workspaceClient{workspace.key: workspace}}
	recycler := newPoolRecycler(nil)
	probeCalls := 0
	recycler.rssProbe = func(Client) (uint64, int, error) {
		probeCalls++
		return 0, 0, errors.New("probe must not run for gopls")
	}
	for range recyclerProbeDegradedThreshold + 1 {
		recycler.recycleIfNeeded(mgr, ResolvedLSPToolScope{}, *workspace)
	}
	if probeCalls != 0 || client.closed {
		t.Fatalf("gopls recycler probe/close = (%d, %v), want (0, false)", probeCalls, client.closed)
	}
	if got := snapshotWorkspaceClients(mgr); len(got) != 1 || got[0].client != client {
		t.Fatalf("gopls workspace after legacy recycler = %#v, want retained root-controller owner", got)
	}
}

func TestRecyclerProbeDegradationFailsClosedForIdleClient(t *testing.T) {
	client := &p2LifecycleClient{healthy: true}
	workspace := &workspaceClient{
		key:          "workspace-probe-degraded",
		languageID:   "typescript",
		client:       client,
		generation:   1,
		state:        workspaceStateIdleCountdown,
		idleSince:    time.Now().Add(-2 * idleTimeoutForTest()),
		lastActivity: time.Now(),
	}
	mgr := &manager{idleTimeout: idleTimeoutForTest(), workspaces: map[string]*workspaceClient{workspace.key: workspace}}
	recycler := newPoolRecycler(nil)
	recycler.rssProbe = func(Client) (uint64, int, error) {
		return 0, 0, errors.New("probe unavailable")
	}
	for range recyclerProbeDegradedThreshold {
		recycler.recycleIfNeeded(mgr, ResolvedLSPToolScope{}, *workspace)
	}
	if !client.closed {
		t.Fatal("degraded RSS probe did not fail closed")
	}
	if got := snapshotWorkspaceClients(mgr); len(got) != 0 {
		t.Fatalf("workspace clients after fail-closed probe = %d, want 0", len(got))
	}
}

func TestRecyclerProbeFailuresAreTrackedPerClient(t *testing.T) {
	failingClient := &p2LifecycleClient{healthy: true}
	healthyClient := &p2LifecycleClient{healthy: true}
	failing := &workspaceClient{key: "workspace-failing-probe", languageID: "typescript", client: failingClient, generation: 1, state: workspaceStateIdleCountdown, idleSince: time.Now().Add(-2 * idleTimeoutForTest())}
	// 该测试只覆盖 probe 计数；健康 Go client 由 root cohort controller 接管，不参与旧 recycler。
	healthy := &workspaceClient{key: "workspace-healthy-probe", languageID: "go", client: healthyClient, generation: 1, state: workspaceStateActive}
	mgr := &manager{idleTimeout: idleTimeoutForTest(), workspaces: map[string]*workspaceClient{
		failing.key: failing,
		healthy.key: healthy,
	}}
	recycler := newPoolRecycler(nil)
	recycler.rssProbe = func(current Client) (uint64, int, error) {
		if current == failingClient {
			return 0, 0, errors.New("probe unavailable")
		}
		return 1, 42, nil
	}
	for range recyclerProbeDegradedThreshold {
		recycler.recycleIfNeeded(mgr, ResolvedLSPToolScope{}, *failing)
		recycler.recycleIfNeeded(mgr, ResolvedLSPToolScope{}, *healthy)
	}
	if !failingClient.closed {
		t.Fatal("healthy client probes reset the failing client's fail-closed threshold")
	}
	if healthyClient.closed {
		t.Fatal("per-client probe accounting closed a healthy client")
	}
}

func assertRecyclerProbeFailureHealth(t *testing.T, health recyclerHealthSnapshot) {
	t.Helper()
	if health.ProbeFailuresTotal != recyclerProbeDegradedThreshold ||
		health.ConsecutiveProbeFailures != recyclerProbeDegradedThreshold ||
		!health.Degraded || health.LastProbeError == "" || health.LastProbeAt.IsZero() {
		t.Fatalf("health after probe failures = %#v", health)
	}
}

func assertRecyclerProbeFailureLog(t *testing.T, logText, secretRoot string) {
	t.Helper()
	for _, want := range []string{
		"LSP recycler RSS probe failed", `"manager_key_sha256":`,
		`"scope_key_sha256":`, `"workspace_sha256":`, `"language":"typescript"`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("probe failure log missing %q: %s", want, logText)
		}
	}
	if strings.Contains(logText, secretRoot) {
		t.Fatalf("probe failure log leaked absolute path: %s", logText)
	}
}

func assertRecyclerProbeRecoveryHealth(t *testing.T, health recyclerHealthSnapshot) {
	t.Helper()
	if health.ProbeFailuresTotal != recyclerProbeDegradedThreshold ||
		health.ConsecutiveProbeFailures != 0 || health.Degraded ||
		health.LastProbeError != "" || health.LastProbeAt.IsZero() {
		t.Fatalf("health after probe recovery = %#v", health)
	}
}

func TestRecyclerInvalidPIDAndMultiWorkspaceFailuresAreObservable(t *testing.T) {
	clientA := &p2LifecycleClient{}
	clientB := &p2LifecycleClient{}
	mgr := &manager{workspaces: map[string]*workspaceClient{
		"workspace-a": {key: "workspace-a", languageID: "typescript", client: clientA, generation: 1, state: workspaceStateActive},
		"workspace-b": {key: "workspace-b", languageID: "typescript", client: clientB, generation: 1, state: workspaceStateActive},
	}}
	recycler := newPoolRecycler(nil)
	recycler.rssProbe = func(Client) (uint64, int, error) {
		return 1024, 0, nil
	}
	recycler.checkManager(0, mgr, ResolvedLSPToolScope{})
	health := recycler.HealthSnapshot()
	if health.ProbeFailuresTotal != 2 || health.ConsecutiveProbeFailures != 1 {
		t.Fatalf("multi-workspace invalid pid health = %#v", health)
	}
	if clientA.closed || clientB.closed {
		t.Fatal("invalid pid probe closed a client")
	}
}

func assertIdleCleanupOwner(t *testing.T, mgr *manager, client Client, wantCount int, phase string) {
	t.Helper()
	got := snapshotWorkspaceClients(mgr)
	if len(got) != wantCount || (wantCount == 1 && got[0].client != client) {
		t.Fatalf("cleanup owner %s = %#v, want count=%d with original client", phase, got, wantCount)
	}
}

func assertIdleRecyclerDebugLog(t *testing.T, logText string) {
	t.Helper()
	for _, want := range []string{
		"LSP recycler idle window exceeded",
		`"idle_timeout":"15m0s"`,
		`"action":"shutdown"`,
		`"pid":4242`,
		`"rss_bytes":536870913`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("idle recycler debug log missing %q; log=%s", want, logText)
		}
	}
}

func forceWorkspaceLastActivity(t *testing.T, mgr *manager, client Client, at time.Time) {
	t.Helper()
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	for _, workspace := range mgr.workspaces {
		if workspace != nil && workspace.client == client {
			workspace.generation = 1
			workspace.state = workspaceStateIdleCountdown
			workspace.idleSince = at
			workspace.lastActivity = at
			return
		}
	}
	t.Fatalf("workspace for client %T was not found", client)
}

func TestReleaseScopeClosesOnlyMatchingAgentThreadClone(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	alpha := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-a", "thread-a"))
	otherThread := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-a", "thread-b"))
	otherAgent := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-b", "thread-a"))

	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-a",
		ThreadID:  "thread-a",
		Drain:     true,
	})
	if err != nil {
		t.Fatalf("ReleaseScope(agent/thread): %v", err)
	}
	if result.MatchedManagers != 1 || result.ClosedManagers != 1 || result.BusyLeases != 0 || !result.Drained {
		t.Fatalf("ReleaseScope result = %#v, want one drained close", result)
	}
	if !managerIsClosed(alpha) {
		t.Fatalf("matching manager was not closed")
	}
	if managerIsClosed(otherThread) || managerIsClosed(otherAgent) {
		t.Fatalf("ReleaseScope closed unrelated manager: otherThread=%v otherAgent=%v", managerIsClosed(otherThread), managerIsClosed(otherAgent))
	}
}

func TestReleaseScopeRespectsActiveLeaseBusyOrDrain(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module busy\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scoped := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-busy", "thread-1"))
	client, err := scoped.EnsureClient(common.WithToolScope(common.WithToolScope(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), common.ToolScope{CWD: root}), target, "go")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	lease, bound, leaseErr := scoped.leaseBoundClient(client)
	if leaseErr != nil || !bound {
		t.Fatalf("leaseBoundClient(active): bound=%v err=%v", bound, leaseErr)
	}

	busy, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-busy", ThreadID: "thread-1", Reason: "busy_check"})
	if err != nil {
		t.Fatalf("ReleaseScope(busy): %v", err)
	}
	assertBusyReleaseScopeResult(t, busy, scoped)
	if err := lease.Release(); err != nil {
		t.Fatalf("release active lease: %v", err)
	}
	ageWorkspaceForLifecycleTest(t, scoped, client)

	drained, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{ScopeKind: ReleaseScopeAgentThread, AgentID: "agent-busy", ThreadID: "thread-1", Drain: true})
	if err != nil {
		t.Fatalf("ReleaseScope(drain): %v", err)
	}
	assertDrainedReleaseScopeResult(t, drained, scoped)
}

func TestReleaseScopeDrainClosesBusyManagerAfterLeaseRelease(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	writeGenericTestFile(t, filepath.Join(root, "go.mod"), "module deferred\n\ngo 1.25.0\n")
	target := filepath.Join(root, "main.go")
	writeGenericTestFile(t, target, "package main\n")
	factory := &p2LifecycleFactory{}
	mgr := NewManager(Config{WorkspaceRoot: root, ClientFactory: factory}).(*manager)
	t.Cleanup(func() { _ = mgr.Close() })
	scoped := scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-deferred", "thread-1"))
	client, err := scoped.EnsureClient(common.WithToolScope(context.Background(), common.ToolScope{CWD: root}), target, "go")
	if err != nil {
		t.Fatalf("EnsureClient(scoped): %v", err)
	}
	lease := acquireDeferredReleaseLease(t, scoped, client)
	result := releaseDeferredScope(t, mgr, "deferred_release")
	assertDeferredBusyReceipt(t, result, "initial")
	retry := releaseDeferredScope(t, mgr, "deferred_release_retry")
	assertDeferredBusyReceipt(t, retry, "retry")

	if err := lease.Release(); err != nil {
		t.Fatalf("release deferred lease: %v", err)
	}
	ageWorkspaceForLifecycleTest(t, scoped, client)
	mgr.pool.drainPendingReleases()
	if !managerIsClosed(scoped) {
		t.Fatal("busy scoped manager stayed open after its final lease was released")
	}
}

func acquireDeferredReleaseLease(t *testing.T, scoped *manager, client Client) leasedClient {
	t.Helper()
	lease, bound, err := scoped.leaseBoundClient(client)
	if err != nil || !bound {
		t.Fatalf("leaseBoundClient(deferred): bound=%v err=%v", bound, err)
	}
	return lease
}

func releaseDeferredScope(t *testing.T, mgr *manager, reason string) ReleaseScopeResult {
	t.Helper()
	result, err := mgr.pool.ReleaseScope(ReleaseScopeRequest{
		ScopeKind: ReleaseScopeAgentThread,
		AgentID:   "agent-deferred",
		ThreadID:  "thread-1",
		Drain:     true,
		Reason:    reason,
	})
	if err != nil {
		t.Fatalf("ReleaseScope(%s): %v", reason, err)
	}
	return result
}

func assertDeferredBusyReceipt(t *testing.T, result ReleaseScopeResult, phase string) {
	t.Helper()
	if result.BusyLeases != 1 || result.Drained {
		t.Fatalf("ReleaseScope(%s) = %#v, want one busy lease and drained=false", phase, result)
	}
}

func TestSnapshotManagersCloseRace(t *testing.T) {
	root := canonicalScopePath(t.TempDir(), "")
	mgr := newManagerPoolTestManager(t, root)
	_ = scopedManagerForTest(t, mgr, testLSPToolScope(root, "agent-race", "thread-1"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 500 {
			_ = mgr.pool.snapshotManagers()
		}
	}()

	for range 500 {
		_ = mgr.pool.Close()
	}
	<-done
}
