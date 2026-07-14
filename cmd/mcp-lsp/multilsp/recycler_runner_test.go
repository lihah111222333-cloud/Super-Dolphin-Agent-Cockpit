package multilsp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPoolRecyclerRunContinuesAfterProbeFailure(t *testing.T) {
	client := &p2LifecycleClient{}
	mgr := &manager{workspaces: map[string]*workspaceClient{
		"workspace": {key: "workspace", languageID: "go", client: client},
	}}
	pool := NewManagerPool(mgr, 1)
	mgr.pool = pool
	pool.recycler.interval = time.Millisecond
	pool.recycler.rssProbe = func(Client) (uint64, int, error) {
		return 0, 0, errors.New("probe unavailable")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	t.Cleanup(cancel)
	goroutines.Go(func() { done <- pool.recycler.Run(ctx) })
	deadline := time.Now().Add(time.Second)
	for pool.recycler.HealthSnapshot().ProbeFailuresTotal == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if pool.recycler.HealthSnapshot().ProbeFailuresTotal == 0 {
		t.Fatal("runner did not record probe failure")
	}
	select {
	case err := <-done:
		t.Fatalf("runner exited after probe failure: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error after cancel = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not exit after cancel")
	}
}

// TestPoolRecyclerRunExitsOnCtxCancel 固定 poolRecycler 的 runner 关闭边界。
// Run 必须在传入 ctx 取消后返回且不留下后台 goroutine，关闭责任由根 runner 聚合器统一驱动。
func TestPoolRecyclerRunExitsOnCtxCancel(t *testing.T) {
	pool := NewManagerPool(nil, defaultPoolSize)
	r := pool.RecyclerRunner()
	if r == nil {
		t.Fatalf("RecyclerRunner() returned nil; expected the pool's recycler")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	t.Cleanup(cancel)
	goroutines.Go(func() { done <- r.Run(ctx) })

	// 等待 loop 装载 ticker，避免 cancel 早于 goroutine 进入 select。
	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() returned error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run() did not return within 2s after ctx cancel")
	}
}

// TestNewManagerPoolDoesNotLaunchRecyclerGoroutine 确认构造池时不会自启动 recycler。
// recycler 对象必须存在以支持 TouchShard 记账，但只有 Run(ctx) 会启动循环。
func TestNewManagerPoolDoesNotLaunchRecyclerGoroutine(t *testing.T) {
	pool := NewManagerPool(nil, defaultPoolSize)
	if pool.recycler == nil {
		t.Fatalf("NewManagerPool did not create a recycler")
	}
	// runner 启动前 TouchShard 仍应可用；它只更新记账时间，不依赖循环运行。
	pool.recycler.TouchShard(0)

	// recycler 从未 Run 时 StopAll 也不能阻塞或 panic，生命周期由 ctx 统一控制。
	if err := pool.StopAll(); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
}

// TestPoolRecyclerRunNilReceiverBlocks 固定 nil recycler 的兼容行为。
// Run(ctx) 会阻塞到 ctx.Done() 后返回 nil，根 runner 聚合器无需为 nil receiver 写特殊分支。
func TestPoolRecyclerRunNilReceiverBlocks(t *testing.T) {
	var nilRecycler *poolRecycler
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	t.Cleanup(cancel)
	goroutines.Go(func() { done <- nilRecycler.Run(ctx) })

	select {
	case err := <-done:
		t.Fatalf("Run() on nil receiver returned before ctx cancel: err=%v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() on nil receiver error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run() on nil receiver did not return after ctx cancel")
	}
}
