package cron

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewCronProgressSubscribersSpec(t *testing.T) {
	t.Parallel()

	scheduler := NewScheduler(slog.Default(), &recordingCronStore{}, &programmableSubmitter{}, SchedulerConfig{}, newTestCronMetrics())
	spec := NewCronProgressSubscribers(scheduler, nil).Spec

	if spec.EventType != "cron.progress" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "cron.subscribeCronProgress" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "cron" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "cron-progress-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestCronProgressSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	var mu sync.Mutex
	var listCalls int
	store := &recordingCronStore{
		listJobsFn: func(context.Context) ([]JobRecord, error) {
			mu.Lock()
			defer mu.Unlock()
			listCalls++
			return nil, nil
		},
	}
	scheduler := NewScheduler(slog.Default(), store, &programmableSubmitter{}, SchedulerConfig{ClaimedBy: "test"}, newTestCronMetrics())
	spec := NewCronProgressSubscribers(scheduler, nil).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, turndto.ItemCompleted{TurnHeader: cronProgressTurnHeader("thread-1", "turn-1", "agent-1")})
	waitForCronProgressListCalls(t, &mu, &listCalls, 1)

	cancel()
	cancel()

	event.Publish(dispatcher, turndto.ItemCompleted{TurnHeader: cronProgressTurnHeader("thread-1", "turn-1", "agent-1")})
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	got := listCalls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("list calls after cancel = %d, want 1", got)
	}
}

func TestCronProgressWorkerWarnsAndCountsStaleTerminal(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store := &recordingCronStore{
		listUnresolvedFn: func(context.Context) ([]RunRecord, error) {
			return nil, nil
		},
	}
	scheduler := NewScheduler(slog.Default(), store, &programmableSubmitter{}, SchedulerConfig{ClaimedBy: "test"}, newTestCronMetrics())
	worker := newCronProgressWorker(scheduler, logger)

	worker.dispatch(context.Background(), cronProgressRequest{kind: cronCompleteTurn, turnID: "stale-turn", success: true})

	if got := worker.staleTotal.Load(); got != 1 {
		t.Fatalf("staleTotal = %d, want 1", got)
	}
	text := logs.String()
	if !strings.Contains(text, "level=WARN") || !strings.Contains(text, "stale_total=1") {
		t.Fatalf("logs = %q, want warn with stale_total", text)
	}
}

func cronProgressTurnHeader(threadID, turnID, agentID string) shareddto.TurnHeader {
	return shareddto.TurnHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID},
			AgentID:      agentID,
		},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
	}
}

func waitForCronProgressListCalls(t *testing.T, mu *sync.Mutex, calls *int, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := *calls
		mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	mu.Lock()
	got := *calls
	mu.Unlock()
	t.Fatalf("list calls = %d, want %d", got, want)
}
