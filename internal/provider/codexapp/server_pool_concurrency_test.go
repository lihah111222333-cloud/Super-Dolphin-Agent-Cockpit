package codexapp

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

type poolAcquireResult struct {
	server  SpawnedServer
	release func()
	err     error
}

// blockingPoolSpawnerForTest 让首个 spawn 挂起，方便测试同一身份的并发 Acquire 是否共享启动结果。
func blockingPoolSpawnerForTest(calls *atomic.Int32, firstStarted chan<- struct{}, release <-chan struct{}) Spawner {
	return func(ctx context.Context, home, _ string) (SpawnedServer, error) {
		if calls.Add(1) == 1 {
			close(firstStarted)
		}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return newFakeServer("ws://" + filepath.Base(home)), nil
	}
}

// acquirePoolAsync 在后台调用 Acquire，并把 server、release 和错误整体送回测试线程。
func acquirePoolAsync(p *ServerPool, id providershared.CodexIdentity, owner string) <-chan poolAcquireResult {
	result := make(chan poolAcquireResult, 1)
	go func() {
		server, release, err := p.Acquire(context.Background(), id, owner)
		result <- poolAcquireResult{server: server, release: release, err: err}
	}()
	return result
}

// waitForFirstSpawn 确认测试已经进入第一个真实 spawn，再启动第二个并发 Acquire。
func waitForFirstSpawn(t *testing.T, firstStarted <-chan struct{}) {
	t.Helper()
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first spawn did not start")
	}
}

// readAcquireResult 读取后台 Acquire 结果，避免测试在异常路径永久阻塞。
func readAcquireResult(t *testing.T, result <-chan poolAcquireResult, label string) poolAcquireResult {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("%s Acquire: %v", label, got.err)
		}
		return got
	case <-time.After(time.Second):
		t.Fatalf("%s Acquire did not return", label)
		return poolAcquireResult{}
	}
}

// assertSharedAcquireResult 校验并发 Acquire 共用同一个 server，并把引用计数提升到 2。
func assertSharedAcquireResult(t *testing.T, p *ServerPool, id providershared.CodexIdentity, first, second poolAcquireResult, calls *atomic.Int32) {
	t.Helper()
	if calls.Load() != 1 {
		t.Fatalf("spawn calls = %d, want 1 shared in-flight spawn", calls.Load())
	}
	if first.server == nil || first.server != second.server {
		t.Fatalf("concurrent acquires returned servers %#v and %#v, want same non-nil server", first.server, second.server)
	}
	entry := entryForKey(t, p, poolKeyFor(t, id, "agent-1"))
	if entry.refCount != 2 {
		t.Fatalf("refCount = %d, want 2 concurrent owners", entry.refCount)
	}
}

// releaseSharedAcquireResult 验证两个 release 依次释放引用，最后一个才关闭并移除池条目。
func releaseSharedAcquireResult(t *testing.T, p *ServerPool, first, second poolAcquireResult) {
	t.Helper()
	first.release()
	if p.Size() != 1 {
		t.Fatalf("pool size after first release = %d, want 1", p.Size())
	}
	second.release()
	if p.Size() != 0 {
		t.Fatalf("pool size after final release = %d, want 0", p.Size())
	}
}

func TestServerPoolConcurrentAcquireSharesInFlightSpawn(t *testing.T) {
	t.Parallel()
	spawnCalls := atomic.Int32{}
	firstSpawnStarted := make(chan struct{})
	releaseSpawn := make(chan struct{})
	spawner := blockingPoolSpawnerForTest(&spawnCalls, firstSpawnStarted, releaseSpawn)
	p, _ := newPoolForTest(t, spawner, PoolConfig{})
	defer p.Close(context.Background())

	id := identityFor(t, "glm")
	first := acquirePoolAsync(p, id, "agent-1")
	waitForFirstSpawn(t, firstSpawnStarted)
	second := acquirePoolAsync(p, id, "agent-1")
	time.Sleep(50 * time.Millisecond)
	close(releaseSpawn)

	firstResult := readAcquireResult(t, first, "first")
	secondResult := readAcquireResult(t, second, "second")
	assertSharedAcquireResult(t, p, id, firstResult, secondResult, &spawnCalls)
	releaseSharedAcquireResult(t, p, firstResult, secondResult)
}
