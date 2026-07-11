package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	orchcron "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/cron"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/taskdag"
)

func TestStartDAG_ScheduledAdapterPassesIdempotencyKey(t *testing.T) {
	dueAt := time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)
	nextRunAt := dueAt.Add(time.Hour)
	runStore := scheduledStartRunStore(dueAt)
	svc := makeStartDAGService(&stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}, runStore)

	err := svc.StartScheduledDAG(context.Background(), scheduledStartRequest(dueAt, nextRunAt))
	if err != nil {
		t.Fatalf("StartDAG() error = %v, want nil", err)
	}
	if len(runStore.createCalls) != 1 {
		t.Fatalf("CreateRun calls = %d, want 1", len(runStore.createCalls))
	}
	got := runStore.createCalls[0]
	if got.RunKey != "dag-1#run-scheduled:dag-1:2026-05-11T07:00:00Z" {
		t.Fatalf("RunKey = %q, want scheduled idempotency key in run key", got.RunKey)
	}
	if got.TriggerSource != "scheduled" {
		t.Fatalf("TriggerSource = %q, want scheduled", got.TriggerSource)
	}
	assertScheduledNextRunAdvanced(t, runStore, dueAt, nextRunAt)
}

func TestStartScheduledDAGRejectsStaleScheduleBeforeCreateRun(t *testing.T) {
	dueAt := time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)
	runStore := scheduledStartRunStore(dueAt.Add(time.Minute))
	svc := makeStartDAGService(&stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}, runStore)

	err := svc.StartScheduledDAG(context.Background(), scheduledStartRequest(dueAt, dueAt.Add(time.Hour)))
	if !errors.Is(err, orchcron.ErrScheduleStateChanged) {
		t.Fatalf("StartScheduledDAG() error = %v, want ErrScheduleStateChanged", err)
	}
	if len(runStore.createCalls) != 0 {
		t.Fatalf("CreateRun calls = %d, want none after stale scheduled due slot", len(runStore.createCalls))
	}
	if len(runStore.updateNextRunCalls) != 0 {
		t.Fatalf("UpdateScheduledDAGNextRun calls = %d, want none after stale scheduled due slot", len(runStore.updateNextRunCalls))
	}
}

func TestStartScheduledDAGRejectsMissingScheduledTriggerSource(t *testing.T) {
	dueAt := time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)
	runStore := scheduledStartRunStore(dueAt)
	svc := makeStartDAGService(&stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}, runStore)
	req := scheduledStartRequest(dueAt, dueAt.Add(time.Hour))
	req.TriggerSource = ""

	err := svc.StartScheduledDAG(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "trigger_source must be scheduled") {
		t.Fatalf("StartScheduledDAG() error = %v, want trigger_source fail-fast", err)
	}
	if len(runStore.createCalls) != 0 {
		t.Fatalf("CreateRun calls = %d, want none after invalid trigger_source", len(runStore.createCalls))
	}
}

func TestStartScheduledDAGRejectsReadyRootsWithoutWakeups(t *testing.T) {
	dueAt := time.Date(2026, 5, 11, 7, 0, 0, 0, time.UTC)
	nextRunAt := dueAt.Add(time.Hour)
	runStore := scheduledStartRunStore(dueAt)
	runStore.promoteRows = 1
	runStore.scheduleRootWakeupsRows = 0
	svc := makeStartDAGService(&stubStartDAGStore{dag: &taskdag.DAG{DagKey: "dag-1"}}, runStore)

	err := svc.StartScheduledDAG(context.Background(), scheduledStartRequest(dueAt, nextRunAt))
	if err == nil || !strings.Contains(err.Error(), "scheduled wakeups=0") {
		t.Fatalf("StartScheduledDAG() error = %v, want scheduled wakeups=0 fail-fast", err)
	}
	if len(runStore.updateNextRunCalls) != 0 {
		t.Fatalf("UpdateScheduledDAGNextRun calls = %d, want none after blocked scheduled start", len(runStore.updateNextRunCalls))
	}
}

func scheduledStartRunStore(nextRunAt time.Time) *stubRunStore {
	return &stubRunStore{
		lockedDAG: &taskdag.DAG{
			DagKey:    "dag-1",
			Version:   4,
			Trigger:   "scheduled",
			CronExpr:  "0 7 * * *",
			NextRunAt: &nextRunAt,
		},
	}
}

func scheduledStartRequest(dueAt, nextRunAt time.Time) orchcron.ScheduledDAGStartRequest {
	return orchcron.ScheduledDAGStartRequest{
		DagKey:         "dag-1",
		TriggerSource:  "scheduled",
		IdempotencyKey: "scheduled:dag-1:" + dueAt.Format(time.RFC3339Nano),
		DueAt:          dueAt,
		NextRunAt:      nextRunAt,
	}
}

func assertScheduledNextRunAdvanced(t *testing.T, runStore *stubRunStore, dueAt, nextRunAt time.Time) {
	t.Helper()
	if len(runStore.updateNextRunCalls) != 1 {
		t.Fatalf("UpdateScheduledDAGNextRun calls = %d, want 1", len(runStore.updateNextRunCalls))
	}
	got := runStore.updateNextRunCalls[0]
	if got.DagKey != "dag-1" || !got.DueAt.Equal(dueAt) || !got.NextRunAt.Equal(nextRunAt) {
		t.Fatalf("UpdateScheduledDAGNextRun call = %+v, want dag-1 due/next", got)
	}
	if got := runStore.callOrder; len(got) == 0 || got[len(got)-1] != "update_next_run:dag-1" {
		t.Fatalf("callOrder = %v, want schedule advance after run start work", got)
	}
}
