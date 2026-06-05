package codexapp

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestServerPoolAcquireSameOwnerDifferentLSPConfigSpawnsDistinctServers(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	rootA := filepath.Join(parent, "repo-a")
	rootB := filepath.Join(parent, "repo-b")
	binaryDir := filepath.Join(parent, "bin-a")
	spawnCalls := atomic.Int32{}
	p, _ := newPoolForTest(t, newCountingFakeSpawner(&spawnCalls), PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	ctxA := withPoolSpawnLSPConfig(context.Background(), []string{rootA}, binaryDir)
	ctxB := withPoolSpawnLSPConfig(context.Background(), []string{rootB}, filepath.Join(parent, "bin-b"))
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

func TestServerPoolAcquirePassesModelProviderToSpawner(t *testing.T) {
	t.Parallel()
	var gotModelProvider atomic.Value
	spawner := func(_ context.Context, home, modelProvider string) (SpawnedServer, error) {
		gotModelProvider.Store(modelProvider)
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	id.ModelProvider = "openai"
	_, release, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer release()

	if got := gotModelProvider.Load(); got != "openai" {
		t.Fatalf("spawner model provider = %q, want openai", got)
	}
}

func TestPoolSpawnPolicySignatureIncludesLSPConfig(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	rootA := filepath.Join(parent, "repo-a")
	rootB := filepath.Join(parent, "repo-b")
	binaryDir := filepath.Join(parent, "bin-a")
	ctx := withPoolSpawnLSPConfig(context.Background(), []string{rootA, rootB}, binaryDir)
	got := poolSpawnPolicySignature(ctx)
	for _, want := range []string{rootA, rootB, binaryDir} {
		if !strings.Contains(got, want) {
			t.Fatalf("policy signature %q missing %q", got, want)
		}
	}
}

func TestPoolSpawnLSPConfigResolvesRelativeRootsAgainstPrimaryRoot(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "repo")
	ctx := withPoolSpawnLSPConfig(context.Background(), []string{repo, "packages/api"}, "")
	got := poolSpawnWorkspaceRoots(ctx)
	want := []string{repo, filepath.Join(repo, "packages", "api")}
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
