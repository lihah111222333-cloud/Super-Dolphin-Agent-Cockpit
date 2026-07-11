package bootstrap

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// TestClientFireShutdownTracksCallbackWithWaitGroup 验证 OnShutdown 回调受 callbackWG 跟踪。
// Close() 依赖 drainCallbacks 有界等待回调结束，不能与裸 goroutine 形成关闭竞态。
func TestClientFireShutdownTracksCallbackWithWaitGroup(t *testing.T) {
	release := make(chan struct{})
	var fired atomic.Bool
	c := New(Config{
		BinaryName: "test-binary",
		InstanceID: "inst-1",
		OnShutdown: func(mcp.ShutdownRequest) {
			fired.Store(true)
			<-release
		},
	})

	c.fireShutdown(mcp.ShutdownRequest{})

	// handler 未释放时 drainCallbacks 必须阻塞，用短超时证明 WaitGroup 确实接管了回调。
	drainStart := time.Now()
	err := c.drainCallbacks(80 * time.Millisecond)
	if err == nil {
		t.Fatalf("drainCallbacks() returned nil before handler finished; WaitGroup may not be tracking the callback")
	}
	if time.Since(drainStart) < 60*time.Millisecond {
		t.Fatalf("drainCallbacks() returned too early: elapsed=%v", time.Since(drainStart))
	}

	close(release)
	if err := c.drainCallbacks(2 * time.Second); err != nil {
		t.Fatalf("drainCallbacks() after release error = %v", err)
	}
	if !fired.Load() {
		t.Fatalf("OnShutdown handler did not fire")
	}
}

// TestClientSpawnCallbackAfterCloseIsNoop 验证关闭后不会再启动新的回调 goroutine。
// 这样 late callback 不能无限拉长 Close() 的 drain 窗口。
func TestClientSpawnCallbackAfterCloseIsNoop(t *testing.T) {
	var fired atomic.Bool
	c := New(Config{BinaryName: "test-binary", InstanceID: "inst-2"})
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	c.spawnCallback(func() { fired.Store(true) })

	if err := c.drainCallbacks(200 * time.Millisecond); err != nil {
		t.Fatalf("drainCallbacks() on empty group error = %v, want nil", err)
	}
	// 给潜在漏跑 goroutine 一个调度窗口；fired 必须保持 false，说明 closed 分支生效。
	time.Sleep(20 * time.Millisecond)
	if fired.Load() {
		t.Fatalf("spawnCallback ran after closed=true; expected no-op")
	}
}

// TestClientDrainCallbacksTimeoutSurfaced 验证卡住的回调会让 drainCallbacks 返回超时错误。
// Close() 可以记录后继续退出，而不是被应用回调无限阻塞。
func TestClientDrainCallbacksTimeoutSurfaced(t *testing.T) {
	c := New(Config{BinaryName: "test-binary", InstanceID: "inst-3"})
	c.callbackWG.Add(1)
	defer c.callbackWG.Done()

	start := time.Now()
	err := c.drainCallbacks(40 * time.Millisecond)
	if err == nil {
		t.Fatalf("drainCallbacks() with stuck callback = nil err, want timeout error")
	}
	elapsed := time.Since(start)
	if elapsed < 30*time.Millisecond {
		t.Fatalf("drainCallbacks() returned too early: elapsed=%v", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("drainCallbacks() returned too late: elapsed=%v", elapsed)
	}
}
