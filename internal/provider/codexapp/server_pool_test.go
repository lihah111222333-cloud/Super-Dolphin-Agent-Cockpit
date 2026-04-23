package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// fakeServer is the ServerPool test double. It records Close calls so
// tests can assert lifecycle precisely.
type fakeServer struct {
	url       string
	closed    atomic.Bool
	closeErr  error
	alive     atomic.Bool
	closeHook func()
}

func newFakeServer(url string) *fakeServer {
	f := &fakeServer{url: url}
	f.alive.Store(true)
	return f
}

func (f *fakeServer) ServerURL() string { return f.url }
func (f *fakeServer) Close(_ context.Context) error {
	if f.closeHook != nil {
		f.closeHook()
	}
	f.closed.Store(true)
	f.alive.Store(false)
	return f.closeErr
}
func (f *fakeServer) Alive() bool { return f.alive.Load() }

// identityFor returns a CodexIdentity bound to a fresh temp directory.
// The pool canonicalizes the home so every test works against a real
// realpath + EvalSymlinks.
func identityFor(t *testing.T, key string) providershared.CodexIdentity {
	t.Helper()
	dir := t.TempDir()
	// Ensure the directory exists as a real path (t.TempDir returns an
	// already-existing path on all supported platforms).
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("temp dir missing: %v", err)
	}
	return providershared.CodexIdentity{
		Home:          dir,
		InstanceKey:   key,
		ModelProvider: "model-provider-" + key,
	}
}

// newPoolForTest returns a pool whose clock is pinned so tests can
// assert LRU + backoff semantics without sleeping.
func newPoolForTest(t *testing.T, spawner Spawner, cfg PoolConfig) (*ServerPool, *time.Time) {
	t.Helper()
	p := NewServerPool(slog.Default(), spawner, cfg)
	now := time.Unix(1_700_000_000, 0).UTC()
	p.now = func() time.Time { return now }
	return p, &now
}

func TestPoolAcquireSpawnsOncePerHome(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	srv1, rel1, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("first Acquire error = %v", err)
	}
	defer rel1()
	srv2, rel2, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("second Acquire error = %v", err)
	}
	defer rel2()
	if srv1 != srv2 {
		t.Fatalf("same identity should return same server")
	}
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawn called %d times, want 1", spawnCalls.Load())
	}
}

func TestPoolSeparateIdentitiesGetSeparateServers(t *testing.T) {
	t.Parallel()
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})
	defer p.Close(context.Background())

	s1, rel1, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if err != nil {
		t.Fatalf("glm: %v", err)
	}
	defer rel1()
	s2, rel2, err := p.Acquire(context.Background(), identityFor(t, "qwen"))
	if err != nil {
		t.Fatalf("qwen: %v", err)
	}
	defer rel2()
	if s1 == s2 {
		t.Fatal("different identities must yield different servers")
	}
	if p.Size() != 2 {
		t.Fatalf("pool size = %d, want 2", p.Size())
	}
}

func TestPoolExhaustedReturnsErrWhenAllBusy(t *testing.T) {
	t.Parallel()
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 1})
	defer p.Close(context.Background())

	_, rel, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// Do NOT release; second acquire for a different home should fail
	// because the sole slot is still refcounted.
	_, _, err = p.Acquire(context.Background(), identityFor(t, "qwen"))
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
	rel()
	// After release the slot is idle, so a new identity evicts + spawns.
	_, relB, err := p.Acquire(context.Background(), identityFor(t, "qwen"))
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	relB()
}

func TestPoolSpawnFailureRecordsBackoff(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := func(_ context.Context, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return nil, errors.New("port taken")
	}
	p, nowRef := newPoolForTest(t, spawner, PoolConfig{Capacity: 4, SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	_, _, err := p.Acquire(context.Background(), id)
	if err == nil || !contains(err.Error(), "port taken") {
		t.Fatalf("want spawn error, got %v", err)
	}
	// Within the backoff window the second Acquire must not call
	// spawn again.
	_, _, err2 := p.Acquire(context.Background(), id)
	if !errors.Is(err2, ErrSpawnBackoff) {
		t.Fatalf("want ErrSpawnBackoff, got %v", err2)
	}
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawner called %d times during backoff, want 1", spawnCalls.Load())
	}
	// Advance past the backoff; next Acquire triggers a fresh spawn
	// attempt (still failing here, but the counter bumps).
	*nowRef = nowRef.Add(2 * time.Minute)
	_, _, _ = p.Acquire(context.Background(), id)
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawner calls after backoff = %d, want 2", spawnCalls.Load())
	}
}

func TestPoolRespawnsDeadServer(t *testing.T) {
	t.Parallel()
	var current *fakeServer
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		current = newFakeServer("ws://" + filepath.Base(home))
		return current, nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})
	defer p.Close(context.Background())
	id := identityFor(t, "glm")
	s1, rel1, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	rel1()
	current.alive.Store(false) // simulate crash
	s2, rel2, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	defer rel2()
	if s1 == s2 {
		t.Fatal("dead server must be replaced")
	}
}

func TestPoolEvictIdleRemovesStaleEntries(t *testing.T) {
	t.Parallel()
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, nowRef := newPoolForTest(t, spawner, PoolConfig{Capacity: 4, IdleTimeout: 10 * time.Minute})
	defer p.Close(context.Background())
	_, rel, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	rel()
	if evicted := p.EvictIdle(); evicted != 0 {
		t.Fatalf("before idle window: evicted %d, want 0", evicted)
	}
	*nowRef = nowRef.Add(11 * time.Minute)
	if evicted := p.EvictIdle(); evicted != 1 {
		t.Fatalf("after idle window: evicted %d, want 1", evicted)
	}
	if p.Size() != 0 {
		t.Fatalf("pool should be empty after EvictIdle; size=%d", p.Size())
	}
}

func TestPoolCloseTearsEverythingDown(t *testing.T) {
	t.Parallel()
	var created []*fakeServer
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		s := newFakeServer("ws://" + filepath.Base(home))
		created = append(created, s)
		return s, nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})
	_, rel1, _ := p.Acquire(context.Background(), identityFor(t, "glm"))
	_, rel2, _ := p.Acquire(context.Background(), identityFor(t, "qwen"))
	rel1()
	rel2()
	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i, s := range created {
		if !s.closed.Load() {
			t.Fatalf("server #%d was not closed on pool shutdown", i)
		}
	}
	// Double Close is a no-op.
	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("double Close: %v", err)
	}
	// Subsequent Acquire surfaces ErrPoolClosed.
	_, _, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("want ErrPoolClosed after Close, got %v", err)
	}
}

func TestPoolRejectsEmptyIdentity(t *testing.T) {
	t.Parallel()
	p, _ := newPoolForTest(t, nil, PoolConfig{})
	_, _, err := p.Acquire(context.Background(), providershared.CodexIdentity{})
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("want ErrInvalidIdentity for empty home, got %v", err)
	}
}

func TestPoolRejectsNilSpawner(t *testing.T) {
	t.Parallel()
	p, _ := newPoolForTest(t, nil, PoolConfig{})
	_, _, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("want ErrInvalidIdentity for nil spawner, got %v", err)
	}
}

// contains is a small helper mirrored from other tests in this tree;
// the stdlib strings import keeps this file's top already importing
// strings-free helpers deliberately.
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
