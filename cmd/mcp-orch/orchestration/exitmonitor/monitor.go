package exitmonitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/processctl"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// Monitor 是本地编排进程 cmd.Wait 的唯一拥有者。
// 它用 (agentID, launchSeq) 做 channel enqueue 围栏，让本地 Wait 和远端合成退出事件不会重复入队。
// 该围栏只约束当前进程内的 channel 入队，不等同于 consumer 的 durable ACK。
// 入队后、consumer 处理前发生进程崩溃仍可能丢失内存事件，持久化对账不属于本监视器。
//
// 职责：
//   - Arm 为本地 cmd 启动 Wait goroutine，并用 WaitGroup 让 Drain 能在关闭时等待收尾。
//   - Emit 供远端 launcher 停止成功但没有本地 cmd 的路径合成退出事件。
//   - ExitEvents 暴露只读事件流，runnerActor 负责消费。
//   - Drain 关闭新 Arm 入口，并按调用方 ctx 等待已经在路上的 Wait goroutine。
type Monitor struct {
	logger *slog.Logger
	events chan Event

	mu     sync.Mutex
	wg     sync.WaitGroup
	closed bool
	fired  map[string]struct{}
	// publishing 记录已经抢占、但尚未成功写入 events channel 的 fence。
	publishing map[string]struct{}

	// publishBlockTimeout 限制事件通道阻塞的最长等待时间。
	// actor 卡住时宁可记录丢弃，也不能永久占住所有 Wait goroutine。
	publishBlockTimeout time.Duration
}

// Target 描述一次本地进程监听的身份围栏和进程句柄。
type Target struct {
	AgentID   string
	LaunchSeq uint64
	Cmd       *exec.Cmd
}

// Event 是 Monitor 向 runnerActor 发出的进程退出事件。
type Event struct {
	AgentID   string
	LaunchSeq uint64
	Err       error
}

// New 创建进程退出监视器。
func New(logger *slog.Logger) *Monitor {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Monitor{
		logger:              logger,
		events:              make(chan Event, 32),
		fired:               make(map[string]struct{}),
		publishing:          make(map[string]struct{}),
		publishBlockTimeout: 5 * time.Second,
	}
}

// Arm 为 target 启动 cmd.Wait goroutine。
// Drain 关闭入口后返回 false，调用方必须自行同步清理，不能假设后续会收到退出事件。
func (m *Monitor) Arm(target Target) bool {
	if target.Cmd == nil {
		return false
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	m.wg.Add(1)
	m.mu.Unlock()
	safego.Go(context.Background(), nil, "mcp-orch.exitmonitor.wait", func(context.Context) {
		defer m.wg.Done()
		err := target.Cmd.Wait()
		m.publishExit(target.AgentID, target.LaunchSeq, err)
	})
	return true
}

// StartMonitoredCommand 启动本地命令并立刻把 Wait 交给 Monitor。
func StartMonitoredCommand(m *Monitor, logger *slog.Logger, target Target, command []string, cwd string, env []string) (*exec.Cmd, *processctl.Guard, error) {
	if m == nil {
		return nil, nil, errors.New("exit monitor is required")
	}
	if len(command) == 0 {
		return nil, nil, errors.New("command is required")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = cwd
	cmd.Env = env
	processctl.Configure(cmd)
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	guard := processctl.Attach(cmd, logger)
	target.Cmd = cmd
	if m.Arm(target) {
		return cmd, guard, nil
	}
	_ = processctl.ForceStop(cmd, guard)
	_ = cmd.Wait()
	if guard != nil {
		guard.Close()
	}
	return nil, nil, errors.New("exit monitor is closed")
}

// Emit 发布没有本地 cmd.Wait 的合成退出事件。
// 它与 Arm 共用 exactly-once 围栏，因此重复发同一 (agentID, launchSeq) 会被忽略。
func (m *Monitor) Emit(agentID string, launchSeq uint64, err error) {
	m.publishExit(agentID, launchSeq, err)
}

// ExitEvents 返回只读退出事件流；生产路径只有 runnerActor 消费。
func (m *Monitor) ExitEvents() <-chan Event { return m.events }

// Drain 关闭新 Arm 入口，并等待已启动的 cmd.Wait goroutine 结束。
// 调用方必须传带超时的 ctx，避免 shutdown 被异常子进程无限拖住。
func (m *Monitor) Drain(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	done := make(chan struct{})
	safego.Go(ctx, nil, "mcp-orch.exitmonitor.drain", func(context.Context) {
		m.wg.Wait()
		close(done)
	})
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// publishExit 先抢占 (agentID, launchSeq) 围栏，再把退出事件送入缓冲通道。
// Arm goroutine、Emit 调用方和测试都走这里，因此必须并发安全。
func (m *Monitor) publishExit(agentID string, seq uint64, err error) {
	if agentID == "" || seq == 0 {
		return
	}
	if !m.claimFire(agentID, seq) {
		return
	}
	committed := false
	defer func() {
		if !committed {
			m.releaseFire(agentID, seq)
		}
	}()
	result := Event{AgentID: agentID, LaunchSeq: seq, Err: err}
	select {
	case m.events <- result:
		m.commitFire(agentID, seq)
		committed = true
		return
	default:
	}
	m.logger.Warn("orchestration: exit event buffer full; falling back to bounded block",
		"agent_id", agentID, "launch_seq", seq)
	timer := time.NewTimer(m.publishBlockTimeout)
	defer timer.Stop()
	select {
	case m.events <- result:
		m.commitFire(agentID, seq)
		committed = true
	case <-timer.C:
		m.logger.Error("orchestration: exit event publish timed out; retry remains eligible",
			"agent_id", agentID, "launch_seq", seq, "timeout", m.publishBlockTimeout,
			"retryable", true)
	}
}

// claimFire 抢占 (agentID, launchSeq) 退出事件围栏，但只登记发送中的 claim。
// 已成功写入 channel 的 fence 或正在发送的 fence 都拒绝重复投递。
func (m *Monitor) claimFire(agentID string, seq uint64) bool {
	key := exitMonitorFenceKey(agentID, seq)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, done := m.fired[key]; done {
		return false
	}
	if _, publishing := m.publishing[key]; publishing {
		return false
	}
	m.publishing[key] = struct{}{}
	return true
}

// commitFire 在退出事件成功写入 Monitor channel 后提交 fence。
func (m *Monitor) commitFire(agentID string, seq uint64) {
	key := exitMonitorFenceKey(agentID, seq)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.publishing, key)
	m.fired[key] = struct{}{}
}

// releaseFire 归还未成功写入 Monitor channel 的 fence，使调用方可以安全重试。
func (m *Monitor) releaseFire(agentID string, seq uint64) {
	key := exitMonitorFenceKey(agentID, seq)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.publishing, key)
}

func exitMonitorFenceKey(agentID string, seq uint64) string {
	return fmt.Sprintf("%s#%d", agentID, seq)
}
