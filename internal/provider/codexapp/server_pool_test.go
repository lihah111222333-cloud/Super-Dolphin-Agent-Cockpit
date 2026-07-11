package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
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

func newCountingFakeSpawner(calls *atomic.Int32) Spawner {
	return func(_ context.Context, home, _ string) (SpawnedServer, error) {
		calls.Add(1)
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
}

func (f *fakeServer) ServerURL() string { return f.url }
func (f *fakeServer) Close(ctx context.Context) error {
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
// assert idle eviction + backoff semantics without sleeping.
func newPoolForTest(t *testing.T, spawner Spawner, cfg PoolConfig) (*ServerPool, *time.Time) {
	t.Helper()
	p := NewServerPool(slog.Default(), spawner, cfg)
	now := time.Unix(1_700_000_000, 0).UTC()
	p.now = func() time.Time { return now }
	return p, &now
}

func poolKeyFor(t *testing.T, identity providershared.CodexIdentity, owner string) poolEntryKey {
	t.Helper()
	home, key, provider, err := normalizePoolIdentity(identity)
	if err != nil {
		t.Fatalf("normalizePoolIdentity: %v", err)
	}
	return poolEntryKey{home: home, instanceKey: key, modelProvider: provider, ownerKey: owner}
}

func entryForKey(t *testing.T, p *ServerPool, key poolEntryKey) *poolEntry {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	entry := p.entries[key]
	if entry == nil {
		t.Fatalf("entry %#v missing", key)
	}
	return entry
}

func waitForFakeClosed(t *testing.T, server *fakeServer) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.closed.Load() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for fake server close")
}

func TestServerPoolAcquireHappyPathSpawnAndRelease(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := newCountingFakeSpawner(&spawnCalls)
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	srv, release, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if srv == nil {
		t.Fatal("Acquire returned nil server")
	}
	if spawnCalls.Load() != 1 {
		t.Fatalf("spawn calls = %d, want 1", spawnCalls.Load())
	}
	entry := entryForKey(t, p, poolKeyFor(t, id, "agent-1"))
	if entry.server != srv {
		t.Fatal("stored server mismatch")
	}
	if entry.refCount != 1 {
		t.Fatalf("refCount = %d, want 1", entry.refCount)
	}

	release()
	if p.Size() != 0 {
		t.Fatalf("pool size after final release = %d, want 0", p.Size())
	}
	fake, ok := srv.(*fakeServer)
	if !ok {
		t.Fatalf("server type = %T, want *fakeServer", srv)
	}
	waitForFakeClosed(t, fake)
}

func TestServerPoolAcquireAliveCacheHitReusesServer(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := newCountingFakeSpawner(&spawnCalls)
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	srv1, rel1, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	srv2, rel2, err := p.Acquire(context.Background(), id, "agent-1")
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

func TestServerPoolAcquireSameIdentityDifferentOwnersIsolated(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := newCountingFakeSpawner(&spawnCalls)
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	srv1, rel1, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	srv2, rel2, err := p.Acquire(context.Background(), id, "agent-2")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if srv1 == srv2 {
		t.Fatal("different owners must not share the same app-server")
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls.Load())
	}
	rel1()
	if p.Size() != 1 {
		t.Fatalf("pool size after releasing one owner = %d, want 1", p.Size())
	}
	rel2()
	if p.Size() != 0 {
		t.Fatalf("pool size after releasing both owners = %d, want 0", p.Size())
	}
}

func TestServerPoolAcquireSameHomeKeyDifferentModelProviderIsolated(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := newCountingFakeSpawner(&spawnCalls)
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	first, release, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer release()
	if first == nil {
		t.Fatal("first Acquire returned nil server")
	}

	conflicting := id
	conflicting.ModelProvider = id.ModelProvider + "-other"
	server, release2, err := p.Acquire(context.Background(), conflicting, "agent-1")
	if err != nil {
		t.Fatalf("conflicting Acquire: %v", err)
	}
	if server != nil {
		if server == first {
			t.Fatal("different model providers must not share the same server")
		}
	} else {
		t.Fatal("conflicting identity returned nil server")
	}
	release2()
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2 isolated servers", spawnCalls.Load())
	}
}

func TestServerPoolAcquireDeadCacheRespawnsServer(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	var current *fakeServer
	spawner := func(_ context.Context, home, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		current = newFakeServer("ws://" + filepath.Base(home))
		return current, nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	srv1, rel1, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	rel1()
	current.alive.Store(false)

	srv2, rel2, err := p.Acquire(context.Background(), id, "agent-1")
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
	p, _ := newPoolForTest(t, func(_ context.Context, home, _ string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}, PoolConfig{})

	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	server, release, err := p.Acquire(context.Background(), identityFor(t, "glm"), "agent-1")
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
	p, _ := newPoolForTest(t, nil, PoolConfig{})

	server, release, err := p.Acquire(context.Background(), identityFor(t, "glm"), "agent-1")
	if !errors.Is(err, ErrInvalidIdentity) {
		t.Fatalf("want ErrInvalidIdentity, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "pool has no spawner") {
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
	spawner := func(_ context.Context, _, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return newFakeServer("ws://unexpected"), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := providershared.CodexIdentity{
		Home:          filepath.Join(t.TempDir(), "missing-home"),
		InstanceKey:   "glm",
		ModelProvider: "model-provider-glm",
	}
	_, _, _, wantErr := normalizePoolIdentity(id)
	server, release, err := p.Acquire(context.Background(), id, "agent-1")
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

func TestServerPoolAcquireDoesNotApplyCapacityLimit(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	spawner := newCountingFakeSpawner(&spawnCalls)
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id1 := identityFor(t, "glm")
	id2 := identityFor(t, "qwen")
	srv1, rel1, err := p.Acquire(context.Background(), id1, "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	srv2, rel2, err := p.Acquire(context.Background(), id2, "agent-2")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	defer rel2()
	if srv2 == nil {
		t.Fatal("second Acquire returned nil server")
	}
	if srv1 == srv2 {
		t.Fatal("separate identities should spawn different servers")
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls.Load())
	}
	if p.Size() != 2 {
		t.Fatalf("pool size = %d, want 2 busy entries", p.Size())
	}
	rel1()
}

func TestServerPoolAcquireAllowsMultipleBusyEntries(t *testing.T) {
	t.Parallel()
	spawner := func(_ context.Context, home, _ string) (SpawnedServer, error) {
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	_, rel1, err := p.Acquire(context.Background(), identityFor(t, "glm"), "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	server, rel2, err := p.Acquire(context.Background(), identityFor(t, "qwen"), "agent-2")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if server == nil {
		t.Fatal("second Acquire returned nil server")
	}
	if p.Size() != 2 {
		t.Fatalf("pool size = %d, want 2", p.Size())
	}
	rel2()
	rel1()
}

func TestServerPoolAcquireSpawnErrorCreatesBackoffSlot(t *testing.T) {
	t.Parallel()
	spawnErr := errors.New("port taken")
	p, nowRef := newPoolForTest(t, func(_ context.Context, _, _ string) (SpawnedServer, error) {
		return nil, spawnErr
	}, PoolConfig{SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	server, release, err := p.Acquire(context.Background(), id, "agent-1")
	if err == nil {
		t.Fatal("Acquire unexpectedly succeeded")
	}
	if server != nil {
		t.Fatalf("spawn error returned non-nil server: %#v", server)
	}
	release()

	entry := entryForKey(t, p, poolKeyFor(t, id, "agent-1"))
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
	p, nowRef := newPoolForTest(t, func(_ context.Context, _, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return nil, spawnErr
	}, PoolConfig{SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	_, _, err := p.Acquire(context.Background(), id, "agent-1")
	if err == nil {
		t.Fatal("first Acquire unexpectedly succeeded")
	}
	firstEntry := entryForKey(t, p, poolKeyFor(t, id, "agent-1"))
	*nowRef = nowRef.Add(2 * time.Minute)

	_, _, err = p.Acquire(context.Background(), id, "agent-1")
	if err == nil {
		t.Fatal("second Acquire unexpectedly succeeded")
	}
	secondEntry := entryForKey(t, p, poolKeyFor(t, id, "agent-1"))
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
	p, _ := newPoolForTest(t, func(_ context.Context, _, _ string) (SpawnedServer, error) {
		spawnCalls.Add(1)
		return nil, spawnErr
	}, PoolConfig{SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	_, _, err := p.Acquire(context.Background(), id, "agent-1")
	if err == nil {
		t.Fatal("first Acquire unexpectedly succeeded")
	}
	server, release, err := p.Acquire(context.Background(), id, "agent-1")
	if !errors.Is(err, ErrSpawnBackoff) {
		t.Fatalf("want ErrSpawnBackoff, got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), spawnErr.Error()) {
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
	p, nowRef := newPoolForTest(t, func(_ context.Context, home, _ string) (SpawnedServer, error) {
		if spawnCalls.Add(1) == 1 {
			return nil, spawnErr
		}
		recovered = newFakeServer("ws://" + filepath.Base(home))
		return recovered, nil
	}, PoolConfig{SpawnBackoff: time.Minute})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	_, _, err := p.Acquire(context.Background(), id, "agent-1")
	if err == nil {
		t.Fatal("first Acquire unexpectedly succeeded")
	}
	*nowRef = nowRef.Add(2 * time.Minute)

	server, release, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("second Acquire after backoff: %v", err)
	}
	defer release()
	if server != recovered {
		t.Fatal("backoff expiry should allow a fresh spawn")
	}
	entry := entryForKey(t, p, poolKeyFor(t, id, "agent-1"))
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
	spawnErr := errors.New("port taken")
	p, nowRef := newPoolForTest(t, func(context.Context, string, string) (SpawnedServer, error) {
		return nil, spawnErr
	}, PoolConfig{IdleTimeout: 10 * time.Minute, SpawnBackoff: time.Hour})
	defer p.Close(context.Background())

	_, _, err := p.Acquire(context.Background(), identityFor(t, "glm"), "agent-1")
	if err == nil {
		t.Fatal("Acquire unexpectedly succeeded")
	}
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
	spawner := func(_ context.Context, home, _ string) (SpawnedServer, error) {
		server := newFakeServer("ws://" + filepath.Base(home))
		created = append(created, server)
		return server, nil
	}
	p, _ := newPoolForTest(t, spawner, PoolConfig{})

	_, _, _ = p.Acquire(context.Background(), identityFor(t, "glm"), "agent-1")
	_, _, _ = p.Acquire(context.Background(), identityFor(t, "qwen"), "agent-2")
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

func TestServerPoolAcquireSameHomeDifferentInstanceKeyIsolated(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	p, _ := newPoolForTest(t, newCountingFakeSpawner(&spawnCalls), PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	first, release, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	defer release()
	id.InstanceKey = "qwen"
	server, rel2, err := p.Acquire(context.Background(), id, "agent-1")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if server == nil {
		t.Fatal("server = nil, want isolated server")
	}
	if server == first {
		t.Fatal("different instance keys must not share the same server")
	}
	if spawnCalls.Load() != 2 {
		t.Fatalf("spawn calls = %d, want 2", spawnCalls.Load())
	}
	rel2()
}

func TestServerPoolSpawnerRunsOutsideMutex(t *testing.T) {
	t.Parallel()
	var p *ServerPool
	spawnerEntered := make(chan struct{})
	spawner := func(_ context.Context, home, _ string) (SpawnedServer, error) {
		close(spawnerEntered)
		_ = p.Size()
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
	p, _ = newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		_, release, err := p.Acquire(context.Background(), identityFor(t, "glm"), "agent-1")
		if release != nil {
			release()
		}
		done <- err
	})
	select {
	case <-spawnerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("spawner did not start")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire deadlocked; spawner likely ran under pool mutex")
	}
}
