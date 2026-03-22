package dashboard

import (
	"context"
	"testing"
	"time"

	tasktracestore "github.com/anthropic-ai/super-agent-v3/internal/store/tasktrace"
)

func TestGetDashboardPageLoadsTaskTraces(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	traceStarted := make(chan struct{}, 1)
	svc := &service{
		taskTraces: &blockingTaskTraceStore{
			started: traceStarted,
			release: release,
			result:  []tasktracestore.TaskTrace{{TraceID: "trace-1"}},
		},
	}

	type result struct {
		page *DashboardPage
		err  error
	}
	done := make(chan result, 1)
	go func() {
		page, err := svc.GetDashboardPage(context.Background(), "tasks")
		done <- result{page: page, err: err}
	}()

	waitForSignal(t, traceStarted, "task trace list")
	close(release)

	got := <-done
	if got.err != nil {
		t.Fatalf("GetDashboardPage(tasks) error = %v", got.err)
	}
	if got.page == nil || len(got.page.TaskTraces) != 1 {
		t.Fatalf("GetDashboardPage(tasks) = %#v", got.page)
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("%s did not start", label)
	}
}

type blockingTaskTraceStore struct {
	started chan<- struct{}
	release <-chan struct{}
	result  []tasktracestore.TaskTrace
}

func (s *blockingTaskTraceStore) Insert(context.Context, tasktracestore.TaskTrace) (*tasktracestore.TaskTrace, error) {
	return nil, nil
}

func (s *blockingTaskTraceStore) List(context.Context, tasktracestore.ListFilter) ([]tasktracestore.TaskTrace, error) {
	notifySignal(s.started)
	<-s.release
	return s.result, nil
}

func notifySignal(ch chan<- struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
