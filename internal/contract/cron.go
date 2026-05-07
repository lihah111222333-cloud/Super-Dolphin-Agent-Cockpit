package contract

import (
	"context"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// ---------------------------------------------------------------------------
// CronThreadStarter (was cron_thread.go)
// ---------------------------------------------------------------------------

// CronThreadStarter is the narrow contract surface that the cron module uses
// to bootstrap a provider thread on a job's first trigger. The full
// thread.Service satisfies a much wider interface; this seam keeps
// cron decoupled from the thread module's implementation types.
//
// The production adapter (thread.CronStarterAdapter) wraps thread.Service;
// see internal/module/thread/cron_adapter.go.
type CronThreadStarter interface {
	CronStartThread(ctx context.Context, req CronStartThreadRequest) (CronStartThreadResult, error)
}

// CronStartThreadRequest carries the subset of thread-start inputs that the
// cron scheduler's first-trigger bootstrap path actually populates. Fields
// intentionally mirror thread.StartRequest's names so the adapter is a
// trivial field copy.
type CronStartThreadRequest struct {
	Provider string
	CWD      string
	Model    string
	Name     string
	Config   map[string]any
}

// CronStartThreadResult carries the thread bootstrap outputs that the cron
// scheduler needs to persist back onto the job row.
type CronStartThreadResult struct {
	ThreadID string
	AgentID  string
}

// ---------------------------------------------------------------------------
// CronTurnExecutor (was cron_turn.go)
// ---------------------------------------------------------------------------

// CronTurnExecutor is the narrow contract surface that the cron module's
// TurnServiceAdapter uses to prepare, submit, track, and dedupe turns.
// The full turn.Service satisfies a much wider interface; this seam keeps
// cron decoupled from the turn module's implementation types.
//
// The production adapter (turn.CronExecutorAdapter) wraps turn.Service;
// see internal/module/turn/cron_adapter.go.
type CronTurnExecutor interface {
	CronPrepareTurn(ctx context.Context, session Session, input CronPrepareInput) (dto.TurnRequest, error)
	CronStartTurn(ctx context.Context, session Session, req dto.TurnRequest) (TurnHandle, error)
	CronTrackTurn(ctx context.Context, localID string) (CronTurnStatus, error)
	CronLookupByDedupeKey(ctx context.Context, dedupeKey string) (CronTurnStatus, bool, error)
}

// CronPrepareInput carries the subset of turn-preparation inputs that the
// cron scheduler actually populates. Fields mirror turn.PrepareInput names
// so the adapter is a trivial field copy.
type CronPrepareInput struct {
	Prompt              string
	Skills              []dto.SkillRef
	Provider            string
	Model               string
	AgentID             string
	CWD                 string
	ThreadRuntimeConfig map[string]any
	DedupeKey           string
}

// CronTurnStatus carries the turn-tracking fields that the cron scheduler
// inspects after TrackTurn / LookupByDedupeKey.
type CronTurnStatus struct {
	LocalID    string
	ProviderID string
	State      string
}
