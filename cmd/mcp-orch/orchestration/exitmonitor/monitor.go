package exitmonitor

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Monitor 是本地编排进程 cmd.Wait 的唯一拥有者。
// 它用 (agentID, launchSeq) 做 exactly-once 围栏，让本地 Wait 和远端合成退出事件不会重复推进状态。
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
	go func() {
		defer m.wg.Done()
		err := target.Cmd.Wait()
		m.publishExit(target.AgentID, target.LaunchSeq, err)
	}()
	return true
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
	go func() { m.wg.Wait(); close(done) }()
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
	result := Event{AgentID: agentID, LaunchSeq: seq, Err: err}
	select {
	case m.events <- result:
		return
	default:
	}
	m.logger.Warn("orchestration: exit event buffer full; falling back to bounded block",
		"agent_id", agentID, "launch_seq", seq)
	timer := time.NewTimer(m.publishBlockTimeout)
	defer timer.Stop()
	select {
	case m.events <- result:
	case <-timer.C:
		m.logger.Error("orchestration: dropped exit event after publishBlockTimeout",
			"agent_id", agentID, "launch_seq", seq, "timeout", m.publishBlockTimeout)
	}
}

// claimFire 抢占 (agentID, launchSeq) 退出事件围栏。
// 返回 false 表示同一进程生命周期的退出事件已发布过，调用方必须跳过重复投递。
func (m *Monitor) claimFire(agentID string, seq uint64) bool {
	key := exitMonitorFenceKey(agentID, seq)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, done := m.fired[key]; done {
		return false
	}
	m.fired[key] = struct{}{}
	return true
}

func exitMonitorFenceKey(agentID string, seq uint64) string {
	return fmt.Sprintf("%s#%d", agentID, seq)
}
