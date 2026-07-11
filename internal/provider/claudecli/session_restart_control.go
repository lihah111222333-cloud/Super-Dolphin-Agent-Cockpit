package claudecli

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
)

// registerTransportPID 将 Claude CLI 子进程登记到崩溃清理表。
// 注册文件写入失败意味着新进程无法被后续回收，调用方必须停止 transport 并返回错误。
func registerTransportPID(reg *pidregistry.Registry, tr *transport, agentID string) error {
	if reg == nil || tr == nil || tr.cmd == nil || tr.cmd.Process == nil {
		return nil
	}
	pid := tr.cmd.Process.Pid
	if err := reg.RegisterChecked(pid, "claude-cli", map[string]string{"agent_id": agentID}); err != nil {
		return fmt.Errorf("register claude-cli pid %d: %w", pid, err)
	}
	return nil
}

func unregisterTransportPID(reg *pidregistry.Registry, tr *transport) {
	if reg == nil || tr == nil || tr.cmd == nil || tr.cmd.Process == nil {
		return
	}
	reg.Unregister(tr.cmd.Process.Pid)
}

func (s *session) beginRestartWaitLocked(ctx context.Context) (context.Context, uint64) {
	if s == nil {
		return ctx, 0
	}
	restartCtx, cancel := context.WithCancel(ctx)
	s.restartGeneration++
	s.restartCancel = cancel
	return restartCtx, s.restartGeneration
}

func (s *session) finishRestartWaitLocked(generation uint64) {
	if s == nil || generation == 0 || s.restartGeneration != generation {
		return
	}
	s.restartCancel = nil
}

func stopTransport(tr *transport, force bool) error {
	if force {
		return tr.Kill()
	}
	return tr.Close()
}

func releaseTransport(tr *transport, cleanup func()) {
	if tr != nil {
		_ = tr.Close()
	}
	if cleanup != nil {
		cleanup()
	}
}
