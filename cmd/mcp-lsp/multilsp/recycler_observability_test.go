package multilsp

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

func TestRecyclerProbeDegradedCleanupFailureIsObservable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private", "workspace")
	now := time.Now()
	shutdownErr := errors.New("shutdown failed for " + root)
	closeErr := errors.New("close failed for " + root)
	client := &p2LifecycleClient{healthy: true, shutdownFailure: shutdownErr, closeFailure: closeErr}
	workspace := &workspaceClient{
		key: root, languageID: "typescript", client: client, lastActivity: now.Add(-2 * time.Minute), generation: 7,
		state: workspaceStateIdleCountdown, idleSince: now.Add(-2 * time.Minute),
	}
	var logs bytes.Buffer
	mgr := &manager{
		logger: slog.New(slog.NewJSONHandler(&logs, nil)), idleTimeout: time.Minute,
		workspaces: map[string]*workspaceClient{workspace.key: workspace},
	}
	recycler := newPoolRecycler(nil)
	recycler.now = func() time.Time { return now }
	recycler.rssProbe = func(Client) (uint64, int, error) {
		return 0, 0, errors.New("RSS probe unavailable")
	}
	scope := ResolvedLSPToolScope{ManagerKey: "manager-recycler-test", ScopeKey: "scope-recycler-test"}
	for range recyclerProbeDegradedThreshold {
		recycler.recycleIfNeeded(mgr, scope, *workspace)
	}

	assertRecyclerCleanupFailureLog(t, logs.String(), root, shutdownErr, closeErr)
	assertRecyclerCleanupOwner(t, mgr, workspace, client)
}

func TestIdleShutdownProtocolFailureDoesNotRetainClosedClient(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private", "workspace")
	now := time.Now()
	client := &p2LifecycleClient{healthy: true, shutdownFailure: errors.New("shutdown failed for " + root)}
	workspace := workspaceClient{
		key: root, languageID: "sql", client: client, lastActivity: now.Add(-2 * time.Minute), generation: 7,
		state: workspaceStateIdleCountdown, idleSince: now.Add(-2 * time.Minute),
	}
	var logs bytes.Buffer
	mgr := &manager{
		logger: slog.New(slog.NewJSONHandler(&logs, nil)), idleTimeout: time.Minute,
		workspaces: map[string]*workspaceClient{workspace.key: &workspace},
	}
	recycler := newPoolRecycler(nil)
	recycler.now = func() time.Time { return now }
	scope := ResolvedLSPToolScope{ManagerKey: "manager-idle-protocol", ScopeKey: "scope-idle-protocol"}
	recycler.shutdownIdleWorkspace(mgr, scope, workspace)

	logText := logs.String()
	for _, want := range []string{
		"LSP idle shutdown protocol degraded", `"action":"shutdown"`, `"action_result":"degraded"`,
		`"shutdown_error_sha256":`, `"reason":"idle_timeout"`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("protocol-only idle shutdown log missing %q: %s", want, logText)
		}
	}
	if strings.Contains(logText, "LSP idle shutdown cleanup failed") || strings.Contains(logText, root) {
		t.Fatalf("protocol-only idle shutdown logged cleanup failure or leaked path: %s", logText)
	}
	if got := snapshotWorkspaceClients(mgr); len(got) != 0 {
		t.Fatalf("protocol-only idle shutdown retained client = %#v", got)
	}
}

func assertRecyclerCleanupFailureLog(t *testing.T, logText, root string, shutdownErr, closeErr error) {
	t.Helper()
	for _, want := range []string{
		`"manager_key_sha256":`, `"scope_key_sha256":`,
		`"workspace_sha256":`, `"workspace_key_sha256":`, `"generation":7`, `"active_leases":0`,
		`"state":"IdleCountdown"`, `"idle_duration":"2m0s"`, `"idle_timeout":"1m0s"`,
		`"action":"shutdown"`, `"action_result":"failed"`, `"recycled":false`,
		`"reason":"probe_degraded"`, `"cleanup_error_sha256":`,
		`"cleanup_error_class":"shutdown_or_close"`, `"cleanup_pending":false`,
		`"owner_missing":false`, `"members_remaining":false`,
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("degraded cleanup log missing %q: %s", want, logText)
		}
	}
	if !strings.Contains(logText, "LSP recycler degraded cleanup failed") || strings.Contains(logText, root) {
		t.Fatalf("degraded cleanup log message/path check failed: %s", logText)
	}
	if strings.Contains(logText, `"signal_sent":true`) {
		t.Fatalf("degraded cleanup log fabricated a signal_sent=true result: %s", logText)
	}
	joinedErr := errors.Join(shutdownErr, closeErr)
	if !strings.Contains(logText, fmt.Sprintf("%x", sha256.Sum256([]byte(joinedErr.Error())))) {
		t.Fatalf("degraded cleanup log did not retain joined shutdown/close error summary: %s", logText)
	}
}

func assertRecyclerCleanupOwner(t *testing.T, mgr *manager, workspace *workspaceClient, client Client) {
	t.Helper()
	got := snapshotWorkspaceClients(mgr)
	if len(got) != 1 || got[0].client != client || got[0].generation != workspace.generation ||
		got[0].state != workspaceStateCleanupPending || got[0].activeLeases != 0 {
		t.Fatalf("cleanup owner after shutdown/close failure = %#v, want retained generation-owned owner", got)
	}
}

func assertStructuredLogContains(t *testing.T, label, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("%s missing %q: %s", label, want, text)
		}
	}
}

func assertStructuredLogOmits(t *testing.T, label, text string, forbidden ...string) {
	t.Helper()
	for _, value := range forbidden {
		if strings.Contains(text, value) {
			t.Fatalf("%s leaked %q: %s", label, value, text)
		}
	}
}

func TestRecyclerCleanupLogContractRedactsScopeAndError(t *testing.T) {
	root := t.TempDir()
	scope := buildTestResolvedScope(t, root, "agent-secret", "thread-secret", "go")
	workspaceKey := filepath.Join(root, "workspace")
	cleanupErr := errors.Join(hiddenexec.ErrProcessTreeRemaining, errors.New("cleanup failed for "+root))
	now := time.Date(2026, 8, 4, 6, 30, 0, 0, time.UTC)
	workspace := workspaceClient{
		key: workspaceKey, languageID: "go", generation: 11,
		state: workspaceStateCleanupPending, activeLeases: 0,
		idleSince: now.Add(-2 * time.Minute), lastActivity: now.Add(-90 * time.Second),
	}
	var logs bytes.Buffer
	mgr := &manager{
		instanceID:  filepath.Join(root, "manager-instance"),
		logger:      slog.New(slog.NewJSONHandler(&logs, nil)),
		idleTimeout: time.Minute,
	}
	logRecyclerCleanupFailure(mgr, scope, []workspaceClient{workspace}, now, "shutdown", "idle_timeout", cleanupErr, "cleanup failure")
	text := logs.String()
	assertStructuredLogContains(t, "cleanup log", text,
		`"manager_key_sha256":`,
		`"scope_key_sha256":`,
		`"workspace_sha256":`,
		`"workspace_key_sha256":`,
		`"generation":11`,
		`"active_leases":0`,
		`"state":"CleanupPending"`,
		`"idle_duration":"2m0s"`,
		`"idle_timeout":"1m0s"`,
		`"action":"shutdown"`,
		`"action_result":"failed"`,
		`"cleanup_error_sha256":`,
		`"cleanup_error_class":"members_remaining"`,
	)
	assertStructuredLogOmits(t, "cleanup log", text,
		root, "agent-secret", "thread-secret", "cleanup failed for",
		`"manager_key":"`, `"scope_key":"`, `"agent_id":"`, `"thread_id":"`,
	)

	logs.Reset()
	logRecyclerCleanupFailure(mgr, scope, nil, now, "close", "deferred_release", errors.New("manager close failed for "+root), "manager cleanup failure")
	text = logs.String()
	assertStructuredLogContains(t, "manager cleanup log", text,
		`"workspace_count":0`,
		`"manager_instance_sha256":`,
		`"cleanup_error_sha256":`,
		`"cleanup_error_class":"shutdown_or_close"`,
		`"action":"close"`,
		`"action_result":"failed"`,
	)
	assertStructuredLogOmits(t, "manager cleanup log", text, root, `"manager_key":"`, `"scope_key":"`)

	logs.Reset()
	logRecyclerCleanupPending(mgr, scope, nil, now, "close", "deferred_release", "manager cleanup pending")
	text = logs.String()
	assertStructuredLogContains(t, "pending cleanup log", text,
		`"workspace_count":0`,
		`"action_result":"pending"`,
		`"cleanup_incomplete":true`,
		`"manager_instance_sha256":`,
	)
	assertStructuredLogOmits(t, "pending cleanup log", text, root, "agent-secret", "thread-secret")
}

func TestRecyclerRSSDeferredLogRedactsWorkspace(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 8, 4, 6, 45, 0, 0, time.UTC)
	scope := buildTestResolvedScope(t, root, "agent-rss-secret", "thread-rss-secret", "typescript")
	client := &p2LifecycleClient{healthy: true}
	workspace := workspaceClient{
		key: root, languageID: "typescript", client: client, generation: 3,
		state: workspaceStateActive, idleSince: now, lastActivity: now,
	}
	var logs bytes.Buffer
	mgr := &manager{
		logger:      slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		idleTimeout: time.Minute,
		workspaces:  map[string]*workspaceClient{root: &workspace},
	}
	recycler := newPoolRecycler(nil)
	recycler.now = func() time.Time { return now }
	recycler.rssProbe = func(Client) (uint64, int, error) {
		return defaultGoRSSLimitBytes + 1, 77, nil
	}
	recycler.recycleIfNeeded(mgr, scope, workspace)
	text := logs.String()
	assertStructuredLogContains(t, "RSS defer log", text,
		"LSP RSS pressure deferred until idle window",
		`"manager_key_sha256":`,
		`"scope_key_sha256":`,
		`"workspace_sha256":`,
		`"action":"defer"`,
		`"reason":"process_tree_rss_limit"`,
	)
	assertStructuredLogOmits(t, "RSS defer log", text,
		root, "agent-rss-secret", "thread-rss-secret", `"workspace":"`)
}

func TestRecyclerCleanupErrorClassUsesProcessTreeSentinels(t *testing.T) {
	tests := []struct {
		name                             string
		err                              error
		want                             string
		pending, ownerMissing, remaining bool
	}{
		{"cleanup pending", errors.Join(hiddenexec.ErrProcessTreeCleanupPending, errors.New("pending")), "cleanup_pending", true, false, false},
		{"owner missing", errors.Join(hiddenexec.ErrProcessTreeOwnerMissing, errors.New("owner missing")), "owner_missing", false, true, false},
		{"members remaining", errors.Join(hiddenexec.ErrProcessTreeRemaining, errors.New("members remain")), "members_remaining", false, false, true},
		{"shutdown or close", errors.New("shutdown and close failed"), "shutdown_or_close", false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := recyclerCleanupErrorFields(tt.err)
			got := make(map[string]any, len(fields)/2)
			for i := 0; i < len(fields); i += 2 {
				got[fields[i].(string)] = fields[i+1]
			}
			if got["cleanup_error_class"] != tt.want || got["cleanup_pending"] != tt.pending ||
				got["owner_missing"] != tt.ownerMissing || got["members_remaining"] != tt.remaining {
				t.Fatalf("cleanup classification fields = %#v, want class=%q pending=%t owner_missing=%t members_remaining=%t",
					got, tt.want, tt.pending, tt.ownerMissing, tt.remaining)
			}
		})
	}
}
