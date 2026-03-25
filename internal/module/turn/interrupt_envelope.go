package turn

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type turnInterruptEnvelope struct {
	confirmed      bool
	mode           string
	interruptSent  bool
	stateBefore    string
	stateAfter     string
	waitedMS       int64
	activeObserved bool
}

func (s TurnStatus) interruptEnvelope() turnInterruptEnvelope {
	return s.interrupt
}

func attachInterruptEnvelope(status TurnStatus, envelope turnInterruptEnvelope) TurnStatus {
	status.interrupt = envelope
	return status
}

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

func interruptConfirmed(state string) bool {
	return strings.EqualFold(strings.TrimSpace(state), "interrupted")
}

func interruptProviderID(status TurnStatus, handle contract.TurnHandle) string {
	if providerID := strings.TrimSpace(status.ProviderID); providerID != "" {
		return providerID
	}
	if handle == nil {
		return ""
	}
	return strings.TrimSpace(handle.ProviderID())
}
