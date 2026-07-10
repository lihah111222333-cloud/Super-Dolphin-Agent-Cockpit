package notify

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	taskdto "github.com/anthropic-ai/super-agent-v3/internal/dto/task"
)

// recordingMessageNotifier implements contract.MessageNotifier so the
// subscriber can be exercised without spinning up the real webhook
// flusher.
type recordingMessageNotifier struct {
	mu   sync.Mutex
	reqs []contract.NotifyRequest
	fail error
}

func (r *recordingMessageNotifier) TryEnqueue(_ context.Context, req contract.NotifyRequest) error {
	if r.fail != nil {
		return r.fail
	}
	r.mu.Lock()
	r.reqs = append(r.reqs, req)
	r.mu.Unlock()
	return nil
}

func (r *recordingMessageNotifier) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reqs)
}

// fakeStore implements taskdag.DAGDetailStore for the DAG subscriber.
type fakeStore struct {
	listNodesFn func(ctx context.Context, dagKey string) ([]taskdag.Node, error)
	getDAGFn    func(ctx context.Context, dagKey string) (*taskdag.DAG, error)
	listCalls   atomic.Int64
	getDAGCalls atomic.Int64
}

var _ taskdag.DAGDetailStore = (*fakeStore)(nil)

func (f *fakeStore) ListNodes(ctx context.Context, dagKey string) ([]taskdag.Node, error) {
	f.listCalls.Add(1)
	if f.listNodesFn != nil {
		return f.listNodesFn(ctx, dagKey)
	}
	return nil, nil
}
func (f *fakeStore) GetDAG(ctx context.Context, dagKey string) (*taskdag.DAG, error) {
	f.getDAGCalls.Add(1)
	if f.getDAGFn != nil {
		return f.getDAGFn(ctx, dagKey)
	}
	return nil, nil
}

// startAndStopDAGNotifier is a test helper that starts the worker,
// fires the event via fn, then stops the worker so all enqueued events
// are drained before assertions.
func startAndStopDAGNotifier(t *testing.T, n *DAGNotifier, fn func()) {
	t.Helper()
	n.Start()
	fn()
	if err := n.Stop(context.Background()); err != nil {
		t.Fatalf("DAGNotifier.Stop: %v", err)
	}
}

func TestOnNodeStatusChangedIgnoresNonTerminal(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			t.Fatal("non-terminal status must not touch the store")
			return nil, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store)
	startAndStopDAGNotifier(t, n, func() {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "running",
		})
	})
	if rec.len() != 0 {
		t.Fatalf("non-terminal enqueue leaked; got %d", rec.len())
	}
}

func TestOnNodeStatusChangedDropsWithoutAlias(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			return []taskdag.Node{{NodeKey: "n"}}, nil
		},
		getDAGFn: func(context.Context, string) (*taskdag.DAG, error) {
			return &taskdag.DAG{}, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store)
	startAndStopDAGNotifier(t, n, func() {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "failed",
		})
	})
	if rec.len() != 0 {
		t.Fatalf("no alias must drop; got %d enqueues", rec.len())
	}
	if n.Metrics().Skipped != 1 {
		t.Fatalf("Skipped metric = %d, want 1", n.Metrics().Skipped)
	}
}

func TestOnNodeStatusChangedEnqueuesWithNodeAlias(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	nodeCfg := jsonB(t, map[string]any{"notify_channel": "slack.ops"})
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			return []taskdag.Node{{NodeKey: "n", Title: "Hello", Config: nodeCfg}}, nil
		},
		getDAGFn: func(context.Context, string) (*taskdag.DAG, error) {
			return &taskdag.DAG{Title: "Big DAG"}, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store)
	startAndStopDAGNotifier(t, n, func() {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			OldStatus: "running",
			NewStatus: "done",
		})
	})
	if rec.len() != 1 {
		t.Fatalf("want 1 enqueue, got %d", rec.len())
	}
	got := rec.reqs[0]
	if got.ChannelAlias != "slack.ops" {
		t.Fatalf("alias = %q, want slack.ops", got.ChannelAlias)
	}
	if !strings.Contains(got.Message.Body, "Big DAG") {
		t.Fatalf("body missing dag title: %q", got.Message.Body)
	}
	if !strings.Contains(got.Message.Body, "Hello") {
		t.Fatalf("body missing node title: %q", got.Message.Body)
	}
	if got.Message.Level != contract.NotifyLevelInfo {
		t.Fatalf("level = %q, want info", got.Message.Level)
	}
}

func TestOnNodeStatusChangedFallsBackToDAGAlias(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			return []taskdag.Node{{NodeKey: "n"}}, nil // node has no config.notify_channel
		},
		getDAGFn: func(context.Context, string) (*taskdag.DAG, error) {
			return &taskdag.DAG{Metadata: jsonB(t, map[string]any{"notify_channel": "slack.dag"})}, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store)
	startAndStopDAGNotifier(t, n, func() {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "failed",
		})
	})
	if rec.len() != 1 {
		t.Fatalf("want 1 enqueue, got %d", rec.len())
	}
	if rec.reqs[0].ChannelAlias != "slack.dag" {
		t.Fatalf("dag fallback alias wrong: %q", rec.reqs[0].ChannelAlias)
	}
	if rec.reqs[0].Message.Level != contract.NotifyLevelError {
		t.Fatalf("failed status must map to error level, got %q", rec.reqs[0].Message.Level)
	}
}

func TestOnNodeStatusChangedCountsEnqueueErrors(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{fail: errors.New("queue full")}
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			return []taskdag.Node{{NodeKey: "n", Config: jsonB(t, map[string]any{"notify_channel": "slack.ops"})}}, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store)
	startAndStopDAGNotifier(t, n, func() {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "done",
		})
	})
	m := n.Metrics()
	if m.EnqueueErrors != 1 || m.Enqueued != 0 {
		t.Fatalf("metrics = %+v, want EnqueueErrors=1 Enqueued=0", m)
	}
}

func TestOnNodeStatusChangedSurvivesNilStore(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	n := NewDAGNotifier(slog.Default(), rec, nil)
	startAndStopDAGNotifier(t, n, func() {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "done",
		})
	})
	// No store -> no node / dag -> empty alias -> dropped.
	if rec.len() != 0 || n.Metrics().Skipped != 1 {
		t.Fatalf("nil store should skip enqueue; got %+v", n.Metrics())
	}
}

func TestLevelForNodeStatusMapping(t *testing.T) {
	t.Parallel()
	cases := map[string]contract.NotifyLevel{
		"failed":    contract.NotifyLevelError,
		"error":     contract.NotifyLevelError,
		"cancelled": contract.NotifyLevelWarn,
		"canceled":  contract.NotifyLevelWarn,
		"done":      contract.NotifyLevelInfo,
		"succeeded": contract.NotifyLevelInfo,
		"skipped":   contract.NotifyLevelInfo,
		"":          contract.NotifyLevelInfo,
	}
	for in, want := range cases {
		if got := levelForNodeStatus(in); got != want {
			t.Errorf("levelForNodeStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDAGNotifierStopDrainsBeforeReturn verifies the bounded drain
// contract: Stop returns only after all enqueued events are processed.
func TestDAGNotifierStopDrainsBeforeReturn(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			time.Sleep(10 * time.Millisecond) // simulate slow DB
			return []taskdag.Node{{NodeKey: "n", Config: jsonB(t, map[string]any{"notify_channel": "ch"})}}, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store)
	n.Start()
	for range 5 {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "done",
		})
	}
	if err := n.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if rec.len() != 5 {
		t.Fatalf("expected 5 enqueues after drain, got %d", rec.len())
	}
}

func TestDAGNotifierRunDrainsWithCanceledRunnerContext(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	storeStarted := make(chan struct{})
	releaseStore := make(chan struct{})
	var startedOnce sync.Once
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() { close(releaseStore) })
	}
	defer release()
	nodeCfg := jsonB(t, map[string]any{"notify_channel": "ch"})
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			startedOnce.Do(func() { close(storeStarted) })
			<-releaseStore
			return []taskdag.Node{{NodeKey: "n", Config: nodeCfg}}, nil
		},
	}
	n := NewDAGNotifier(slog.Default(), rec, store)
	n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
		TaskNodeHeader: shareddto.TaskNodeHeader{
			TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
			NodeKey:       "n",
		},
		NewStatus: "done",
	})

	runCtx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		runErr <- n.Run(runCtx)
	})
	select {
	case <-storeStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not start draining queued event")
	}

	cancel()
	select {
	case err := <-runErr:
		t.Fatalf("Run returned before drain completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	release()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after drain completed")
	}
	if rec.len() != 1 {
		t.Fatalf("expected queued event to drain before Run returned, got %d enqueues", rec.len())
	}
}

func TestDAGNotifierDropsWhenQueueFull(t *testing.T) {
	t.Parallel()
	rec := &recordingMessageNotifier{}
	storeStarted := make(chan struct{})
	releaseStore := make(chan struct{})
	var startedOnce sync.Once
	store := &fakeStore{
		listNodesFn: func(context.Context, string) ([]taskdag.Node, error) {
			startedOnce.Do(func() { close(storeStarted) })
			<-releaseStore
			return []taskdag.Node{{NodeKey: "n", Config: jsonB(t, map[string]any{"notify_channel": "ch"})}}, nil
		},
	}
	capOpt, err := WithDAGNotifyQueueCapacity(1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n := NewDAGNotifier(slog.Default(), rec, store, capOpt)
	n.Start()

	n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
		TaskNodeHeader: shareddto.TaskNodeHeader{
			TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
			NodeKey:       "n",
		},
		NewStatus: "done",
	})
	select {
	case <-storeStarted:
	case <-time.After(time.Second):
		t.Fatal("worker did not start processing first event")
	}

	for range 100 {
		n.onNodeStatusChanged(taskdto.TaskNodeStatusChanged{
			TaskNodeHeader: shareddto.TaskNodeHeader{
				TaskDAGHeader: shareddto.TaskDAGHeader{DAGHeader: shareddto.DAGHeader{DagKey: "d"}},
				NodeKey:       "n",
			},
			NewStatus: "done",
		})
	}

	if n.Metrics().Dropped == 0 {
		t.Fatalf("Dropped = 0, want queue overflow drops")
	}
	close(releaseStore)
	if err := n.Stop(context.Background()); err != nil {
		t.Fatalf("DAGNotifier.Stop: %v", err)
	}
}
