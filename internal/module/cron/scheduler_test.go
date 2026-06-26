package cron

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

// recordingCronStore is a programmable double for cronstore.Store. Only
// the methods the scheduler calls have first-class slots; everything
// else returns zero-values so lint / compile stays quiet.
type recordingCronStore struct {
	mu                      sync.Mutex
	claimFn                 func(context.Context, cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error)
	insertRunFn             func(context.Context, cronstore.InsertRunParams) (cronstore.Run, error)
	casStatusFn             func(context.Context, cronstore.CASRunStatusParams) error
	setRunTurnFn            func(context.Context, cronstore.SetRunTurnParams) error
	setActiveTurnFn         func(context.Context, cronstore.SetActiveTurnParams) error
	markFinishedFn          func(context.Context, cronstore.MarkFinishedParams) error
	markFailedFn            func(context.Context, cronstore.MarkFailedParams) error
	renewLeaseFn            func(context.Context, cronstore.LeaseParams) error
	listJobsFn              func(context.Context) ([]cronstore.Job, error)
	getJobFn                func(context.Context, string) (cronstore.Job, error)
	listUnresolvedFn        func(context.Context) ([]cronstore.Run, error)
	getRunningRunByTurnIDFn func(context.Context, string) (cronstore.Run, error)
	listJobsClaimedByFn     func(context.Context, string) ([]cronstore.Job, error)

	casCalls []cronstore.CASRunStatusParams
}

func (s *recordingCronStore) ClaimDueJobsForUpdate(ctx context.Context, p cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error) {
	if s.claimFn != nil {
		return s.claimFn(ctx, p)
	}
	return nil, nil
}
func (s *recordingCronStore) InsertRun(ctx context.Context, p cronstore.InsertRunParams) (cronstore.Run, error) {
	if s.insertRunFn != nil {
		return s.insertRunFn(ctx, p)
	}
	return cronstore.Run{ID: p.ID, JobID: p.JobID, Status: p.Status, DedupeKey: p.DedupeKey}, nil
}
func (s *recordingCronStore) CASRunStatus(ctx context.Context, p cronstore.CASRunStatusParams) error {
	s.mu.Lock()
	s.casCalls = append(s.casCalls, p)
	s.mu.Unlock()
	if s.casStatusFn != nil {
		return s.casStatusFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) SetRunTurn(ctx context.Context, p cronstore.SetRunTurnParams) error {
	if s.setRunTurnFn != nil {
		return s.setRunTurnFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) SetActiveTurn(ctx context.Context, p cronstore.SetActiveTurnParams) error {
	if s.setActiveTurnFn != nil {
		return s.setActiveTurnFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) MarkFinished(ctx context.Context, p cronstore.MarkFinishedParams) error {
	if s.markFinishedFn != nil {
		return s.markFinishedFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) MarkFailed(ctx context.Context, p cronstore.MarkFailedParams) error {
	if s.markFailedFn != nil {
		return s.markFailedFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) RenewLease(ctx context.Context, p cronstore.LeaseParams) error {
	if s.renewLeaseFn != nil {
		return s.renewLeaseFn(ctx, p)
	}
	return nil
}
func (s *recordingCronStore) ListJobs(ctx context.Context) ([]cronstore.Job, error) {
	if s.listJobsFn != nil {
		return s.listJobsFn(ctx)
	}
	return nil, nil
}

// Unused store methods (not called by scheduler/actor logic in phase 2b).
func (s *recordingCronStore) CreateJob(context.Context, cronstore.CreateJobParams) (cronstore.Job, error) {
	return cronstore.Job{}, nil
}
func (s *recordingCronStore) GetJobByID(ctx context.Context, id string) (cronstore.Job, error) {
	if s.getJobFn != nil {
		return s.getJobFn(ctx, id)
	}
	return cronstore.Job{}, nil
}
func (s *recordingCronStore) DeleteJob(context.Context, string) error { return nil }
func (s *recordingCronStore) UpdateJobSchedule(context.Context, cronstore.UpdateJobScheduleParams) error {
	return nil
}
func (s *recordingCronStore) SetJobEnabled(context.Context, string, bool, time.Time) error {
	return nil
}
func (s *recordingCronStore) PatchNextRunAt(context.Context, string, time.Time, time.Time) error {
	return nil
}
func (s *recordingCronStore) ExtendClaim(context.Context, cronstore.LeaseParams) error { return nil }
func (s *recordingCronStore) ReleaseClaim(context.Context, string, string, time.Time) error {
	return nil
}
func (s *recordingCronStore) GetRunByID(context.Context, string) (cronstore.Run, error) {
	return cronstore.Run{}, nil
}
func (s *recordingCronStore) GetRunByDedupeKey(context.Context, string) (cronstore.Run, error) {
	return cronstore.Run{}, nil
}
func (s *recordingCronStore) ListRunsByJob(context.Context, string, int32) ([]cronstore.Run, error) {
	return nil, nil
}
func (s *recordingCronStore) ListUnresolvedRuns(ctx context.Context) ([]cronstore.Run, error) {
	if s.listUnresolvedFn != nil {
		return s.listUnresolvedFn(ctx)
	}
	return nil, nil
}
func (s *recordingCronStore) GetRunningRunByTurnID(ctx context.Context, turnID string) (cronstore.Run, error) {
	if s.getRunningRunByTurnIDFn != nil {
		return s.getRunningRunByTurnIDFn(ctx, turnID)
	}
	// Default: delegate to listUnresolvedFn for backwards compat with tests
	// that set up listUnresolvedFn.
	if s.listUnresolvedFn != nil {
		runs, err := s.listUnresolvedFn(ctx)
		if err != nil {
			return cronstore.Run{}, err
		}
		for _, run := range runs {
			if run.TurnID == turnID && run.Status == cronstore.StatusRunning {
				return run, nil
			}
		}
	}
	return cronstore.Run{}, cronstore.ErrJobRunNotFound
}
func (s *recordingCronStore) ListJobsClaimedBy(ctx context.Context, claimedBy string) ([]cronstore.Job, error) {
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
		var out []cronstore.Job
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
func newTestScheduler(t *testing.T, store cronstore.Store, submitter TurnSubmitter) *Scheduler {
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
	job := cronstore.Job{
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
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
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
	// Expected CAS transitions stop at submitted->running. A terminal bus event,
	// not Observe(), is responsible for running->finished/failed.
	if len(store.casCalls) != 3 {
		t.Fatalf("CAS call count = %d, want 3", len(store.casCalls))
	}
	wantPairs := []struct{ exp, next string }{
		{"pending", "submitting"},
		{"submitting", "submitted"},
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

func TestSchedulerStartTurnFailureMarksFailed(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{
		startFn: func(context.Context, StartTurnRequest) (StartTurnResult, error) {
			return StartTurnResult{}, errors.New("provider down")
		},
	}
	s := newTestScheduler(t, store, sub)

	var failed cronstore.MarkFailedParams
	store.markFailedFn = func(_ context.Context, p cronstore.MarkFailedParams) error {
		failed = p
		return nil
	}
	job := cronstore.Job{
		ID: "job-1", ScheduleExpr: "0 9 * * *", ClaimToken: "tok", NextRunAt: s.now(),
		MaxAttempts: 3,
	}
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
	}
	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if failed.ID != "job-1" || failed.LastStatus != cronstore.StatusFailed {
		t.Fatalf("markFailed params = %+v", failed)
	}
	if failed.LastError != "provider down" {
		t.Fatalf("LastError = %q, want 'provider down'", failed.LastError)
	}
}

func TestSchedulerObserveFailureMarksObserveLost(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	sub := &programmableSubmitter{
		observeFn: func(context.Context, string) error { return ErrTurnNotFound },
	}
	s := newTestScheduler(t, store, sub)

	var failed cronstore.MarkFailedParams
	store.markFailedFn = func(_ context.Context, p cronstore.MarkFailedParams) error {
		failed = p
		return nil
	}
	job := cronstore.Job{ID: "job-1", ScheduleExpr: "0 9 * * *", ClaimToken: "tok", NextRunAt: s.now()}
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
	}
	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if failed.LastStatus != cronstore.StatusObserveLost {
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

	job := cronstore.Job{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", NextRunAt: s.now()}
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
	}
	markFinishedCalls := 0
	store.markFinishedFn = func(context.Context, cronstore.MarkFinishedParams) error {
		markFinishedCalls++
		return nil
	}

	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick error = %v", err)
	}
	if markFinishedCalls != 0 {
		t.Fatalf("long turn was marked finished before terminal event; calls=%d", markFinishedCalls)
	}
	last := store.casCalls[len(store.casCalls)-1]
	if last.NextStatus != cronstore.StatusRunning {
		t.Fatalf("last status = %s, want running", last.NextStatus)
	}
}

func TestSchedulerTerminalEventMarksFinished(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := cronstore.Job{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", NextRunAt: s.now()}
	run := cronstore.Run{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: cronstore.StatusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]cronstore.Run, error) { return []cronstore.Run{run}, nil }
	store.getJobFn = func(context.Context, string) (cronstore.Job, error) { return job, nil }
	var finished cronstore.MarkFinishedParams
	store.markFinishedFn = func(_ context.Context, p cronstore.MarkFinishedParams) error {
		finished = p
		return nil
	}

	if err := s.CompleteTurn(context.Background(), "turn-1", true, ""); err != nil {
		t.Fatalf("CompleteTurn error = %v", err)
	}
	if finished.ID != job.ID || finished.LastTurnID != "turn-1" {
		t.Fatalf("MarkFinished params = %+v", finished)
	}
	last := store.casCalls[len(store.casCalls)-1]
	if last.ExpectedStatus != cronstore.StatusRunning || last.NextStatus != cronstore.StatusFinished {
		t.Fatalf("CAS = %s -> %s, want running -> finished", last.ExpectedStatus, last.NextStatus)
	}
}

func TestSchedulerTerminalEventRejectsInvalidScheduleInsteadOfReusingNextRunAt(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{}
	s := newTestScheduler(t, store, &programmableSubmitter{})
	job := cronstore.Job{
		ID:           "job-1",
		ScheduleExpr: "not a cron",
		Timezone:     "UTC",
		ClaimToken:   "tok",
		NextRunAt:    s.now().Add(24 * time.Hour),
	}
	run := cronstore.Run{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: cronstore.StatusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]cronstore.Run, error) { return []cronstore.Run{run}, nil }
	store.getJobFn = func(context.Context, string) (cronstore.Job, error) { return job, nil }
	store.markFinishedFn = func(_ context.Context, p cronstore.MarkFinishedParams) error {
		t.Fatalf("MarkFinished reused old next_run_at on invalid schedule: %+v", p)
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
	job := cronstore.Job{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", NextRunAt: s.now(), MaxAttempts: 1}
	run := cronstore.Run{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: cronstore.StatusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]cronstore.Run, error) { return []cronstore.Run{run}, nil }
	store.getJobFn = func(context.Context, string) (cronstore.Job, error) { return job, nil }
	var failed cronstore.MarkFailedParams
	store.markFailedFn = func(_ context.Context, p cronstore.MarkFailedParams) error {
		failed = p
		return nil
	}

	if err := s.CompleteTurn(context.Background(), "turn-1", false, "provider failed"); err != nil {
		t.Fatalf("CompleteTurn error = %v", err)
	}
	if failed.ID != job.ID || failed.LastTurnID != "turn-1" || failed.LastStatus != cronstore.StatusFailed || failed.LastError != "provider failed" {
		t.Fatalf("MarkFailed params = %+v", failed)
	}
	last := store.casCalls[len(store.casCalls)-1]
	if last.ExpectedStatus != cronstore.StatusRunning || last.NextStatus != cronstore.StatusFailed {
		t.Fatalf("CAS = %s -> %s, want running -> failed", last.ExpectedStatus, last.NextStatus)
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
	job := cronstore.Job{
		ID:           "job-1",
		ScheduleExpr: "not a cron",
		Timezone:     "UTC",
		ClaimToken:   "tok",
		NextRunAt:    s.now().Add(24 * time.Hour),
		MaxAttempts:  3,
	}
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
	}
	store.markFailedFn = func(_ context.Context, p cronstore.MarkFailedParams) error {
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
	job := cronstore.Job{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", ClaimToken: "tok", NextRunAt: s.now()}
	run := cronstore.Run{ID: "run-1", JobID: job.ID, TurnID: "turn-1", Status: cronstore.StatusRunning, ScheduledAt: s.now()}
	store.listUnresolvedFn = func(context.Context) ([]cronstore.Run, error) { return []cronstore.Run{run}, nil }
	store.getJobFn = func(context.Context, string) (cronstore.Job, error) { return job, nil }
	finished := make(chan cronstore.MarkFinishedParams, 1)
	store.markFinishedFn = func(_ context.Context, p cronstore.MarkFinishedParams) error {
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

// ----- RenewLeases -----

func TestRenewLeasesOnlyBumpsOwnedJobs(t *testing.T) {
	t.Parallel()
	store := &recordingCronStore{
		listJobsFn: func(context.Context) ([]cronstore.Job, error) {
			return []cronstore.Job{
				{ID: "mine", ClaimedBy: "test", ClaimToken: "t-me"},
				{ID: "theirs", ClaimedBy: "other-scheduler", ClaimToken: "t-other"},
				{ID: "free", ClaimedBy: "", ClaimToken: ""},
			}, nil
		},
	}
	var renewed []string
	store.renewLeaseFn = func(_ context.Context, p cronstore.LeaseParams) error {
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
