package claudecli

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
)

func registerTransportPID(reg *pidregistry.Registry, tr *transport, agentID string) {
	if reg == nil || tr == nil || tr.cmd == nil || tr.cmd.Process == nil {
		return
	}
	reg.Register(tr.cmd.Process.Pid, "claude-cli", map[string]string{"agent_id": agentID})
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
