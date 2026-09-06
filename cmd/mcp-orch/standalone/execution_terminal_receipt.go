package standalone

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// cliTerminalTruth is the provider-side identity used to decide whether a
// late notification is an exact retry of a terminal already accepted by this
// session. It intentionally excludes the generated canonical EventID and
// timestamp: those values must remain stable in the pending receipt itself.
type cliTerminalTruth struct {
	provider             string
	threadID             string
	turnID               string
	providerTurnID       string
	status               string
	output               string
	interruptRequestID   string
	providerEventType    string
	providerEventSubtype string
	providerEventID      string
	providerResult       string
	parentToolUseID      string
	providerIsError      bool
}

// pendingTerminalReceipt is deliberately a single slot on cliSession. A
// session has at most one active provider turn, so this bounds retained
// canonical output while a receipt consumer is unavailable.
type pendingTerminalReceipt struct {
	terminal  turndto.TurnTerminalV2
	truth     cliTerminalTruth
	inFlight  bool
	retryable bool
}

var (
	errCLITerminalReceiptMissing  = errors.New("CLI terminal receipt is not pending")
	errCLITerminalReceiptInFlight = errors.New("CLI terminal receipt retry is already in flight")
)

const (
	// Keep provider truth bounded even when a malformed result frame contains
	// an unexpectedly large diagnostic or parent-tool identifier.
	maxCLITruthFieldRunes = 4096
	maxCLIIdentityRunes   = 4096
)

func codexTerminalTruth(event codexEvent, output, interruptRequestID string) cliTerminalTruth {
	return cliTerminalTruth{
		provider:           "codex",
		threadID:           event.ThreadID,
		turnID:             event.Turn.ID,
		providerTurnID:     boundedCLITruthField(event.TurnID),
		status:             boundedCLITruthField(event.Turn.Status),
		output:             output,
		interruptRequestID: boundedCLITruthField(interruptRequestID),
	}
}

func claudeTerminalTruth(event claudewire.Event, turnID string) cliTerminalTruth {
	parentToolUseID := ""
	if event.ParentToolUseID != nil {
		parentToolUseID = boundedCLITruthField(*event.ParentToolUseID)
	}
	return cliTerminalTruth{
		provider:             "claude",
		threadID:             event.SessionID,
		turnID:               turnID,
		providerEventType:    boundedCLITruthField(event.Type),
		providerEventSubtype: boundedCLITruthField(event.Subtype),
		providerEventID:      boundedCLITruthField(event.UUID),
		providerResult:       boundedCLITruthField(event.Result),
		parentToolUseID:      parentToolUseID,
		providerIsError:      event.IsError,
	}
}

// stableCLIEventID derives one canonical identity from provider truth. This
// makes direct construction deterministic and, more importantly, prevents a
// sink retry from manufacturing a second terminal identity.
func stableCLIEventID(truth cliTerminalTruth) string {
	source := fmt.Sprintf("%q\x00%q\x00%q\x00%q\x00%q\x00%q\x00%q\x00%q\x00%q\x00%q\x00%q\x00%t",
		truth.provider, truth.threadID, truth.turnID, truth.providerTurnID, truth.status,
		truth.output, truth.interruptRequestID, truth.providerEventType,
		truth.providerEventSubtype, truth.providerEventID, truth.providerResult,
		truth.providerIsError)
	digest := sha256.Sum256([]byte(source))
	return fmt.Sprintf("orch-cli-event-%x", digest)
}

func buildCodexTerminal(event codexEvent, output, interruptRequestID string) (turndto.TurnTerminalV2, cliTerminalTruth, error) {
	if err := validateCLIIdentity(event.ThreadID, event.Turn.ID); err != nil {
		return turndto.TurnTerminalV2{}, cliTerminalTruth{}, err
	}
	truth := codexTerminalTruth(event, output, interruptRequestID)
	terminal := terminalFromCLITruth(truth, event.Turn.Status)
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return turndto.TurnTerminalV2{}, cliTerminalTruth{}, err
	}
	return terminal, truth, nil
}

func buildClaudeTerminal(event claudewire.Event, turnID, output string, failed bool) (turndto.TurnTerminalV2, cliTerminalTruth, error) {
	if err := validateCLIIdentity(event.SessionID, turnID); err != nil {
		return turndto.TurnTerminalV2{}, cliTerminalTruth{}, err
	}
	truth := claudeTerminalTruth(event, turnID)
	status := "completed"
	if failed || event.IsError || event.Subtype != "success" {
		status = "failed"
	}
	terminal := terminalFromCLITruth(truth, status)
	if status == "completed" {
		terminal.PublicSummary = terminalSummary(output)
	}
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return turndto.TurnTerminalV2{}, cliTerminalTruth{}, err
	}
	return terminal, truth, nil
}

func validateCLIIdentity(threadID, turnID string) error {
	if utf8.RuneCountInString(threadID) > maxCLIIdentityRunes || utf8.RuneCountInString(turnID) > maxCLIIdentityRunes {
		return errors.New("CLI terminal identity exceeds bounded length")
	}
	return nil
}

func boundedCLITruthField(value string) string {
	if utf8.RuneCountInString(value) <= maxCLITruthFieldRunes {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("<sha256:%x:%d>", digest, len(value))
}

func terminalFromCLITruth(truth cliTerminalTruth, status string) turndto.TurnTerminalV2 {
	terminal := turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       stableCLIEventID(truth),
		ThreadID:      truth.threadID,
		TurnID:        truth.turnID,
		OccurredAt:    nowUTCString(),
	}
	switch status {
	case "completed":
		terminal.Outcome = "success"
		terminal.PublicSummary = terminalSummary(truth.output)
	case "interrupted":
		setInterruptedTerminal(&terminal, truth.interruptRequestID)
	default:
		terminal.Outcome = "failed"
		terminal.PublicError = &turndto.PublicErrorV1{Code: "cli_turn_failed", Title: "CLI turn failed",
			Message: "The CLI did not complete this turn.", DiagnosticID: diagnosticIDForCLIEvent(terminal.EventID),
			RecoveryActions: []string{"copy_diagnostics"}}
	}
	return terminal
}

func nowUTCString() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func cloneCLITerminal(terminal turndto.TurnTerminalV2) turndto.TurnTerminalV2 {
	clone := terminal
	if terminal.PartialItemIDs != nil {
		clone.PartialItemIDs = append([]string(nil), terminal.PartialItemIDs...)
	}
	if terminal.PublicError != nil {
		err := *terminal.PublicError
		if terminal.PublicError.RecoveryActions != nil {
			err.RecoveryActions = append([]string(nil), terminal.PublicError.RecoveryActions...)
		}
		clone.PublicError = &err
	}
	return clone
}

func (e *Execution) deliverCanonicalTerminal(terminal turndto.TurnTerminalV2) error {
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return err
	}
	e.mu.Lock()
	sink := e.sink
	receiptSink := e.receiptSink
	e.mu.Unlock()
	// Pass an isolated value to consumers. A consumer must not be able to
	// mutate the canonical receipt retained for an exact retry.
	terminal = cloneCLITerminal(terminal)
	if receiptSink != nil {
		return receiptSink(context.Background(), terminal)
	}
	if sink == nil {
		return errors.New("CLI terminal sink is not configured")
	}
	sink(context.Background(), terminal)
	return nil
}

// claimTerminalReceiptLocked either creates the single pending slot or claims
// it for an exact retry. The caller must hold session.mu.
func claimTerminalReceiptLocked(session *cliSession, truth cliTerminalTruth, terminal turndto.TurnTerminalV2) (*pendingTerminalReceipt, bool, error) {
	if session == nil {
		return nil, false, errors.New("CLI session is required")
	}
	if pending := session.pendingTerminal; pending != nil {
		if pending.truth != truth {
			return pending, false, fmt.Errorf("CLI terminal truth conflict for turn %q", pending.terminal.TurnID)
		}
		if pending.inFlight {
			return pending, false, nil
		}
		pending.inFlight, pending.retryable = true, false
		return pending, true, nil
	}
	pending := &pendingTerminalReceipt{terminal: cloneCLITerminal(terminal), truth: truth, inFlight: true}
	session.pendingTerminal = pending
	return pending, true, nil
}

func (e *Execution) finishTerminalReceipt(session *cliSession, pending *pendingTerminalReceipt) error {
	if session == nil || pending == nil {
		return errors.New("CLI terminal receipt session is required")
	}
	err := e.deliverCanonicalTerminal(pending.terminal)
	if err != nil {
		session.mu.Lock()
		if session.pendingTerminal == pending {
			pending.inFlight, pending.retryable = false, true
			previousReceiptErr := session.terminalReceiptErr
			session.terminalReceiptErr = err
			if session.failed == nil || session.failed == previousReceiptErr {
				// Do not hide an independent provider/protocol failure behind a
				// later consumer outage; only replace an earlier receipt error.
				session.failed = err
			}
			session.submitting = false
		}
		session.mu.Unlock()
		session.signalReady(err)
		e.reclaimSettledSession(session)
		return err
	}
	session.mu.Lock()
	if session.pendingTerminal == pending {
		session.completedTurnID = pending.terminal.TurnID
		session.turnID = ""
		session.output = ""
		session.interruptRequestID = ""
		session.submitting = false
		session.pendingTerminal = nil
		if session.terminalReceiptErr != nil && session.failed == session.terminalReceiptErr {
			session.failed = nil
		}
		session.terminalReceiptErr = nil
	}
	session.mu.Unlock()
	// A stopped/settled session can finally release its launch identity once
	// the canonical terminal has actually been acknowledged.
	e.reclaimSettledSession(session)
	return nil
}

// retryPendingTerminal is the owner-side recovery seam. It only retries the
// retained canonical sink delivery; it never invokes a provider operation.
func (e *Execution) retryPendingTerminal(session *cliSession) error {
	if session == nil {
		return errCLITerminalReceiptMissing
	}
	session.mu.Lock()
	pending := session.pendingTerminal
	if pending == nil {
		session.mu.Unlock()
		return errCLITerminalReceiptMissing
	}
	if pending.inFlight {
		session.mu.Unlock()
		return errCLITerminalReceiptInFlight
	}
	pending.inFlight, pending.retryable = true, false
	session.mu.Unlock()
	return e.finishTerminalReceipt(session, pending)
}

// RetryPendingTerminal retries a failed strict receipt after the consumer has
// recovered. It is intentionally separate from SubmitTurn, so recovery cannot
// issue a second provider call.
func (e *Execution) RetryPendingTerminal(ctx context.Context, agentID string) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	session, err := e.session(agentID)
	if err != nil {
		return err
	}
	return e.retryPendingTerminal(session)
}
