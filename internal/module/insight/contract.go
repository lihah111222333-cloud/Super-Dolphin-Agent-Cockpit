// Package insight consumes the Canonical Turn Observation Contract and
// persists per-turn aggregate metrics through contract.InsightStore.
//
// The module wires together three pieces:
//   - subscriber (bus.ResilientSubscribe → queue): terminal events push a
//     small flush signal onto a bounded channel; callbacks do zero work
//     beyond enqueueing.
//   - flusher (platformrunner.Runner): drains the queue, reads facts from
//     observation.Contract, and UPSERTs into insight.Store. Runs until
//     ctx cancels; shutdown is a bounded drain (5s) per the P3 plan.
//   - service: read-side API consumed by dashboard RPC handlers from the
//     persisted rows.
package insight

import (
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Service re-exports the contract interface so in-package references
// (and downstream consumers that already import insight) keep working.
type Service = contract.InsightService

// Snapshot is the read-model row returned by insight queries.
type Snapshot = contract.InsightSnapshot

// ApprovalSnapshot is the approval-observation row returned by insight
// approval queries.
type ApprovalSnapshot = contract.InsightApprovalSnapshot

// ErrInvalidLimit re-exports the contract sentinel.
var ErrInvalidLimit = contract.ErrInsightInvalidLimit

// flushSignal is what the subscriber pushes onto the queue. Everything
// the flusher needs to read observation + build the Insight row is
// carried on the signal so the flusher never blocks on cross-module
// lookups.
type flushSignal struct {
	LocalTurnID string
	ThreadID    string
	AgentID     string
	Provider    string
	Timestamp   time.Time
	Retried     bool
}

// mapTerminalKindToStatus translates the observation.TerminalKind string
// into the insight.Status string expected by the DB. The two layers
// agree on all values except empty-string "" → "unknown"; this helper
// is the one place that boundary is crossed.
func mapTerminalKindToStatus(k string) string {
	if k == "" {
		return contract.InsightStatusUnknown
	}
	return k
}
