package cron

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"

	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

// recordingCronStore is a programmable double for SchedulerStore. Only
// the methods the scheduler calls have first-class slots; everything
// else returns zero-values so lint / compile stays quiet.
type recordingCronStore struct {
	recordingCronRunWriteStore
	recordingCronTerminalStore
	recordingCronCompatibilityStore
	recordingCronRecoveryStore

	mu                      sync.Mutex
	claimFn                 func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error)
	casStatusFn             func(context.Context, CASRunStatusParams) error
	markFailedFn            func(context.Context, MarkFailedParams) error
	renewLeaseFn            func(context.Context, LeaseParams) error
	listJobsFn              func(context.Context) ([]JobRecord, error)
	listUnresolvedFn        func(context.Context) ([]RunRecord, error)
	getRunningRunByTurnIDFn func(context.Context, string) (RunRecord, error)
	getRunByTurnIDFn        func(context.Context, string) (RunRecord, error)
	listUnresolvedPageFn    func(context.Context, int32, string) ([]RunRecord, error)
	listJobsClaimedByFn     func(context.Context, string) ([]JobRecord, error)

	casCalls []CASRunStatusParams
}

type recordingCronRunWriteStore struct {
	insertRunFn             func(context.Context, InsertRunParams) (RunRecord, error)
	setRunTurnFn            func(context.Context, SetRunTurnParams) error
	setActiveTurnFn         func(context.Context, SetActiveTurnParams) error
	submitRunWithActiveTurn func(context.Context, SubmitRunWithActiveTurnParams) error
}

type recordingCronTerminalStore struct {
	getJobFn       func(context.Context, string) (JobRecord, error)
	markFinishedFn func(context.Context, MarkFinishedParams) error
}

type recordingCronCompatibilityStore struct{}

func (s *recordingCronStore) ClaimDueJobsForUpdate(ctx context.Context, p ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
	if s.claimFn != nil {
		return s.claimFn(ctx, p)
	}
	return nil, nil
}
func (s *recordingCronRunWriteStore) InsertRun(ctx context.Context, p InsertRunParams) (RunRecord, error) {
	if s.insertRunFn != nil {
		return s.insertRunFn(ctx, p)
	}
	return RunRecord{ID: p.ID, JobID: p.JobID, Status: p.Status, DedupeKey: p.DedupeKey}, nil
}
func (s *recordingCronStore) CASRunStatus(ctx context.Context, p CASRunStatusParams) error {
	s.mu.Lock()
	s.casCalls = append(s.casCalls, p)
	s.mu.Unlock()
	if s.casStatusFn != nil {
		return s.casStatusFn(ctx, p)
	}
	return nil
}
func (s *recordingCronRunWriteStore) SetRunTurn(ctx context.Context, p SetRunTurnParams) error {
	if s.setRunTurnFn != nil {
		return s.setRunTurnFn(ctx, p)
	}
	return nil
}
func (s *recordingCronRunWriteStore) SetActiveTurn(ctx context.Context, p SetActiveTurnParams) error {
	if s.setActiveTurnFn != nil {
		return s.setActiveTurnFn(ctx, p)
	}
	return nil
}

func (s *recordingCronRunWriteStore) SubmitRunWithActiveTurn(ctx context.Context, p SubmitRunWithActiveTurnParams) error {
	if s.submitRunWithActiveTurn != nil {
		return s.submitRunWithActiveTurn(ctx, p)
	}
	return nil
}
func (s *recordingCronTerminalStore) MarkFinished(ctx context.Context, p MarkFinishedParams) error {
	if s.markFinishedFn != nil {
		return s.markFinishedFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) MarkFailed(ctx context.Context, p MarkFailedParams) error {
	if s.markFailedFn != nil {
		return s.markFailedFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) RenewLease(ctx context.Context, p LeaseParams) error {
	if s.renewLeaseFn != nil {
		return s.renewLeaseFn(ctx, p)
	}
	return nil
}
func (s *recordingCronTerminalStore) GetJobByID(ctx context.Context, id string) (JobRecord, error) {
	if s.getJobFn != nil {
		return s.getJobFn(ctx, id)
	}
	return JobRecord{}, nil
}

// Unused store methods keep the programmable double compatible with older scheduler tests.
func (recordingCronCompatibilityStore) CreateJob(context.Context, CreateJobParams) (JobRecord, error) {
	return JobRecord{}, nil
}
func (recordingCronCompatibilityStore) DeleteJob(context.Context, string) error { return nil }
func (recordingCronCompatibilityStore) UpdateJobSchedule(context.Context, UpdateJobScheduleParams) error {
	return nil
}
func (recordingCronCompatibilityStore) SetJobEnabled(context.Context, string, bool, time.Time) error {
	return nil
}
func (recordingCronCompatibilityStore) PatchNextRunAt(context.Context, string, time.Time, time.Time) error {
	return nil
}
func (recordingCronCompatibilityStore) ExtendClaim(context.Context, LeaseParams) error { return nil }
func (recordingCronCompatibilityStore) ReleaseClaim(context.Context, string, string, time.Time) error {
	return nil
}
func (s *recordingCronStore) ListUnresolvedRuns(ctx context.Context) ([]RunRecord, error) {
	if s.listUnresolvedFn != nil {
		return s.listUnresolvedFn(ctx)
	}
	return nil, nil
}
func (s *recordingCronStore) GetRunningRunByTurnID(ctx context.Context, turnID string) (RunRecord, error) {
	if s.getRunningRunByTurnIDFn != nil {
		return s.getRunningRunByTurnIDFn(ctx, turnID)
	}
	// Default: delegate to listUnresolvedFn for backwards compat with tests
	// that set up listUnresolvedFn.
	if s.listUnresolvedFn != nil {
		runs, err := s.listUnresolvedFn(ctx)
		if err != nil {
			return RunRecord{}, err
		}
		for _, run := range runs {
			if run.TurnID == turnID && run.Status == statusRunning {
				return run, nil
			}
		}
	}
	return RunRecord{}, ErrStoreJobRunNotFound
}

func (s *recordingCronStore) GetSubmittedOrRunningRunByTurnID(ctx context.Context, turnID string) (RunRecord, error) {
	if s.getRunByTurnIDFn != nil {
		return s.getRunByTurnIDFn(ctx, turnID)
	}
	if s.getRunningRunByTurnIDFn != nil {
		return s.getRunningRunByTurnIDFn(ctx, turnID)
	}
	if s.listUnresolvedFn != nil {
		runs, err := s.listUnresolvedFn(ctx)
		if err != nil {
			return RunRecord{}, err
		}
		for _, run := range runs {
			if run.TurnID == turnID && (run.Status == statusSubmitted || run.Status == statusRunning) {
				return run, nil
			}
		}
	}
	return RunRecord{}, ErrStoreJobRunNotFound
}

func (s *recordingCronStore) ListUnresolvedRunsPage(ctx context.Context, limit int32, cursor string) ([]RunRecord, error) {
	if s.listUnresolvedPageFn != nil {
		return s.listUnresolvedPageFn(ctx, limit, cursor)
	}
	return nil, nil
}

func (s *recordingCronStore) ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]JobRecord, error) {
	if s.listJobsClaimedByFn != nil {
		return s.listJobsClaimedByFn(ctx, claimedBy)
	}
	// Default: delegate to listJobsFn for backwards compat with tests
	// that set up listJobsFn, filtering by claimedBy.
	if s.listJobsFn != nil {
		all, err := s.listJobsFn(ctx)
		if err != nil {
			return nil, err
		}
		var out []JobRecord
		for _, j := range all {
			if j.ClaimedBy == claimedBy && j.ClaimToken != "" {
				out = append(out, j)
			}
		}
		return out, nil
	}
	return nil, nil
}

// programmableSubmitter records calls and lets tests control StartTurn /
// Observe outcomes.
type programmableSubmitter struct {
	startFn   func(context.Context, StartTurnRequest) (StartTurnResult, error)
	lookupFn  func(context.Context, string) (ObservedTurn, error)
	observeFn func(context.Context, string) error

	mu     sync.Mutex
	starts []StartTurnRequest
}

func (p *programmableSubmitter) StartTurn(ctx context.Context, req StartTurnRequest) (StartTurnResult, error) {
	p.mu.Lock()
	p.starts = append(p.starts, req)
	p.mu.Unlock()
	if p.startFn != nil {
		return p.startFn(ctx, req)
	}
	return StartTurnResult{TurnID: "turn-" + req.RunID, ThreadID: "thread-1", AgentID: "agent-1"}, nil
}
func (p *programmableSubmitter) LookupByDedupeKey(ctx context.Context, key string) (ObservedTurn, error) {
	if p.lookupFn != nil {
		return p.lookupFn(ctx, key)
	}
	return ObservedTurn{Found: false}, nil
}
func (p *programmableSubmitter) Observe(ctx context.Context, turnID string) error {
	if p.observeFn != nil {
		return p.observeFn(ctx, turnID)
	}
	return nil
}

// newTestScheduler hands back a scheduler whose clock + id generator are
// pinned so assertions can match exact values.
func newTestScheduler(t *testing.T, store SchedulerStore, submitter TurnSubmitter) *Scheduler {
	t.Helper()
	s := NewScheduler(slog.Default(), store, submitter, SchedulerConfig{ClaimedBy: "test"})
	s.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	// Deterministic monotonic id stream (not globally unique, but
	// enough for tests that only compare structural relations).
	counter := 0
	s.newID = func() string {
		counter++
		return uuidShort(counter)
	}
	return s
}

func uuidShort(n int) string {
	// Stable sequence like "id-0001", "id-0002" ... Longer than a raw
	// int so it's obvious in failure messages.
	return "id-" + itoaPad(n, 4)
}

func itoaPad(n, width int) string {
	out := make([]byte, 0, width)
	for _, d := range []byte{byte(n / 1000 % 10), byte(n / 100 % 10), byte(n / 10 % 10), byte(n % 10)} {
		out = append(out, '0'+d)
	}
	return string(out)
}

// ----- happy path -----

func TestSchedulerDriveJobHappyPath(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{}
	s := newTestScheduler(t, store, sub)

	now := time.Unix(1_700_000_000, 0).UTC()
	job := JobRecord{
		ID:           "job-1",
		Name:         "daily",
		Prompt:       "check",
		ScheduleExpr: "0 9 * * *",
		Timezone:     "UTC",
		Provider:     "codex",
		CWD:          "/repo",
		ClaimToken:   "claim-token-xyz",
		NextRunAt:    now,
	}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}

	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if len(sub.starts) != 1 {
		t.Fatalf("StartTurn should fire once; got %d", len(sub.starts))
	}
	if sub.starts[0].JobID != "job-1" {
		t.Fatalf("StartTurn request job_id = %q", sub.starts[0].JobID)
	}
	if len(store.casCalls) != 2 {
		t.Fatalf("CAS call count = %d, want 2", len(store.casCalls))
	}
	wantPairs := []struct{ exp, next string }{
		{"pending", "submitting"},
		{"submitted", "running"},
	}
	for i, want := range wantPairs {
		got := store.casCalls[i]
		if got.ExpectedStatus != want.exp || got.NextStatus != want.next {
			t.Fatalf("CAS[%d] = (%s -> %s), want (%s -> %s)",
				i, got.ExpectedStatus, got.NextStatus, want.exp, want.next)
		}
	}
}

// TestPersistSubmittedTurnAtomicWithActiveTurn locks run turn, submitted status,
// and job active turn into one durable unit.
func TestPersistSubmittedTurnAtomicWithActiveTurn(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{
		recordingCronRunWriteStore: recordingCronRunWriteStore{
			submitRunWithActiveTurn: func(context.Context, SubmitRunWithActiveTurnParams) error {
				return errors.New("active turn write failed")
			},
		},
	}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	err := s.persistSubmittedTurn(context.Background(),
		JobRecord{ID: "job-1", ClaimToken: "claim-token"},
		RunRecord{ID: "run-1", JobID: "job-1", ScheduledAt: s.now()},
		StartTurnResult{TurnID: "turn-1"})
	if err == nil || !strings.Contains(err.Error(), "active turn write failed") {
		t.Fatalf("persistSubmittedTurn err = %v, want active turn failure", err)
	}
	if len(store.casCalls) != 0 {
		t.Fatalf("submitted status must be atomic with active turn; CAS calls = %+v", store.casCalls)
	}
}

// TestSetActiveTurnFailureDoesNotPublishSubmitted prevents UI/progress
// subscribers from seeing submitted before active_turn_id is durable.
func TestSetActiveTurnFailureDoesNotPublishSubmitted(t *testing.T) {
	t.Parallel()
	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()
	store := &recordingCronStore{
		recordingCronRunWriteStore: recordingCronRunWriteStore{
			submitRunWithActiveTurn: func(context.Context, SubmitRunWithActiveTurnParams) error {
				return errors.New("active turn write failed")
			},
		},
		claimFn: func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
			return []JobRecord{{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", Provider: "codex", CWD: "/repo", ClaimToken: "claim-token"}}, nil
		},
	}
	s := newTestScheduler(t, store, &programmableSubmitter{}).WithDispatcher(dispatcher)
	out, cleanup := collectRunStateEvents(t, dispatcher)
	defer cleanup()
	if err := s.RunTick(context.Background()); err == nil || !strings.Contains(err.Error(), "active turn write failed") {
		t.Fatalf("RunTick err = %v, want active turn failure", err)
	}
	time.Sleep(50 * time.Millisecond)
	for _, ev := range out.get() {
		if ev.Status == statusSubmitted {
			t.Fatalf("submitted event published before active turn was durable: %+v", out.get())
		}
	}
}

// TestTerminalEarlyArrivalDoesNotBecomePermanentStale proves an immediately
// delivered terminal event can finalize a submitted run.
func TestTerminalEarlyArrivalDoesNotBecomePermanentStale(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "claim-token"}
	run := RunRecord{ID: "run-1", JobID: job.ID, Status: statusSubmitting, ScheduledAt: s.now()}
	var terminalErr error
	var finished FinalizeRecoveredRunParams
	store.submitRunWithActiveTurn = func(_ context.Context, p SubmitRunWithActiveTurnParams) error {
		run.TurnID, run.Status = p.ActiveTurnID, statusSubmitted
		job.ActiveTurnID = p.ActiveTurnID
		terminalErr = s.CompleteTurn(context.Background(), p.ActiveTurnID, true, "")
		return nil
	}
	store.getRunByTurnIDFn = func(context.Context, string) (RunRecord, error) { return run, nil }
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) { return []RunRecord{run}, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		finished = p
		return nil
	}

	if err := s.persistSubmittedTurn(context.Background(), job, run, StartTurnResult{TurnID: "turn-1"}); err != nil {
		t.Fatalf("persistSubmittedTurn error = %v", err)
	}
	if terminalErr != nil {
		t.Fatalf("early terminal error = %v, want terminal to observe active turn", terminalErr)
	}
	if finished.ExpectedActiveTurnID != "turn-1" || finished.LastTurnID != "turn-1" {
		t.Fatalf("FinalizeRecoveredRun params = %+v", finished)
	}
}

func TestSchedulerStartTurnFailureMarksFailed(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{
		startFn: func(context.Context, StartTurnRequest) (StartTurnResult, error) {
			return StartTurnResult{}, errors.New("provider down")
		},
	}
	s := newTestScheduler(t, store, sub)

	var failed MarkFailedParams
	store.markFailedFn = func(_ context.Context, p MarkFailedParams) error {
		failed = p
		return nil
	}
	job := JobRecord{
		ID: "job-1", ScheduleExpr: "0 9 * * *", ClaimToken: "tok", NextRunAt: s.now(),
		MaxAttempts: 3,
	}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}
	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if failed.ID != "job-1" || failed.LastStatus != statusFailed {
		t.Fatalf("markFailed params = %+v", failed)
	}
	if failed.LastError != "provider down" {
		t.Fatalf("LastError = %q, want 'provider down'", failed.LastError)
	}
}

func TestSchedulerCorruptSkillsDecodeMarksFailedWithoutStartTurn(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{}
	s := newTestScheduler(t, store, sub)

	var failed MarkFailedParams
	store.markFailedFn = func(_ context.Context, p MarkFailedParams) error {
		failed = p
		return nil
	}
	job := JobRecord{
		ID:           "job-1",
		ScheduleExpr: "0 9 * * *",
		Timezone:     "UTC",
		ClaimToken:   "tok",
		NextRunAt:    s.now(),
		Skills:       json.RawMessage("{bad}"),
		MaxAttempts:  1,
	}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}

	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if len(sub.starts) != 0 {
		t.Fatalf("StartTurn should not run after corrupt skills decode; starts=%d", len(sub.starts))
	}
	if failed.ID != "job-1" || failed.LastStatus != statusFailed {
		t.Fatalf("markFailed params = %+v", failed)
	}
	if !strings.Contains(failed.LastError, "cron: decode skills snapshot") {
		t.Fatalf("LastError = %q, want corrupt skills decode error", failed.LastError)
	}
}

func TestSchedulerObserveFailureMarksObserveLost(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{
		observeFn: func(context.Context, string) error { return ErrTurnNotFound },
	}
	s := newTestScheduler(t, store, sub)

	var failed MarkFailedParams
	store.markFailedFn = func(_ context.Context, p MarkFailedParams) error {
		failed = p
		return nil
	}
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", ClaimToken: "tok", NextRunAt: s.now()}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}
	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if failed.LastStatus != statusObserveLost {
		t.Fatalf("LastStatus = %q, want observe_lost", failed.LastStatus)
	}
	if !failed.NextRetryAt.IsZero() {
		t.Fatalf("observe_lost must not schedule retry; got %v", failed.NextRetryAt)
	}
}

func TestSchedulerDoesNotFinishLongTurnUntilTerminalEvent(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{}
	s := newTestScheduler(t, store, sub)

	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", NextRunAt: s.now()}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}
	finalizeCalls := 0
	store.finalizeRecoveredRunFn = func(context.Context, FinalizeRecoveredRunParams) error {
		finalizeCalls++
		return nil
	}

	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if finalizeCalls != 0 {
		t.Fatalf("long turn was finalized before terminal event; calls=%d", finalizeCalls)
	}
	last := store.casCalls[len(store.casCalls)-1]
	if last.NextStatus != statusRunning {
		t.Fatalf("last status = %s, want running", last.NextStatus)
	}
}

func TestSchedulerTerminalEventMarksFinished(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", NextRunAt: s.now()}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) { return []RunRecord{run}, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	var finished FinalizeRecoveredRunParams
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		finished = p
		return nil
	}

	if err := s.CompleteTurn(context.Background(), "turn-1", true, ""); err != nil {
		t.Fatalf("CompleteTurn error = %v", err)
	}
	if finished.ID != job.ID || finished.RunID != run.ID || finished.ExpectedActiveTurnID != "turn-1" || finished.LastTurnID != "turn-1" {
		t.Fatalf("FinalizeRecoveredRun params = %+v", finished)
	}
	if finished.ExpectedRunStatus != statusRunning || finished.LastStatus != statusFinished {
		t.Fatalf("terminal transition = %s -> %s, want running -> finished", finished.ExpectedRunStatus, finished.LastStatus)
	}
	if len(store.casCalls) != 0 {
		t.Fatalf("terminal finalization must not use a separate CAS: %+v", store.casCalls)
	}
}

func TestCronTerminalEventFinalizesSubmittedRun(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", NextRunAt: s.now()}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusSubmitted, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) { return []RunRecord{run}, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	var finished FinalizeRecoveredRunParams
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		finished = p
		return nil
	}

	if err := s.CompleteTurn(context.Background(), "turn-1", true, ""); err != nil {
		t.Fatalf("CompleteTurn error = %v", err)
	}
	if finished.ID != job.ID || finished.RunID != run.ID || finished.ExpectedActiveTurnID != "turn-1" || finished.LastTurnID != "turn-1" {
		t.Fatalf("FinalizeRecoveredRun params = %+v", finished)
	}
	if finished.ExpectedRunStatus != statusSubmitted || finished.LastStatus != statusFinished {
		t.Fatalf("terminal transition = %s -> %s, want submitted -> finished", finished.ExpectedRunStatus, finished.LastStatus)
	}
	if len(store.casCalls) != 0 {
		t.Fatalf("terminal finalization must not use a separate CAS: %+v", store.casCalls)
	}
}

// TestTerminalRunByTurnIDUsesPointLookup prevents submitted terminal events from scanning all unresolved runs.
func TestTerminalRunByTurnIDUsesPointLookup(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", NextRunAt: s.now()}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusSubmitted, ScheduledAt: s.now()}
	store.getRunByTurnIDFn = func(_ context.Context, turnID string) (RunRecord, error) {
		if turnID != "turn-1" {
			t.Fatalf("turnID = %q, want turn-1", turnID)
		}
		return run, nil
	}
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) {
		t.Fatal("terminal lookup fell back to full unresolved scan")
		return nil, nil
	}
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	store.finalizeRecoveredRunFn = func(context.Context, FinalizeRecoveredRunParams) error { return nil }

	if err := s.CompleteTurn(context.Background(), "turn-1", true, ""); err != nil {
		t.Fatalf("CompleteTurn error = %v", err)
	}
}

func TestSchedulerRejectsStaleTerminalWhenJobActiveTurnMovedToNewClaim(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{
		ID:           "job-1",
		ScheduleExpr: "0 9 * * *",
		Timezone:     "UTC",
		ClaimToken:   "new-token",
		ActiveTurnID: "turn-new",
		NextRunAt:    s.now(),
	}
	run := RunRecord{ID: "run-old", JobID: job.ID, TurnID: "turn-old", Status: statusRunning, ScheduledAt: s.now().Add(-time.Hour)}
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) { return []RunRecord{run}, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		t.Fatalf("stale terminal reached FinalizeRecoveredRun with params %+v", p)
		return nil
	}

	err := s.CompleteTurn(context.Background(), "turn-old", true, "")
	if !errors.Is(err, ErrStoreClaimTokenMismatch) {
		t.Fatalf("CompleteTurn error = %v, want ErrClaimTokenMismatch", err)
	}
	if len(store.casCalls) != 0 {
		t.Fatalf("stale terminal updated run state: %+v", store.casCalls)
	}
}

func TestSchedulerTerminalEventRejectsInvalidScheduleInsteadOfReusingNextRunAt(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{
		ID:           "job-1",
		ScheduleExpr: "not a cron",
		Timezone:     "UTC",
		ClaimToken:   "tok",
		ActiveTurnID: "turn-1",
		NextRunAt:    s.now().Add(24 * time.Hour),
	}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) { return []RunRecord{run}, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		t.Fatalf("FinalizeRecoveredRun reused old next_run_at on invalid schedule: %+v", p)
		return nil
	}

	err := s.CompleteTurn(context.Background(), "turn-1", true, "")
	if err == nil || !strings.Contains(err.Error(), "schedule_expr") {
		t.Fatalf("CompleteTurn error = %v, want schedule_expr validation error", err)
	}
}

func TestSchedulerTerminalEventMarksFailed(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", NextRunAt: s.now(), MaxAttempts: 1}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) { return []RunRecord{run}, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	var failed FinalizeRecoveredRunParams
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		failed = p
		return nil
	}

	if err := s.CompleteTurn(context.Background(), "turn-1", false, "provider failed"); err != nil {
		t.Fatalf("CompleteTurn error = %v", err)
	}
	if !slices.Equal(
		[]string{failed.ID, failed.RunID, failed.ExpectedActiveTurnID, failed.LastTurnID, failed.LastStatus, failed.LastError},
		[]string{job.ID, run.ID, "turn-1", "turn-1", statusFailed, "provider failed"},
	) {
		t.Fatalf("FinalizeRecoveredRun params = %+v", failed)
	}
	if failed.ExpectedRunStatus != statusRunning || failed.LastStatus != statusFailed {
		t.Fatalf("terminal transition = %s -> %s, want running -> failed", failed.ExpectedRunStatus, failed.LastStatus)
	}
	if len(store.casCalls) != 0 {
		t.Fatalf("terminal finalization must not use a separate CAS: %+v", store.casCalls)
	}
}

func TestSchedulerStartFailureRejectsInvalidRetrySchedule(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{
		startFn: func(context.Context, StartTurnRequest) (StartTurnResult, error) {
			return StartTurnResult{}, errors.New("provider down")
		},
	}
	s := newTestScheduler(t, store, sub)
	job := JobRecord{
		ID:           "job-1",
		ScheduleExpr: "not a cron",
		Timezone:     "UTC",
		ClaimToken:   "tok",
		NextRunAt:    s.now().Add(24 * time.Hour),
		MaxAttempts:  3,
	}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}
	store.markFailedFn = func(_ context.Context, p MarkFailedParams) error {
		t.Fatalf("MarkFailed reused old next_run_at on invalid schedule: %+v", p)
		return nil
	}

	err := s.RunTick(context.Background())
	if err == nil || !strings.Contains(err.Error(), "schedule_expr") {
		t.Fatalf("RunTick error = %v, want schedule_expr validation error", err)
	}
}

func TestCronTerminalSubscriberMarksFinished(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", ActiveTurnID: "turn-1", NextRunAt: s.now()}
	run := RunRecord{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: statusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) { return []RunRecord{run}, nil }
	store.getJobFn = func(context.Context, string) (JobRecord, error) { return job, nil }
	finished := make(chan FinalizeRecoveredRunParams, 1)
	store.finalizeRecoveredRunFn = func(_ context.Context, p FinalizeRecoveredRunParams) error {
		finished <- p
		return nil
	}
	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	cancel := NewCronProgressSubscribers(s, nil).Spec.Register(dispatcher)
	defer cancel()

	event.Publish(dispatcher, turndto.TurnCompleted{TurnHeader: cronProgressTurnHeader("thread-1", "turn-1", "agent-1"), Success: true})
	select {
	case got := <-finished:
		if got.LastTurnID != "turn-1" {
			t.Fatalf("LastTurnID = %q", got.LastTurnID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("terminal subscriber did not mark finished")
	}
}

// TestRecoverDanglingRunsProcessesBatches prevents startup recovery from loading all unresolved runs at once.
func TestRecoverDanglingRunsProcessesBatches(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	pageCalls := 0
	store.listUnresolvedFn = func(context.Context) ([]RunRecord, error) {
		t.Fatal("startup recovery used full unresolved scan")
		return nil, nil
	}
	store.listUnresolvedPageFn = func(_ context.Context, limit int32, cursor string) ([]RunRecord, error) {
		if limit <= 0 {
			t.Fatalf("page limit = %d, want positive cap", limit)
		}
		pageCalls++
		if cursor == "" && pageCalls == 1 {
			return []RunRecord{{ID: "run-1", JobID: "job-1", TurnID: "turn-1", Status: statusRunning, ScheduledAt: s.now()}}, nil
		}
		if cursor == "run-1" && pageCalls == 2 {
			return nil, nil
		}
		t.Fatalf("unexpected recovery cursor %q", cursor)
		return nil, nil
	}
	store.getJobFn = func(_ context.Context, id string) (JobRecord, error) {
		return JobRecord{ID: id, ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", LeaseExpiresAt: s.now().Add(time.Hour)}, nil
	}
	if err := s.RecoverDanglingRuns(context.Background()); err != nil {
		t.Fatalf("RecoverDanglingRuns error = %v", err)
	}
	if pageCalls != 2 {
		t.Fatalf("page calls = %d, want first batch and empty batch", pageCalls)
	}
}

// ----- RenewLeases -----

func TestRenewLeasesOnlyBumpsOwnedJobs(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{
		listJobsFn: func(context.Context) ([]JobRecord, error) {
			return []JobRecord{
				{ID: "mine", ClaimedBy: "test", ClaimToken: "t-me"},
				{ID: "theirs", ClaimedBy: "other-scheduler", ClaimToken: "t-other"},
				{ID: "free", ClaimedBy: "", ClaimToken: ""},
			}, nil
		},
	}
	var renewed []string
	store.renewLeaseFn = func(_ context.Context, p LeaseParams) error {
		renewed = append(renewed, p.ID)
		return nil
	}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	if err := s.RenewLeases(context.Background()); err != nil {
		t.Fatalf("RenewLeases error = %v", err)
	}
	if len(renewed) != 1 || renewed[0] != "mine" {
		t.Fatalf("RenewLeases should only bump own jobs, got %v", renewed)
	}
}

// ----- noop submitter behaviour (regression guard) -----

func TestNoopTurnSubmitterFailsFast(t *testing.T) {
	t.Parallel()
	_, err := NoopTurnSubmitter{}.StartTurn(context.Background(), StartTurnRequest{})
	if !errors.Is(err, ErrSubmitterNotWired) {
		t.Fatalf("NoopTurnSubmitter.StartTurn err = %v, want ErrSubmitterNotWired", err)
	}
	err = NoopTurnSubmitter{}.Observe(context.Background(), "turn-1")
	if !errors.Is(err, ErrSubmitterNotWired) {
		t.Fatalf("NoopTurnSubmitter.Observe err = %v, want ErrSubmitterNotWired", err)
	}
	got, err := NoopTurnSubmitter{}.LookupByDedupeKey(context.Background(), "k")
	if err != nil || got.Found {
		t.Fatalf("NoopTurnSubmitter.Lookup unexpected: err=%v got=%+v", err, got)
	}
}
