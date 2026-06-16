// Package insight persists P21 P3 per-turn session metrics.
//
// This store is the DB surface; the collector / flush worker / dashboard
// aggregation that live on top of it will land in Track F phase 2. The
// UPSERT query enforces the precedence and non-regression invariants at
// the SQL layer so a misbehaving consumer cannot silently downgrade a
// terminal status or regress token counters.
package insight

import "github.com/anthropic-ai/super-agent-v3/internal/contract"

// Status constants mirror the P3 plan's status domain.
const (
	StatusUnknown     = contract.InsightStatusUnknown
	StatusCompleted   = contract.InsightStatusCompleted
	StatusInterrupted = contract.InsightStatusInterrupted
	StatusAborted     = contract.InsightStatusAborted
	StatusFailed      = contract.InsightStatusFailed
	StatusStalled     = contract.InsightStatusStalled
)

// Sentinel errors.
var (
	ErrNotFound = contract.ErrInsightNotFound
	ErrEmptyID  = contract.ErrInsightEmptyID
)

// Store is the domain facade. All methods work with the Insight DTO; the
// sqlc-level SessionInsight row never leaks to callers.
type Store = contract.InsightStore

// Insight is the per-turn aggregate. Success is *bool so consumers can
// distinguish "unknown" from "false"; see the P3 plan on the
// TurnCompleted.Success non-pointer bool trap.
type Insight = contract.Insight

// UpsertParams drives Store.Upsert. SkillsSelected is optional JSON; nil
// / empty is treated as "no change" by the SQL DO UPDATE clause (so the
// collector can flush token / terminal updates without re-sending skills
// every time).
type UpsertParams = contract.InsightUpsertParams

// ApprovalRow is the slim projection for ListObservedApprovalRequests.
// It deliberately excludes unobserved turns so callers that want a
// true average / percentile don't have to filter themselves.
type ApprovalRow = contract.InsightApprovalRow

// TokenRow is the slim projection for ListObservedTokenTurns.
type TokenRow = contract.InsightTokenRow
