package claudecli

import (
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
)

const interruptTransportGracePeriod = 2 * time.Second

// defaultSettleInterruptedTransport 用默认宽限期完成 interrupt 后的 transport 收尾。
func defaultSettleInterruptedTransport(tr *transport) error {
	return settleInterruptedTransportWithTimeout(tr, interruptTransportGracePeriod)
}

// settleInterruptedTransportWithTimeout 先发 interrupt，再在宽限期后升级为 kill。
// Claude CLI 正常退出时关闭 stdin；若进程仍存活则强制终止，避免旧 transport 继续写事件。
func settleInterruptedTransportWithTimeout(tr *transport, grace time.Duration) error {
	if tr == nil {
		return nil
	}
	if err := normalizeSignalError(tr.signalProcess(sigInterrupt)); err != nil {
		return tr.Kill()
	}
	if grace > 0 {
		tr.waitForExit(grace)
	}
	if tr.Running() {
		return tr.Kill()
	}
	tr.closeInput()
	return nil
}

// cleanupInterruptedTransport 解除 PID 注册并执行被 interrupt transport 的清理回调。
// settle 失败只记录告警，因为调用方已经切换到新的 transport，不能让旧进程清理阻断恢复流程。
func cleanupInterruptedTransport(logger *slog.Logger, reg *pidregistry.Registry, tr *transport, cleanup func(), settleTransport func(*transport) error) {
	if tr == nil {
		if cleanup != nil {
			cleanup()
		}
		return
	}
	unregisterTransportPID(reg, tr)
	if err := settleTransport(tr); err != nil && logger != nil {
		logger.Warn("claudecli: interrupt transport cleanup failed", "error", err)
	}
	if cleanup != nil {
		cleanup()
	}
}
