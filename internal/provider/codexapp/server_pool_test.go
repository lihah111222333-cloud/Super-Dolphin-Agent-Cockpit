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

func canonicalHome(t *testing.T, identity providershared.CodexIdentity) string {
	t.Helper()
	home, _, err := normalizePoolIdentity(identity)
	if err != nil {
		t.Fatalf("normalizePoolIdentity: %v", err)
	}
	return home
}

func entryForHome(t *testing.T, p *ServerPool, home string) *poolEntry {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[home]
	if entry == nil {
		t.Fatalf("entry %q missing", home)
	}
	return entry
}

func waitForClose(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async close")
	}
}

func TestServerPoolAcquireHappyPathSpawnAndRelease(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	home := canonicalHome(t, id)
	srv, release, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if srv == nil {
		t.Fatal("Acquire returned nil server")
	}
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawnCalls.Load())
	}
	entry := entryForHome(t, p, home)
	if entry.server != srv {
		t.Fatal("stored server mismatch")
	}
	if entry.refCount != 1 {
		t.Fatalf("refCount = %d, want 1", entry.refCount)
	}

	release()
	if entryForHome(t, p, home).refCount != 0 {
		t.Fatalf("refCount after release = %d, want 0", entryForHome(t, p, home).refCount)
	}
}

func TestServerPoolAcquireAliveCacheHitReusesServer(t *testing.T) {
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
		t.Fatalf("first Acquire: %v", err)
	}
	srv2, rel2, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if srv1 != srv2 {
		t.Fatal("alive cache hit should reuse the same server")
	}
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawnCalls.Load())
	}
	defer rel2()
	defer rel1()
}

func TestServerPoolAcquireDeadCacheRespawnsServer(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	var current *fakeServer
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		current = newFakeServer("ws://" + filepath.Base(home))
		return current, nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	srv1, rel1, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	rel1()
	current.alive.Store(false)

	srv2, rel2, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer rel2()
	if srv1 == srv2 {
		t.Fatal("dead cached server must be respawned")
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls.Load())
	}
}

func TestServerPoolAcquireClosedPoolReturnsNoopRelease(t *testing.T) {
	t.Parallel()
	p, _ := newPoolForTest(t, func(_ context.Context, home string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}, PoolConfig{Capacity: 4})

	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	server, release, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("want ErrPoolClosed, got %v", err)
	}
	if server != nil {
		t.Fatalf("closed pool returned non-nil server: %#v", server)
	}
	release()
	release()
}

func TestServerPoolAcquireNilSpawnerReturnsInvalidIdentity(t *testing.T) {
	t.Parallel()
	p, _ := newPoolForTest(t, nil, PoolConfig{Capacity: 4})

	server, release, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("want ErrInvalidIdentity, got %v", err)
	}
	if err == nil || !contains(err.Error(), "pool has no spawner") {
		t.Fatalf("want nil spawner detail, got %v", err)
	}
	if server != nil {
		t.Fatalf("nil spawner returned non-nil server: %#v", server)
	}
	release()
}

func TestServerPoolAcquireNormalizeIdentityError(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := func(_ context.Context, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://unexpected"), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})
	defer p.Close(context.Background())

	id := providershared.CodexIdentity{
		Home:          filepath.Join(t.TempDir(), "missing-home"),
		InstanceKey:   "glm",
		ModelProvider: "model-provider-glm",
	}
	_, _, wantErr := normalizePoolIdentity(id)
	server, release, err := p.Acquire(context.Background(), id)
	if err == nil {
		t.Fatal("Acquire unexpectedly succeeded")
	}
	if wantErr == nil || err.Error() != wantErr.Error() {
		t.Fatalf("want normalize error %q, got %v", wantErr, err)
	}
	if spawnCalls.Load() != 0 {
		t.Fatalf("spawner called %d times on normalize error, want 0", spawnCalls.Load())
	}
	if server != nil {
		t.Fatalf("normalize error returned non-nil server: %#v", server)
	}
	release()
}

func TestServerPoolAcquireCapacityFullEvictsLRU(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	firstClosed := make(chan struct{})
	firstCloseOnce := atomic.Bool{}
	var firstServer SpawnedServer
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		call := spawnCalls.Add(1)
		server := newFakeServer("ws://" + filepath.Base(home))
		if call == 1 {
			server.closeHook = func() {
				if firstCloseOnce.CompareAndSwap(false, true) {
					close(firstClosed)
				}
			}
			firstServer = server
		}
		return server, nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 1})
	defer p.Close(context.Background())

	id1 := identityFor(t, "glm")
	id2 := identityFor(t, "qwen")
	srv1, rel1, err := p.Acquire(context.Background(), id1)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	rel1()

	srv2, rel2, err := p.Acquire(context.Background(), id2)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer rel2()
	if srv2 == nil {
		t.Fatal("eviction path returned nil server")
	}
	if srv1 == srv2 {
		t.Fatal("eviction path should spawn a different server")
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls.Load())
	}
	if p.Size() != 1 {
		t.Fatalf("pool size = %d, want 1", p.Size())
	}
	waitForClose(t, firstClosed)
	if firstServer == nil {
		t.Fatal("first server was never created")
	}
}

func TestServerPoolAcquireCapacityFullAllBusyReturnsErrPoolExhausted(t *testing.T) {
	t.Parallel()
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 1})
	defer p.Close(context.Background())

	_, rel1, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	server, rel2, err := p.Acquire(context.Background(), identityFor(t, "qwen"))
	if !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("want ErrPoolExhausted, got %v", err)
	}
	if server != nil {
		t.Fatalf("ErrPoolExhausted returned non-nil server: %#v", server)
	}
	rel2()
	rel1()
}

func TestServerPoolAcquireSpawnErrorCreatesBackoffSlot(t *testing.T) {
	t.Parallel()
	spawnErr := errors.New("port taken")
	p, nowRef := newPoolForTest(t, func(_ context.Context, _ string) (SpawnedServer, error) {
		return nil, spawnErr
	}, PoolConfig{Capacity: 4, SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	home := canonicalHome(t, id)
	server, release, err := p.Acquire(context.Background(), id)
	if err == nil {
		t.Fatal("Acquire unexpectedly succeeded")
	}
	if server != nil {
		t.Fatalf("spawn error returned non-nil server: %#v", server)
	}
	release()

	entry := entryForHome(t, p, home)
	if entry.server != nil {
		t.Fatal("spawn error should not store a live server")
	}
	if !errors.Is(entry.spawnErr, spawnErr) {
		t.Fatalf("entry.spawnErr = %v, want %v", entry.spawnErr, spawnErr)
	}
	if got, want := entry.backoffUntil, nowRef.Add(time.Minute); !got.Equal(want) {
		t.Fatalf("backoffUntil = %v, want %v", got, want)
	}
	if !entry.lastUsed.Equal(*nowRef) {
		t.Fatalf("lastUsed = %v, want %v", entry.lastUsed, *nowRef)
	}
}

func TestServerPoolAcquireSpawnErrorPreservesExistingSlot(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawnErr := errors.New("port taken")
	p, nowRef := newPoolForTest(t, func(_ context.Context, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return nil, spawnErr
	}, PoolConfig{Capacity: 4, SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	home := canonicalHome(t, id)
	_, _, err := p.Acquire(context.Background(), id)
	if err == nil {
		t.Fatal("first Acquire unexpectedly succeeded")
	}
	firstEntry := entryForHome(t, p, home)
	*nowRef = nowRef.Add(2 * time.Minute)

	_, _, err = p.Acquire(context.Background(), id)
	if err == nil {
		t.Fatal("second Acquire unexpectedly succeeded")
	}
	secondEntry := entryForHome(t, p, home)
	if firstEntry != secondEntry {
		t.Fatal("spawn error on existing entry should preserve the slot")
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls.Load())
	}
}

func TestServerPoolAcquireBackoffActiveReturnsWrappedError(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawnErr := errors.New("port taken")
	p, _ := newPoolForTest(t, func(_ context.Context, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return nil, spawnErr
	}, PoolConfig{Capacity: 4, SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	_, _, err := p.Acquire(context.Background(), id)
	if err == nil {
		t.Fatal("first Acquire unexpectedly succeeded")
	}
	server, release, err := p.Acquire(context.Background(), id)
	if !errors.Is(err, ErrSpawnBackoff) {
		t.Fatalf("want ErrSpawnBackoff, got %v", err)
	}
	if err == nil || !contains(err.Error(), spawnErr.Error()) {
		t.Fatalf("want cached spawn error in backoff message, got %v", err)
	}
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawn calls during backoff = %d, want 1", spawnCalls.Load())
	}
	if server != nil {
		t.Fatalf("backoff returned non-nil server: %#v", server)
	}
	release()
}

func TestServerPoolAcquireBackoffExpiredRetriesSpawn(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawnErr := errors.New("port taken")
	var recovered SpawnedServer
	p, nowRef := newPoolForTest(t, func(_ context.Context, home string) (SpawnedServer, error) {
		if spawnCalls.Add(1) == 1 {
			return nil, spawnErr
		}
		recovered = newFakeServer("ws://" + filepath.Base(home))
		return recovered, nil
	}, PoolConfig{Capacity: 4, SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	home := canonicalHome(t, id)
	_, _, err := p.Acquire(context.Background(), id)
	if err == nil {
		t.Fatal("first Acquire unexpectedly succeeded")
	}
	*nowRef = nowRef.Add(2 * time.Minute)

	server, release, err := p.Acquire(context.Background(), id)
	if err != nil {
		t.Fatalf("second Acquire after backoff: %v", err)
	}
	defer release()
	if server != recovered {
		t.Fatal("backoff expiry should allow a fresh spawn")
	}
	entry := entryForHome(t, p, home)
	if entry.spawnErr != nil {
		t.Fatalf("spawnErr = %v, want nil", entry.spawnErr)
	}
	if !entry.backoffUntil.IsZero() {
		t.Fatalf("backoffUntil = %v, want zero", entry.backoffUntil)
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls.Load())
	}
}

func TestServerPoolEvictIdleRemovesStaleEntries(t *testing.T) {
	t.Parallel()
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, nowRef := newPoolForTest(t, spawner, PoolConfig{Capacity: 4, IdleTimeout: 10 * time.Minute})
	defer p.Close(context.Background())

	_, rel, err := p.Acquire(context.Background(), identityFor(t, "glm"))
	if err != nil {
		t.Fatalf("Acquire: %v", err)
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
		t.Fatalf("pool size = %d, want 0", p.Size())
	}
}

func TestServerPoolCloseTearsEverythingDown(t *testing.T) {
	t.Parallel()
	var created []*fakeServer
	spawner := func(_ context.Context, home string) (SpawnedServer, error) {
		server := newFakeServer("ws://" + filepath.Base(home))
		created = append(created, server)
		return server, nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{Capacity: 4})

	_, rel1, _ := p.Acquire(context.Background(), identityFor(t, "glm"))
	_, rel2, _ := p.Acquire(context.Background(), identityFor(t, "qwen"))
	rel1()
	rel2()
	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i, server := range created {
		if !server.closed.Load() {
			t.Fatalf("server #%d was not closed", i)
		}
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("double Close: %v", err)
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
