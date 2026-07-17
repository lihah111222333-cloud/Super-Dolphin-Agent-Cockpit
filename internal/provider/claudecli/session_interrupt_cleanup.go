package claudecli

import (
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
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
		return errors.Join(fmt.Errorf("signal interrupt: %w", err), tr.Kill())
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

// cleanupInterruptedTransport 确认进程退出后解除 PID 注册并执行清理回调。
// 任一 settle 错误或仍存活状态都会保留 PID remediation ownership，由调用方返回失败语义。
func cleanupInterruptedTransport(reg *pidregistry.Registry, tr *transport, cleanup func(), settleTransport func(*transport) error) error {
	if tr == nil {
		if cleanup != nil {
			cleanup()
		}
		return nil
	}
	if settleTransport == nil {
		return errors.New("claudecli: interrupt transport settle function is required")
	}
	if err := settleTransport(tr); err != nil {
		return fmt.Errorf("claudecli: settle interrupted transport: %w", err)
	}
	if tr.Running() {
		return errors.New("claudecli: interrupted transport is still running")
	}
	unregisterTransportPID(reg, tr)
	if cleanup != nil {
		cleanup()
	}
	return nil
}
