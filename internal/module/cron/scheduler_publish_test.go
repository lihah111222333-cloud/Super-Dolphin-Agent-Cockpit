package cron

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kelindar/event"

	crondto "github.com/anthropic-ai/super-agent-v3/internal/dto/cron"
	cronstore "github.com/anthropic-ai/super-agent-v3/internal/store/cron"
)

func collectRunStateEvents(t *testing.T, dispatcher *event.Dispatcher) (capture *[]crondto.JobRunStateChanged, cleanup func()) {
	t.Helper()
	out := []crondto.JobRunStateChanged{}
	cancel := event.Subscribe(dispatcher, func(ev crondto.JobRunStateChanged) {
		out = append(out, ev)
	})
	return &out, cancel
}

func TestSchedulerPublishesHappyPathTransitions(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	store := &recordingCronStore{}
	sub := &programmableSubmitter{}
	sub.startFn = func(_ context.Context, req StartTurnRequest) (StartTurnResult, error) {
		return StartTurnResult{TurnID: "turn-1", ThreadID: "thread-1", AgentID: "agent-1"}, nil
	}
	s := newTestScheduler(t, store, sub)
	s.WithDispatcher(dispatcher)

	now := time.Unix(1_700_000_000, 0).UTC()
	job := cronstore.Job{
		ID: "job-1", Name: "daily", Prompt: "x", ScheduleExpr: "0 9 * * *",
		Timezone: "UTC", Provider: "codex", CWD: "/repo", ClaimToken: "t", NextRunAt: now,
	}
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
	}

	out, cleanup := collectRunStateEvents(t, dispatcher)
	defer cleanup()

	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick = %v", err)
	}
	// allow async event delivery to flush
	time.Sleep(50 * time.Millisecond)

	wantStatuses := []string{"pending", "submitting", "submitted", "running"}
	if len(*out) != len(wantStatuses) {
		t.Fatalf("got %d events (statuses=%v); want %d", len(*out), statusesOf(*out), len(wantStatuses))
	}
	for i, want := range wantStatuses {
		if (*out)[i].Status != want {
			t.Fatalf("event[%d].Status = %q, want %q", i, (*out)[i].Status, want)
		}
	}
	if (*out)[2].TurnID != "turn-1" || (*out)[3].TurnID != "turn-1" {
		t.Fatalf("submitted/running events should carry turn_id; got %+v", *out)
	}
}

func TestSchedulerPublishesFailedOnStartTurnError(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	store := &recordingCronStore{}
	sub := &programmableSubmitter{
		startFn: func(context.Context, StartTurnRequest) (StartTurnResult, error) {
			return StartTurnResult{}, errors.New("provider down")
		},
	}
	s := newTestScheduler(t, store, sub)
	s.WithDispatcher(dispatcher)

	now := time.Unix(1_700_000_000, 0).UTC()
	job := cronstore.Job{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", Provider: "codex", CWD: "/r", ClaimToken: "t", NextRunAt: now}
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
	}

	out, cleanup := collectRunStateEvents(t, dispatcher)
	defer cleanup()

	_ = s.RunTick(context.Background())
	time.Sleep(50 * time.Millisecond)

	statuses := statusesOf(*out)
	// Expect: pending → submitting → failed
	if len(statuses) != 3 || statuses[2] != "failed" {
		t.Fatalf("statuses = %v; want pending/submitting/failed", statuses)
	}
	if (*out)[2].Error == "" {
		t.Fatalf("failed event should carry error message; got %+v", (*out)[2])
	}
}

func TestSchedulerNoPublishWhenDispatcherUnset(t *testing.T) {
	t.Parallel()

	store := &recordingCronStore{}
	sub := &programmableSubmitter{}
	s := newTestScheduler(t, store, sub)
	// no WithDispatcher

	now := time.Unix(1_700_000_000, 0).UTC()
	job := cronstore.Job{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", Provider: "codex", CWD: "/r", ClaimToken: "t", NextRunAt: now}
	store.claimFn = func(context.Context, cronstore.ClaimDueJobsParams) ([]cronstore.Job, error) {
		return []cronstore.Job{job}, nil
	}
	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick = %v", err)
	}
	// no panic + no observable side effect: success.
}

func statusesOf(events []crondto.JobRunStateChanged) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Status
	}
	return out
}
