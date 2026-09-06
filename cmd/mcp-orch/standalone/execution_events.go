package standalone

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/creachadair/jrpc2"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

type codexEvent struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Delta    string `json:"delta"`
	Turn     struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"turn"`
}

// notificationHandler 在传输回调上处理已知事件，正文上限先于追加。
func (e *Execution) notificationHandler(session *cliSession) func(*jrpc2.Request) {
	return func(request *jrpc2.Request) {
		switch request.Method() {
		case "item/agentMessage/delta", "turn/completed":
		default:
			return
		}
		var event codexEvent
		if err := request.UnmarshalParams(&event); err != nil {
			e.failSession(session, err)
			return
		}
		e.handleCodexEvent(session, request.Method(), event)
	}
}

// handleCodexEvent 只让当前 turn 的通知修改输出和 busy 状态。
func (e *Execution) handleCodexEvent(session *cliSession, method string, event codexEvent) {
	if session == nil {
		return
	}
	session.mu.Lock()
	if event.ThreadID == "" || event.ThreadID != session.threadID {
		session.mu.Unlock()
		return
	}
	if method == "item/agentMessage/delta" {
		if !session.acceptCodexDelta(event) {
			session.mu.Unlock()
			return
		}
		session.mu.Unlock()
		return
	}
	session.mu.Unlock()
	e.handleCodexCompletion(session, event)
}

func (s *cliSession) acceptCodexDelta(event codexEvent) bool {
	if s.stopped || s.pendingTerminal != nil {
		return false
	}
	s.acceptDelta(event)
	return true
}

// handleCodexCompletion 将 Codex 完成通知映射为唯一 canonical receipt。
func (e *Execution) handleCodexCompletion(session *cliSession, event codexEvent) {
	session.mu.Lock()
	// A strict receipt failure leaves one canonical terminal in this slot. A
	// duplicate provider notification may retry that exact receipt, but a
	// different payload for the same turn is a conflict and must not replace
	// the first truth. Keep this check before failed/stopped fences so consumer
	// recovery remains possible after failSession.
	if pending := session.pendingTerminal; pending != nil {
		session.mu.Unlock()
		e.retryCodexReceipt(session, event, pending)
		return
	}
	if session.stopped || !session.acceptCompletion(event.Turn.ID) {
		session.mu.Unlock()
		return
	}
	// Early Codex completion can arrive before turn/start returns. Preserve the
	// provider turn identity while the receipt is pending; SubmitTurn will see
	// the same ID in its response and must not reopen the turn.
	if session.turnID == "" {
		session.turnID = event.Turn.ID
	}
	terminal, truth, err := buildCodexTerminalForSession(session, event)
	if err != nil {
		session.mu.Unlock()
		e.failSession(session, err)
		return
	}
	pending, _, claimErr := claimTerminalReceiptLocked(session, truth, terminal)
	if claimErr != nil {
		session.mu.Unlock()
		e.failSession(session, claimErr)
		return
	}
	session.mu.Unlock()
	_ = e.finishTerminalReceipt(session, pending)
}

func (e *Execution) retryCodexReceipt(session *cliSession, event codexEvent, pending *pendingTerminalReceipt) {
	session.mu.Lock()
	truth := codexTerminalTruth(event, session.output, session.interruptRequestID)
	session.mu.Unlock()
	if pending.truth != truth {
		if event.Turn.ID == pending.terminal.TurnID {
			e.failSession(session, fmt.Errorf("CLI terminal truth conflict for turn %q", event.Turn.ID))
		}
		return
	}
	session.mu.Lock()
	if pending.inFlight {
		session.mu.Unlock()
		return
	}
	pending.inFlight, pending.retryable = true, false
	session.mu.Unlock()
	_ = e.finishTerminalReceipt(session, pending)
}

func buildCodexTerminalForSession(session *cliSession, event codexEvent) (turndto.TurnTerminalV2, cliTerminalTruth, error) {
	output, interruptRequestID := session.output, session.interruptRequestID
	truth := codexTerminalTruth(event, output, interruptRequestID)
	status := event.Turn.Status
	if session.failed != nil {
		status, output = "failed", ""
	}
	terminal, _, err := buildCodexTerminal(event, output, interruptRequestID)
	if err == nil && status != event.Turn.Status {
		terminal = terminalFromCLITruth(truth, status)
		err = turndto.ValidateTurnTerminalV2(terminal)
	}
	return terminal, truth, err
}

// acceptDelta 在持有 session.mu 时检查 turn，旧通知不能混入新报告。
func (s *cliSession) acceptDelta(event codexEvent) {
	if s.failed != nil {
		return
	}
	if event.TurnID == "" || event.TurnID == s.completedTurnID {
		return
	}
	if s.turnID != event.TurnID && !s.submitting {
		return
	}
	if len(s.output)+len(event.Delta) > 1024*1024 {
		s.failed = errors.New("CLI output exceeds bounded report limit")
		return
	}
	s.output += event.Delta
}

// acceptCompletion 在提交窗口允许早到终态，其余情况要求 exact turn 匹配。
func (s *cliSession) acceptCompletion(turnID string) bool {
	if turnID == "" || turnID == s.completedTurnID {
		return false
	}
	return s.turnID == turnID || s.submitting
}

// deliverTerminal 将 provider 状态映射到既有 canonical terminal，不另写任务状态。
func (e *Execution) deliverTerminal(event codexEvent, output, interruptRequestID string) error {
	terminal, _, err := buildCodexTerminal(event, output, interruptRequestID)
	if err != nil {
		return err
	}
	// Keep the canonical consumer guard anchored at this legacy helper as well
	// as in the session-owned receipt path.
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return err
	}
	return e.deliverCanonicalTerminal(terminal)
}

// setInterruptedTerminal 区分本地请求和 provider 自发中断，不伪造 request ID。
func setInterruptedTerminal(terminal *turndto.TurnTerminalV2, requestID string) {
	terminal.Outcome = "interrupted"
	if requestID != "" {
		terminal.TerminationCause, terminal.TerminationRequestID = "user_request", requestID
		return
	}
	terminal.TerminationCause = "provider"
	terminal.PublicError = &turndto.PublicErrorV1{
		Code: "cli_interrupted", Title: "CLI turn interrupted", Message: "The provider interrupted this turn.",
		DiagnosticID: diagnosticIDForCLIEvent(terminal.EventID), RecoveryActions: []string{"copy_diagnostics"},
	}
}

func diagnosticIDForCLIEvent(eventID string) string {
	digest := sha256.Sum256([]byte(eventID))
	return fmt.Sprintf("diag-%x", digest)
}

// terminalSummary 只投影有界摘要；完整正文必须由后续 event/content store 保存。
func terminalSummary(output string) string {
	if output == "" {
		return "CLI turn completed without text output."
	}
	if utf8.RuneCountInString(output) <= 4096 {
		return output
	}
	return string([]rune(output)[:4070]) + "\n[summary truncated]"
}
