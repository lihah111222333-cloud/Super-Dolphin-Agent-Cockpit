package turn

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// turnInterruptEnvelope 是 RPC 响应里携带的中断判定摘要。
// 它只描述本次中断的 UI 可见结果，不参与 tracker 状态推进。
type turnInterruptEnvelope struct {
	confirmed      bool
	mode           string
	interruptSent  bool
	stateBefore    string
	stateAfter     string
	waitedMS       int64
	activeObserved bool
	requestID      string
	requestIDKnown bool
}

// interruptEnvelope 返回 TurnStatus 内部保存的中断摘要，供 RPC 结果构造使用。
func (s TurnStatus) interruptEnvelope() turnInterruptEnvelope {
	return s.interrupt
}

// attachInterruptEnvelope 把中断摘要附加到状态副本上。
// 调用方拿到的是返回值副本，tracker 内部状态不会因此被写入 UI envelope。
func attachInterruptEnvelope(status TurnStatus, envelope turnInterruptEnvelope) TurnStatus {
	status.interrupt = envelope
	return status
}

// attachAcceptedInterruptRequestID 把 provider 已接受的 Stop identity 附到响应状态副本。
func attachAcceptedInterruptRequestID(status TurnStatus, requestID string) TurnStatus {
	status.interrupt.requestID = strings.TrimSpace(requestID)
	status.interrupt.requestIDKnown = true
	return status
}

// buildTurnInterruptEnvelope 根据中断前后状态推导 UI 可展示的 settle mode。
func buildTurnInterruptEnvelope(
	beforeRaw string,
	afterRaw string,
	interruptSent bool,
	activeObserved bool,
	waitedMS int64,
	confirmed bool,
) turnInterruptEnvelope {
	beforeState := normalizeTurnInterruptState(beforeRaw)
	afterState := normalizeTurnInterruptState(afterRaw)
	mode := interruptSettleMode(confirmed, afterState)
	if !interruptSent || !activeObserved {
		mode = "no_active_turn"
	}
	return turnInterruptEnvelope{
		confirmed:      confirmed,
		mode:           mode,
		interruptSent:  interruptSent,
		stateBefore:    beforeState,
		stateAfter:     afterState,
		waitedMS:       waitedMS,
		activeObserved: activeObserved,
	}
}

// buildTurnInterruptTimeoutEnvelope 构造 provider 已收到中断但本地等待超时的摘要。
func buildTurnInterruptTimeoutEnvelope(beforeRaw string, afterRaw string, waitedMS int64) turnInterruptEnvelope {
	return turnInterruptEnvelope{
		confirmed:      true,
		mode:           "interrupt_timeout",
		interruptSent:  true,
		stateBefore:    normalizeTurnInterruptState(beforeRaw),
		stateAfter:     normalizeTurnInterruptState(afterRaw),
		waitedMS:       waitedMS,
		activeObserved: true,
	}
}

// buildTurnInterruptNotAppliedEnvelope 区分 provider 拒绝当前目标与本地目标已经切换。
func buildTurnInterruptNotAppliedEnvelope(beforeRaw string) turnInterruptEnvelope {
	state := normalizeTurnInterruptState(beforeRaw)
	return turnInterruptEnvelope{
		confirmed:      false,
		mode:           "not_applied",
		interruptSent:  false,
		stateBefore:    state,
		stateAfter:     state,
		activeObserved: true,
	}
}

// buildTurnInterruptRegisteredEnvelope 表示 preparing 阶段已锁定同一 Stop identity，
// 尚未取得 provider turn ID，因此绝不声称中断已发送或已完成。
func buildTurnInterruptRegisteredEnvelope(beforeRaw, afterRaw string) turnInterruptEnvelope {
	return turnInterruptEnvelope{
		confirmed:      false,
		mode:           "interrupt_registered",
		interruptSent:  false,
		stateBefore:    normalizeTurnInterruptState(beforeRaw),
		stateAfter:     normalizeTurnInterruptState(afterRaw),
		activeObserved: true,
	}
}

// buildTurnInterruptSentPendingEnvelope 表示 provider 已接受中断但真实终态尚未观察到。
func buildTurnInterruptSentPendingEnvelope(beforeRaw, afterRaw string) turnInterruptEnvelope {
	return turnInterruptEnvelope{
		confirmed:      false,
		mode:           "interrupt_sent_pending",
		interruptSent:  true,
		stateBefore:    normalizeTurnInterruptState(beforeRaw),
		stateAfter:     normalizeTurnInterruptState(afterRaw),
		waitedMS:       0,
		activeObserved: true,
	}
}

// buildTurnInterruptTerminalReplayEnvelope 复用既有 settle 契约，回放同一 request 的真实终态。
func buildTurnInterruptTerminalReplayEnvelope(stateRaw string, interruptSent bool) turnInterruptEnvelope {
	return buildTurnInterruptEnvelope(stateRaw, stateRaw, interruptSent, true, 0, interruptSent && interruptConfirmed(stateRaw))
}

// normalizeTurnInterruptState 把 provider 与 tracker 的多种终止词折叠成 UI 分支需要的少数类别。
// 未知状态原样返回，让前端和日志仍能看到 provider 的真实字面量。
func normalizeTurnInterruptState(raw string) string {
	state := strings.ToLower(strings.TrimSpace(raw))
	switch state {
	case "", "completed", "complete", "done", "success", "succeeded", "ready", "stopped", "ended", "closed", "interrupted":
		return "idle"
	case "failed", "fail", "stalled", "error":
		return "error"
	case "preparing", "running", "interrupting":
		return "running"
	default:
		return state
	}
}

// interruptSettleMode 把确认结果和最终状态映射为前端可分支处理的中断模式。
func interruptSettleMode(confirmed bool, afterState string) string {
	if confirmed {
		return "interrupt_confirmed"
	}
	switch normalizeTurnInterruptState(afterState) {
	case "error":
		return "interrupt_terminal_failed"
	case "idle":
		return "interrupt_terminal_completed"
	default:
		return "interrupt_timeout"
	}
}

// interruptConfirmed 只接受 tracker 明确进入 interrupted；completed/failed 等迟到终态不算确认中断。
func interruptConfirmed(state string) bool {
	return strings.EqualFold(strings.TrimSpace(state), "interrupted")
}

// interruptProviderID 优先使用已追踪状态里的 providerID，缺失时回退到当前 handle。
func interruptProviderID(status TurnStatus, handle contract.TurnHandle) string {
	if providerID := strings.TrimSpace(status.ProviderID); providerID != "" {
		return providerID
	}
	if handle == nil {
		return ""
	}
	return strings.TrimSpace(handle.ProviderID())
}
