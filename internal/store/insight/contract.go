// Package insight persists P21 P3 per-turn session metrics.
//
// This store is the DB surface; the collector / flush worker / dashboard
// aggregation that live on top of it will land in Track F phase 2. The
// UPSERT query enforces the precedence and non-regression invariants at
// the SQL layer so a misbehaving consumer cannot silently downgrade a
// terminal status or regress token counters.
package insight

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Status constants mirror the P3 plan's status domain.
const (
	StatusUnknown     = "unknown"
	StatusCompleted   = "completed"
	StatusInterrupted = "interrupted"
	StatusAborted     = "aborted"
	StatusFailed      = "failed"
	StatusStalled     = "stalled"
)

// Sentinel errors.
var (
	ErrNotFound = errors.New("insight: session insight not found")
	ErrEmptyID  = errors.New("insight: id is required")
)

// Store is the domain facade. All methods work with the Insight DTO; the
// sqlc-level SessionInsight row never leaks to callers.
type Store interface {
	Upsert(ctx context.Context, params UpsertParams) (Insight, error)
	GetByLocalTurn(ctx context.Context, threadID, localTurnID string) (Insight, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]Insight, error)
	ListRecent(ctx context.Context, limit int32) ([]Insight, error)
	ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalRow, error)
	ListObservedTokenTurns(ctx context.Context, threadID string, limit int32) ([]TokenRow, error)
}

// Insight is the per-turn aggregate. Success is *bool so consumers can
// distinguish "unknown" from "false"; see the P3 plan on the
// TurnCompleted.Success non-pointer bool trap.
type Insight struct {
	ID                       int64
	ThreadID                 string
	AgentID                  string
	SessionID                string
	Provider                 string
	LocalTurnID              string
	ProviderTurnID           string
	StartedAt                time.Time
	CompletedAt              time.Time
	DurationMS               int32
	Success                  *bool
	Status                   string
	StopReason               string
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
	TokenInput               int32
	TokenOutput              int32
	TokenTotal               int32
	TokenSnapshotObserved    bool
	ContextWindowTokens      int32
	UIProjection             string
	SkillsSelected           json.RawMessage
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// UpsertParams drives Store.Upsert. SkillsSelected is optional JSON; nil
// / empty is treated as "no change" by the SQL DO UPDATE clause (so the
// collector can flush token / terminal updates without re-sending skills
// every time).
type UpsertParams struct {
	ThreadID                 string
	AgentID                  string
	SessionID                string
	Provider                 string
	LocalTurnID              string
	ProviderTurnID           string
	StartedAt                time.Time
	CompletedAt              time.Time
	DurationMS               int32
	Success                  *bool
	Status                   string
	StopReason               string
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
	TokenInput               int32
	TokenOutput              int32
	TokenTotal               int32
	TokenSnapshotObserved    bool
	ContextWindowTokens      int32
	UIProjection             string
	SkillsSelected           json.RawMessage
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// ApprovalRow is the slim projection for ListObservedApprovalRequests.
// It deliberately excludes unobserved turns so callers that want a
// true average / percentile don't have to filter themselves.
type ApprovalRow struct {
	ID               int64
	ThreadID         string
	AgentID          string
	LocalTurnID      string
	ProviderTurnID   string
	ApprovalRequests int32
	CreatedAt        time.Time
}

// TokenRow is the slim projection for ListObservedTokenTurns.
type TokenRow struct {
	ID                  int64
	ThreadID            string
	AgentID             string
	LocalTurnID         string
	ProviderTurnID      string
	TokenInput          int32
	TokenOutput         int32
	TokenTotal          int32
	ContextWindowTokens int32
	CreatedAt           time.Time
}
