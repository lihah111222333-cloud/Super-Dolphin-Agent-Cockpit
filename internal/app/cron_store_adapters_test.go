package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/cron"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
	storeadaptertest "github.com/anthropic-ai/super-agent-v3/internal/testutil/storeadapter"
)

type cronStoreTestState struct {
	claimDueJobs             func(context.Context, cronstore.ClaimDueJobsForUpdateParams) ([]cronstore.Job, error)
	insertRun                func(context.Context, cronstore.InsertRunParams) (cronstore.Run, error)
	submitRunWithActiveTurn  func(context.Context, cronstore.SubmitRunWithActiveTurnParams) error
	getSubmittedOrRunningRun func(context.Context, string) (cronstore.Run, error)
	listUnresolvedRunsPage   func(context.Context, int32, string) ([]cronstore.Run, error)
	getJobByID               func(context.Context, string) (cronstore.Job, error)
}

type cronStoreTestDouble struct {
	*cronStoreJobTestDouble
	*cronStoreClaimTestDouble
	*cronStoreRunTestDouble
}

func newCronStoreTestDouble(state *cronStoreTestState) *cronStoreTestDouble {
	if state == nil {
		state = &cronStoreTestState{}
	}
	return &cronStoreTestDouble{
		cronStoreJobTestDouble:   &cronStoreJobTestDouble{state: state},
		cronStoreClaimTestDouble: &cronStoreClaimTestDouble{state: state},
		cronStoreRunTestDouble:   &cronStoreRunTestDouble{state: state},
	}
}

type cronStoreJobTestDouble struct{ state *cronStoreTestState }

func (*cronStoreJobTestDouble) CreateJob(_ context.Context, p cronstore.CreateJobParams) (cronstore.Job, error) {
	return cronstore.Job{ID: p.ID}, nil
}

func (s *cronStoreJobTestDouble) GetJobByID(ctx context.Context, id string) (cronstore.Job, error) {
	if s.state.getJobByID != nil {
		return s.state.getJobByID(ctx, id)
	}
	return cronstore.Job{ID: id}, nil
}

func (*cronStoreJobTestDouble) ListJobs(context.Context) ([]cronstore.Job, error) { return nil, nil }
func (*cronStoreJobTestDouble) DeleteJob(context.Context, string) error           { return nil }
func (*cronStoreJobTestDouble) UpdateJobSchedule(context.Context, cronstore.UpdateJobScheduleParams) error {
	return nil
}
func (*cronStoreJobTestDouble) SetJobEnabled(context.Context, string, bool, time.Time) error {
	return nil
}
func (*cronStoreJobTestDouble) PatchNextRunAt(context.Context, string, time.Time, time.Time) error {
	return nil
}
func (*cronStoreJobTestDouble) ListRunsByJob(context.Context, string, int32) ([]cronstore.Run, error) {
	return nil, nil
}

type cronStoreClaimTestDouble struct{ state *cronStoreTestState }

func (s *cronStoreClaimTestDouble) ClaimDueJobsForUpdate(
	ctx context.Context,
	p cronstore.ClaimDueJobsForUpdateParams,
) ([]cronstore.Job, error) {
	if s.state.claimDueJobs != nil {
		return s.state.claimDueJobs(ctx, p)
	}
	return nil, nil
}

func (*cronStoreClaimTestDouble) RenewLease(context.Context, cronstore.LeaseParams) error {
	return nil
}
func (*cronStoreClaimTestDouble) ExtendClaim(context.Context, cronstore.LeaseParams) error {
	return nil
}
func (*cronStoreClaimTestDouble) ReleaseClaim(context.Context, string, string, time.Time) error {
	return nil
}
func (*cronStoreClaimTestDouble) MarkFinished(context.Context, cronstore.MarkFinishedParams) error {
	return nil
}
func (*cronStoreClaimTestDouble) MarkFailed(context.Context, cronstore.MarkFailedParams) error {
	return nil
}
func (*cronStoreClaimTestDouble) SetActiveTurn(context.Context, cronstore.SetActiveTurnParams) error {
	return nil
}
func (*cronStoreClaimTestDouble) ListJobsClaimedBy(context.Context, string) ([]cronstore.Job, error) {
	return nil, nil
}

type cronStoreRunTestDouble struct{ state *cronStoreTestState }

func (s *cronStoreRunTestDouble) SubmitRunWithActiveTurn(
	ctx context.Context,
	p cronstore.SubmitRunWithActiveTurnParams,
) error {
	if s.state.submitRunWithActiveTurn != nil {
		return s.state.submitRunWithActiveTurn(ctx, p)
	}
	return nil
}

func (s *cronStoreRunTestDouble) InsertRun(ctx context.Context, p cronstore.InsertRunParams) (cronstore.Run, error) {
	if s.state.insertRun != nil {
		return s.state.insertRun(ctx, p)
	}
	return cronstore.Run{ID: p.ID, JobID: p.JobID, Status: p.Status}, nil
}

func (*cronStoreRunTestDouble) CASRunStatus(context.Context, cronstore.CASRunStatusParams) error {
	return nil
}
func (*cronStoreRunTestDouble) SetRunTurn(context.Context, cronstore.SetRunTurnParams) error {
	return nil
}
func (*cronStoreRunTestDouble) GetRunByID(context.Context, string) (cronstore.Run, error) {
	return cronstore.Run{}, cronstore.ErrJobRunNotFound
}
func (*cronStoreRunTestDouble) GetRunByDedupeKey(context.Context, string) (cronstore.Run, error) {
	return cronstore.Run{}, cronstore.ErrJobRunNotFound
}
func (*cronStoreRunTestDouble) ListUnresolvedRuns(context.Context) ([]cronstore.Run, error) {
	return nil, nil
}
func (s *cronStoreRunTestDouble) ListUnresolvedRunsPage(
	ctx context.Context,
	limit int32,
	cursor string,
) ([]cronstore.Run, error) {
	if s.state.listUnresolvedRunsPage != nil {
		return s.state.listUnresolvedRunsPage(ctx, limit, cursor)
	}
	return nil, nil
}
func (*cronStoreRunTestDouble) GetRunningRunByTurnID(context.Context, string) (cronstore.Run, error) {
	return cronstore.Run{}, cronstore.ErrJobRunNotFound
}
func (s *cronStoreRunTestDouble) GetSubmittedOrRunningRunByTurnID(
	ctx context.Context,
	turnID string,
) (cronstore.Run, error) {
	if s.state.getSubmittedOrRunningRun != nil {
		return s.state.getSubmittedOrRunningRun(ctx, turnID)
	}
	return cronstore.Run{}, cronstore.ErrJobRunNotFound
}

var _ cronstore.Store = (*cronStoreTestDouble)(nil)

type cronSubmittedTurnStoreMirror interface {
	SubmitRunWithActiveTurn(context.Context, cron.SubmitRunWithActiveTurnParams) error
}

type cronTerminalRunLookupStoreMirror interface {
	GetSubmittedOrRunningRunByTurnID(context.Context, string) (cron.RunRecord, error)
}

type cronUnresolvedRunsPageStoreMirror interface {
	ListUnresolvedRunsPage(context.Context, int32, string) ([]cron.RunRecord, error)
}

var _ cronSubmittedTurnStoreMirror = (*cronSchedulerStoreAdapter)(nil)
var _ cronTerminalRunLookupStoreMirror = (*cronSchedulerStoreAdapter)(nil)
var _ cronUnresolvedRunsPageStoreMirror = (*cronSchedulerStoreAdapter)(nil)

// TestCronStoreAdapterRequiredConstructor 固定 cron Store 是 required 依赖且由 App 统一投影两个领域端口。
func TestCronStoreAdapterRequiredConstructor(t *testing.T) {
	if _, err := newCronStoreAdapter(nil); !errors.Is(err, errCronStoreAdapterMissing) {
		t.Fatalf("expected nil cron Store constructor error, got %v", err)
	}
	var typedNil *cronStoreTestDouble
	if _, err := newCronStoreAdapter(typedNil); !errors.Is(err, errCronStoreAdapterMissing) {
		t.Fatalf("expected typed nil cron Store constructor error, got %v", err)
	}
	if _, err := provideCronStore(nil); !errors.Is(err, errCronStoreAdapterMissing) {
		t.Fatalf("expected nil root cron Store projection error, got %v", err)
	}
	if _, err := provideCronSchedulerStore(nil); !errors.Is(err, errCronStoreAdapterMissing) {
		t.Fatalf("expected nil root scheduler Store projection error, got %v", err)
	}
}

// TestCronStoreAdapterProjectsSharedRoot 固定两个领域端口来自同一个 required root adapter。
func TestCronStoreAdapterProjectsSharedRoot(t *testing.T) {
	root, err := newCronStoreAdapter(newCronStoreTestDouble(nil))
	if err != nil {
		t.Fatalf("construct cron root adapter: %v", err)
	}
	storePort, err := provideCronStore(root)
	if err != nil {
		t.Fatalf("provide cron Store: %v", err)
	}
	schedulerPort, err := provideCronSchedulerStore(root)
	if err != nil {
		t.Fatalf("provide cron SchedulerStore: %v", err)
	}
	if storePort != root.jobs || schedulerPort != root.scheduler {
		t.Fatal("expected both cron ports to project the shared root adapter")
	}
}

// TestCronStoreAdapterFieldCoverage 用 one-hot 输入覆盖所有领域 Params 与 Job/Run 记录字段。
func TestCronStoreAdapterFieldCoverage(t *testing.T) {
	t.Run("create_job_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronCreateJobParams)
	})
	t.Run("update_job_schedule_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronUpdateJobScheduleParams)
	})
	t.Run("claim_due_jobs_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronClaimDueJobsForUpdateParams)
	})
	t.Run("lease_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronLeaseParams)
	})
	t.Run("mark_finished_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronMarkFinishedParams)
	})
	t.Run("mark_failed_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronMarkFailedParams)
	})
	t.Run("set_active_turn_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronSetActiveTurnParams)
	})
	t.Run("submit_run_with_active_turn_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronSubmitRunWithActiveTurnParams)
	})
	t.Run("insert_run_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronInsertRunParams)
	})
	t.Run("cas_run_status_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronCASRunStatusParams)
	})
	t.Run("set_run_turn_params", func(t *testing.T) {
		assertCronStoreFieldsMap(t, toStoreCronSetRunTurnParams)
	})
	t.Run("job_record", func(t *testing.T) {
		assertCronStoreFieldsMap(t, fromStoreJob)
	})
	t.Run("run_record", func(t *testing.T) {
		assertCronStoreFieldsMap(t, fromStoreRun)
	})
}

func assertCronStoreFieldsMap[Source, Target any](t *testing.T, mapper func(Source) Target) {
	t.Helper()
	storeadaptertest.AssertFieldsMapE(t, func(source Source) (Target, error) {
		return mapper(source), nil
	})
}

// TestCronStoreAdapterCopiesJobJSON 固定 Config/Skills 在领域到 Store 及 Store 到领域两向均复制。
func TestCronStoreAdapterCopiesJobJSON(t *testing.T) {
	t.Run("domain_to_store", func(t *testing.T) {
		config := json.RawMessage(`{"mode":"safe"}`)
		skills := json.RawMessage(`["review"]`)
		stored := toStoreCronCreateJobParams(cron.CreateJobParams{Config: config, Skills: skills})
		stored.Config[0] = '['
		stored.Skills[0] = '{'
		if string(config) != `{"mode":"safe"}` || string(skills) != `["review"]` {
			t.Fatalf("domain JSON shared with Store DTO: config=%s skills=%s", config, skills)
		}

		updated := toStoreCronUpdateJobScheduleParams(cron.UpdateJobScheduleParams{Config: config, Skills: skills})
		updated.Config[0] = '['
		updated.Skills[0] = '{'
		if string(config) != `{"mode":"safe"}` || string(skills) != `["review"]` {
			t.Fatalf("update JSON shared with Store DTO: config=%s skills=%s", config, skills)
		}
	})
	t.Run("store_to_domain", func(t *testing.T) {
		config := json.RawMessage(`{"mode":"safe"}`)
		skills := json.RawMessage(`["review"]`)
		record := fromStoreJob(cronstore.Job{Config: config, Skills: skills})
		config[0] = '['
		skills[0] = '{'
		if string(record.Config) != `{"mode":"safe"}` || string(record.Skills) != `["review"]` {
			t.Fatalf("Store JSON shared with domain record: config=%s skills=%s", record.Config, record.Skills)
		}
	})
}

// TestCronStoreAdapterErrorMapping 固定八类 Store sentinel 的领域映射和普通错误身份。
func TestCronStoreAdapterErrorMapping(t *testing.T) {
	tests := []struct {
		name       string
		storeError error
		domain     error
	}{
		{name: "job_not_found", storeError: cronstore.ErrJobNotFound, domain: cron.ErrStoreJobNotFound},
		{name: "run_not_found", storeError: cronstore.ErrJobRunNotFound, domain: cron.ErrStoreJobRunNotFound},
		{name: "claim_token", storeError: cronstore.ErrClaimTokenMismatch, domain: cron.ErrStoreClaimTokenMismatch},
		{name: "status_transition", storeError: cronstore.ErrStatusTransitionRefused, domain: cron.ErrStoreStatusTransitionRefused},
		{name: "empty_id", storeError: cronstore.ErrEmptyID, domain: cron.ErrStoreEmptyID},
		{name: "empty_cwd", storeError: cronstore.ErrEmptyCWD, domain: cron.ErrStoreEmptyCWD},
		{name: "empty_provider", storeError: cronstore.ErrEmptyProvider, domain: cron.ErrStoreEmptyProvider},
		{name: "empty_schedule", storeError: cronstore.ErrEmptyScheduleExpr, domain: cron.ErrStoreEmptyScheduleExpr},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("Store operation: %w", tc.storeError)
			got := mapCronStoreError(wrapped)
			if !errors.Is(got, tc.domain) {
				t.Fatalf("expected domain sentinel %v, got %v", tc.domain, got)
			}
			if errors.Is(got, tc.storeError) {
				t.Fatalf("Store sentinel leaked through cron boundary: %v", got)
			}
			wantMessage := fmt.Sprintf("%v: %v", tc.domain, wrapped)
			if got.Error() != wantMessage {
				t.Fatalf("mapped message = %q, want %q", got.Error(), wantMessage)
			}
		})
	}

	sentinel := errors.New("cron ordinary store sentinel")
	wrapped := fmt.Errorf("ordinary Store operation: %w", sentinel)
	if got := mapCronStoreError(wrapped); got != wrapped || !errors.Is(got, sentinel) {
		t.Fatalf("ordinary Store error identity lost: got %v", got)
	}
}

// TestCronStoreAdapterRealMethodUsesErrorMapping 证明真实 adapter 方法使用统一 mapper。
func TestCronStoreAdapterRealMethodUsesErrorMapping(t *testing.T) {
	state := &cronStoreTestState{getJobByID: func(context.Context, string) (cronstore.Job, error) {
		return cronstore.Job{}, fmt.Errorf("real adapter lookup: %w", cronstore.ErrJobNotFound)
	}}
	root, err := newCronStoreAdapter(newCronStoreTestDouble(state))
	if err != nil {
		t.Fatalf("construct cron root adapter: %v", err)
	}
	storePort, err := provideCronStore(root)
	if err != nil {
		t.Fatalf("provide cron Store: %v", err)
	}
	_, err = storePort.GetJobByID(context.Background(), "missing")
	if !errors.Is(err, cron.ErrStoreJobNotFound) || errors.Is(err, cronstore.ErrJobNotFound) {
		t.Fatalf("real adapter did not apply cron error mapping: %v", err)
	}
}

// TestCronSchedulerAdapterImplicitCapabilities 固定真实 scheduler concrete 可被 cron 的三个隐式能力消费。
func TestCronSchedulerAdapterImplicitCapabilities(t *testing.T) {
	t.Run("submitted_turn_store", testCronSubmittedTurnStoreCapability)
	t.Run("terminal_run_lookup", testCronTerminalRunLookupCapability)
	t.Run("unresolved_runs_page", testCronUnresolvedRunsPageCapability)
}

func testCronSubmittedTurnStoreCapability(t *testing.T) {
	var captured cronstore.SubmitRunWithActiveTurnParams
	state := &cronStoreTestState{submitRunWithActiveTurn: func(
		_ context.Context,
		params cronstore.SubmitRunWithActiveTurnParams,
	) error {
		captured = params
		return nil
	}}
	root, err := newCronStoreAdapter(newCronStoreTestDouble(state))
	if err != nil {
		t.Fatalf("construct cron root adapter: %v", err)
	}
	port, ok := any(root.scheduler).(cronSubmittedTurnStoreMirror)
	if !ok {
		t.Fatal("cron scheduler adapter lacks atomic submitted-turn capability")
	}
	params := cron.SubmitRunWithActiveTurnParams{
		RunID: "run-1", JobID: "job-1", ClaimToken: "claim-1", ActiveTurnID: "turn-1",
		ThreadID: "thread-1", AgentID: "agent-1", SubmittedAt: time.Unix(41, 0), Now: time.Unix(42, 0),
	}
	if err := port.SubmitRunWithActiveTurn(context.Background(), params); err != nil {
		t.Fatalf("submit run with active turn: %v", err)
	}
	if want := toStoreCronSubmitRunWithActiveTurnParams(params); captured != want {
		t.Fatalf("submitted-turn params = %#v, want %#v", captured, want)
	}
}

func testCronTerminalRunLookupCapability(t *testing.T) {
	storeRow := cronstore.Run{ID: "run-1", JobID: "job-1", TurnID: "turn-1", Status: "submitted"}
	state := &cronStoreTestState{getSubmittedOrRunningRun: func(
		_ context.Context,
		turnID string,
	) (cronstore.Run, error) {
		if turnID != "turn-1" {
			t.Fatalf("turnID = %q", turnID)
		}
		return storeRow, nil
	}}
	root, err := newCronStoreAdapter(newCronStoreTestDouble(state))
	if err != nil {
		t.Fatalf("construct cron root adapter: %v", err)
	}
	port, ok := any(root.scheduler).(cronTerminalRunLookupStoreMirror)
	if !ok {
		t.Fatal("cron scheduler adapter lacks terminal-run lookup capability")
	}
	got, err := port.GetSubmittedOrRunningRunByTurnID(context.Background(), "turn-1")
	if err != nil {
		t.Fatalf("lookup terminal run: %v", err)
	}
	if want := fromStoreRun(storeRow); got != want {
		t.Fatalf("terminal run = %#v, want %#v", got, want)
	}
}

func testCronUnresolvedRunsPageCapability(t *testing.T) {
	rows := []cronstore.Run{{ID: "run-1"}, {ID: "run-2"}}
	state := &cronStoreTestState{listUnresolvedRunsPage: func(
		_ context.Context,
		limit int32,
		cursor string,
	) ([]cronstore.Run, error) {
		if limit != 128 || cursor != "cursor-1" {
			t.Fatalf("page args = (%d, %q)", limit, cursor)
		}
		return rows, nil
	}}
	root, err := newCronStoreAdapter(newCronStoreTestDouble(state))
	if err != nil {
		t.Fatalf("construct cron root adapter: %v", err)
	}
	port, ok := any(root.scheduler).(cronUnresolvedRunsPageStoreMirror)
	if !ok {
		t.Fatal("cron scheduler adapter lacks paged recovery capability")
	}
	got, err := port.ListUnresolvedRunsPage(context.Background(), 128, "cursor-1")
	if err != nil {
		t.Fatalf("list unresolved runs page: %v", err)
	}
	rows[0].ID = "mutated"
	if len(got) != 2 || got[0].ID != "run-1" || got[1].ID != "run-2" {
		t.Fatalf("paged runs were not independently mapped: %#v", got)
	}
}
