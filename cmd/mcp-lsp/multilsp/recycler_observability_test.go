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
		key: root, languageID: "go", client: client, lastActivity: now.Add(-2 * time.Minute), generation: 7,
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

func assertRecyclerCleanupFailureLog(t *testing.T, logText, root string, shutdownErr, closeErr error) {
	t.Helper()
	for _, want := range []string{
		`"manager_key":"manager-recycler-test"`, `"scope_key":"scope-recycler-test"`,
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
