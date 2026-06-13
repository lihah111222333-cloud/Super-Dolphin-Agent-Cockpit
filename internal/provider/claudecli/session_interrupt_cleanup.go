package claudecli

import (
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
)

const interruptTransportGracePeriod = 2 * time.Second

func defaultSettleInterruptedTransport(tr *transport) error {
	return settleInterruptedTransportWithTimeout(tr, interruptTransportGracePeriod)
}

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

// cleanupInterruptedTransport 处理cleanupinterrupted传输。
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
