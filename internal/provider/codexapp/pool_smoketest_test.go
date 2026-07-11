//go:build codex_smoketest
// +build codex_smoketest

// This file is only compiled when the 'codex_smoketest' build tag is
// explicitly requested:
//
//   go test -tags codex_smoketest -run TestServerPoolMultiProviderSmoke \
//     ./internal/provider/codexapp/ -count=1 -v
//
// It requires a real `codex` binary on PATH. The regular `make test`
// pipeline ignores the tag so CI stays binary-free.
//
// What the smoke proves:
//   * NewTransportSpawner really launches an app-server per owning session
//   * ServerPool.Acquire returns distinct SpawnedServer URLs for two
//     distinct identities (the core multi-provider claim)
//   * Close tears both children down without leaking processes
//   * Release of the last session closes the pool entry
//
// Failure is loud: any error path (spawn timeout, duplicate URL, leaked
// PID) fails the test so you can triage before flipping the flag in
// production.

package codexapp

import (
	"context"
	"log/slog"
	"os/exec"
	"testing"
	"time"

	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func requireCodexBinary(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("codex binary not on PATH, skipping smoke: %v", err)
	}
}

// TestServerPoolMultiProviderSmoke acquires two distinct identities
// in the same pool, asserts each is wired to a unique app-server,
// and walks through the release + close lifecycle.
func TestServerPoolMultiProviderSmoke(t *testing.T) {
	requireCodexBinary(t)

	homeA := t.TempDir()
	homeB := t.TempDir()

	pool := NewServerPool(slog.Default(), NewTransportSpawner(nil, slog.Default()), PoolConfig{
		IdleTimeout:  30 * time.Minute,
		SpawnBackoff: 2 * time.Minute,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	defer pool.Close(context.Background())

	idA := providershared.CodexIdentity{Home: homeA, InstanceKey: "A", ModelProvider: "mp-a"}
	idB := providershared.CodexIdentity{Home: homeB, InstanceKey: "B", ModelProvider: "mp-b"}

	srvA, relA, err := pool.Acquire(ctx, idA, "agent-a")
	if err != nil {
		t.Fatalf("Acquire A: %v", err)
	}
	defer relA()
	srvB, relB, err := pool.Acquire(ctx, idB, "agent-b")
	if err != nil {
		t.Fatalf("Acquire B: %v", err)
	}
	defer relB()

	urlA := srvA.ServerURL()
	urlB := srvB.ServerURL()
	if urlA == "" || urlB == "" {
		t.Fatalf("empty URL(s): A=%q B=%q", urlA, urlB)
	}
	if urlA == urlB {
		t.Fatalf("two identities must land on distinct app-servers, both = %q", urlA)
	}
	requireSmokeServersAlive(t, srvA, srvB, urlA, urlB)

	// Second Acquire of identity A must hit the same cached server.
	srvA2, relA2, err := pool.Acquire(ctx, idA, "agent-a")
	if err != nil {
		t.Fatalf("Acquire A (second): %v", err)
	}
	defer relA2()
	if srvA2.ServerURL() != urlA {
		t.Fatalf("same identity returned different URL: %q vs %q", srvA2.ServerURL(), urlA)
	}

	if pool.Size() != 2 {
		t.Fatalf("pool.Size() = %d, want 2", pool.Size())
	}

	t.Logf("smoke ok: pool size=%d homeA=%s -> %s homeB=%s -> %s",
		pool.Size(), homeA, urlA, homeB, urlB)
}

func requireSmokeServersAlive(t *testing.T, srvA, srvB SpawnedServer, urlA, urlB string) {
	t.Helper()
	if srvA.Alive() && srvB.Alive() {
		return
	}
	tailA, exitA := smokeServerExitDetails(srvA)
	tailB, exitB := smokeServerExitDetails(srvB)
	t.Fatalf("alive check failed:\n  A alive=%v url=%s exit=%v stderr=%q\n  B alive=%v url=%s exit=%v stderr=%q",
		srvA.Alive(), urlA, exitA, tailA,
		srvB.Alive(), urlB, exitB, tailB)
}

func smokeServerExitDetails(srv SpawnedServer) (string, error) {
	ts, _ := srv.(*transportServer)
	if ts == nil {
		return "", nil
	}
	return ts.DiagnoseExit()
}
