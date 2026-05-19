package codexapp

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

func TestServerPoolAcquireSameOwnerDifferentLSPConfigSpawnsDistinctServers(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	p, _ := newPoolForTest(t, newCountingFakeSpawner(&spawnCalls), PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	ctxA := withPoolSpawnLSPConfig(context.Background(), []string{"/repo/a"}, "/bin/a")
	ctxB := withPoolSpawnLSPConfig(context.Background(), []string{"/repo/b"}, "/bin/b")
	srv1, rel1, err := p.Acquire(ctxA, id, "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer rel1()
	srv2, rel2, err := p.Acquire(ctxB, id, "agent-1")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer rel2()
	if srv1 == srv2 {
		t.Fatal("different LSP native MCP config must not reuse the same app-server")
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2 isolated app-servers", spawnCalls.Load())
	}
}

func TestPoolSpawnPolicySignatureIncludesLSPConfig(t *testing.T) {
	t.Parallel()
	ctx := withPoolSpawnLSPConfig(context.Background(), []string{"/repo/a", "/repo/b"}, "/bin/a")
	got := poolSpawnPolicySignature(ctx)
	for _, want := range []string{"/repo/a", "/repo/b", "/bin/a"} {
		if !strings.Contains(got, want) {
			t.Fatalf("policy signature %q missing %q", got, want)
		}
	}
}

func TestPoolSpawnLSPConfigResolvesRelativeRootsAgainstPrimaryRoot(t *testing.T) {
	t.Parallel()
	ctx := withPoolSpawnLSPConfig(context.Background(), []string{"/repo", "packages/api"}, "")
	got := poolSpawnWorkspaceRoots(ctx)
	want := []string{"/repo", "/repo/packages/api"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("pool spawn roots = %#v, want %#v", got, want)
	}
}

func TestPoolSpawnLSPConfigDropsRelativeRootsWithoutPrimaryRoot(t *testing.T) {
	t.Parallel()
	ctx := withPoolSpawnLSPConfig(context.Background(), []string{"packages/api"}, "")
	if got := poolSpawnWorkspaceRoots(ctx); len(got) != 0 {
		t.Fatalf("pool spawn roots = %#v, want empty without trusted primary root", got)
	}
}
