package mcpcontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

func startSweeperRunnerForTest(t *testing.T, run func(context.Context) error) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("sweeper runner goroutine did not stop")
		}
	})
	return cancel, done
}

// TestSweeperRunnerBlocksUntilContextDone 锁定 runner 的阻塞 actor 行为。
// Run 只有在 ctx 取消后才返回，并把 ctx.Err() 交给 run.Group 作为正常收尾信号。
func TestSweeperRunnerBlocksUntilContextDone(t *testing.T) {
	t.Parallel()
	sweeper := NewSweeperWithOptions(NewRegistry(), nil, SweeperOptions{
		Tick:   10 * time.Millisecond,
		Jitter: time.Millisecond,
	})
	runner := NewSweeperRunner(sweeper)

	cancel, done := startSweeperRunnerForTest(t, runner.Run)

	// 等待 sweep 至少触发一次，确保覆盖的是循环运行路径，而不是冷启动立即返回。
	time.Sleep(25 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Run returned before ctx cancel: err=%v", err)
	default:
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s after ctx cancel")
	}
}

// TestSweeperRunnerPreservesJitterAndStaleTransitions 校验 runner 包装层不改写 sweeper 节奏和 stale 逻辑。
// underlying Sweeper 必须保留原 tick/jitter/timeout 配置，避免 runner 重新创建实例导致运维参数漂移。
func TestSweeperRunnerPreservesJitterAndStaleTransitions(t *testing.T) {
	t.Parallel()
	opts := SweeperOptions{
		Tick:       7 * time.Millisecond,
		Jitter:     2 * time.Millisecond,
		Timeout:    defaultHeartbeatTTL, // 30s
		StaleGrace: defaultStaleGraceTime,
	}
	sweeper := NewSweeperWithOptions(NewRegistry(), nil, opts)
	r, ok := NewSweeperRunner(sweeper).(*SweeperRunner)
	if !ok {
		t.Fatalf("NewSweeperRunner type = %T, want *SweeperRunner", r)
	}
	if r.sweeper != sweeper {
		t.Fatalf("runner wraps a different sweeper instance: got %p, want %p", r.sweeper, sweeper)
	}
	// 这些默认值是生产心跳和清扫节奏的代码真值，漂移必须显式暴露给测试。
	if defaultHeartbeatTTL != 30*time.Second {
		t.Fatalf("defaultHeartbeatTTL drifted to %v, want 30s", defaultHeartbeatTTL)
	}
	if defaultSweepTick != 5*time.Second {
		t.Fatalf("defaultSweepTick drifted to %v, want 5s", defaultSweepTick)
	}
	if defaultStaleGraceTime != 5*time.Second {
		t.Fatalf("defaultStaleGraceTime drifted to %v, want 5s", defaultStaleGraceTime)
	}
}

// TestSweeperHeartbeatUsesCodeTruthTTL30s 固定 heartbeat TTL 的代码真值。
// runner 和文档都应跟随这个常量，不能悄悄缩短租约有效期。
func TestSweeperHeartbeatUsesCodeTruthTTL30s(t *testing.T) {
	t.Parallel()
	if defaultHeartbeatTTL != 30*time.Second {
		t.Fatalf("defaultHeartbeatTTL = %v, want 30s", defaultHeartbeatTTL)
	}
}

// TestRunnerStopDrainsBeforeFinalCleanup 覆盖停止顺序。
// runner ctx 取消后 Run 应及时返回；registry 的 shutdownActiveLeases 属于独立收尾路径，
// 即使 Run 已退出也仍应可执行。
func TestRunnerStopDrainsBeforeFinalCleanup(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	sweeper := NewSweeperWithOptions(reg, nil, SweeperOptions{
		Tick:   5 * time.Millisecond,
		Jitter: time.Millisecond,
	})
	runner := NewSweeperRunner(sweeper)

	cancel, done := startSweeperRunnerForTest(t, runner.Run)

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}

	// Run 返回后仍能关闭活跃租约；空 registry 上调用应成功，证明最终清理路径可独立运行。
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stopCancel()
	if err := reg.shutdownActiveLeases(stopCtx); err != nil {
		t.Fatalf("shutdownActiveLeases() after Run drain = %v", err)
	}
}

// TestSweeperRunnerNilSweeperBlocksUntilDone 覆盖 nil sweeper 的防御路径。
// runner 不应 panic，并且仍然必须遵守 ctx 取消。
func TestSweeperRunnerNilSweeperBlocksUntilDone(t *testing.T) {
	t.Parallel()
	runner := NewSweeperRunner(nil)
	cancel, done := startSweeperRunnerForTest(t, runner.Run)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return with nil sweeper")
	}
}
