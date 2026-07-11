package cron

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"

	crondto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/cron"
)

type eventCapture struct {
	mu     sync.Mutex
	events []crondto.JobRunStateChanged
}

func (c *eventCapture) add(ev crondto.JobRunStateChanged) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *eventCapture) get() []crondto.JobRunStateChanged {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]crondto.JobRunStateChanged, len(c.events))
	copy(out, c.events)
	return out
}

func collectRunStateEvents(t *testing.T, dispatcher *event.Dispatcher) (capture *eventCapture, cleanup func()) {
	t.Helper()
	cap := &eventCapture{}
	cancel := event.Subscribe(dispatcher, func(ev crondto.JobRunStateChanged) {
		cap.add(ev)
	})
	return cap, cancel
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
	job := JobRecord{
		ID: "job-1", Name: "daily", Prompt: "x", ScheduleExpr: "0 9 * * *",
		Timezone: "UTC", Provider: "codex", CWD: "/repo", ClaimToken: "t", NextRunAt: now,
	}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}

	out, cleanup := collectRunStateEvents(t, dispatcher)
	defer cleanup()

	if err := s.RunTick(context.Background()); err != nil {
		t.Fatalf("RunTick = %v", err)
	}
	// allow async event delivery to flush
	time.Sleep(50 * time.Millisecond)

	events := out.get()
	wantStatuses := []string{"pending", "submitting", "submitted", "running"}
	if len(events) != len(wantStatuses) {
		t.Fatalf("got %d events (statuses=%v); want %d", len(events), statusesOf(events), len(wantStatuses))
	}
	for i, want := range wantStatuses {
		if events[i].Status != want {
			t.Fatalf("event[%d].Status = %q, want %q", i, events[i].Status, want)
		}
	}
	if events[2].TurnID != "turn-1" || events[3].TurnID != "turn-1" {
		t.Fatalf("submitted/running events should carry turn_id; got %+v", events)
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
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", Provider: "codex", CWD: "/r", ClaimToken: "t", NextRunAt: now}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
	}

	out, cleanup := collectRunStateEvents(t, dispatcher)
	defer cleanup()

	_ = s.RunTick(context.Background())
	time.Sleep(50 * time.Millisecond)

	events := out.get()
	statuses := statusesOf(events)
	// Expect: pending → submitting → failed
	if len(statuses) != 3 || statuses[2] != "failed" {
		t.Fatalf("statuses = %v; want pending/submitting/failed", statuses)
	}
	if events[2].Error == "" {
		t.Fatalf("failed event should carry error message; got %+v", events[2])
	}
}

func TestSchedulerNoPublishWhenDispatcherUnset(t *testing.T) {
	t.Parallel()

	store := &recordingCronStore{}
	sub := &programmableSubmitter{}
	s := newTestScheduler(t, store, sub)
	// no WithDispatcher

	now := time.Unix(1_700_000_000, 0).UTC()
	job := JobRecord{ID: "job-1", ScheduleExpr: "0 9 * * *", Timezone: "UTC", Provider: "codex", CWD: "/r", ClaimToken: "t", NextRunAt: now}
	store.claimFn = func(context.Context, ClaimDueJobsForUpdateParams) ([]JobRecord, error) {
		return []JobRecord{job}, nil
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
