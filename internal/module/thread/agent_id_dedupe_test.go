package thread

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func TestPrepareStartRequestReassignsExistingExplicitAgentID(t *testing.T) {
	cwd := wantStartCWD(t)
	svc := &service{
		threadStore: &stubThreadStore{thread: &threadstore.Thread{ThreadID: "agent-dup"}},
	}
	req, agentID, release, err := svc.prepareStartRequest(context.Background(), StartRequest{
		AgentID:  "agent-dup",
		Name:     "worker",
		Provider: "codex",
		CWD:      cwd,
	})
	if err != nil {
		t.Fatalf("prepareStartRequest() error = %v", err)
	}
	defer release()
	if agentID == "agent-dup" || req.AgentID == "agent-dup" {
		t.Fatalf("agent_id was not reassigned on collision: req=%q agentID=%q", req.AgentID, agentID)
	}
	if !strings.HasPrefix(agentID, "agent_") {
		t.Fatalf("reassigned agent_id = %q, want generated agent_ id", agentID)
	}
}

func TestPrepareStartRequestPreservesAvailableExplicitAgentID(t *testing.T) {
	cwd := wantStartCWD(t)
	svc := &service{threadStore: &stubThreadStore{}}
	req, agentID, release, err := svc.prepareStartRequest(context.Background(), StartRequest{
		AgentID:  "agent-keep",
		Name:     "worker",
		Provider: "codex",
		CWD:      cwd,
	})
	if err != nil {
		t.Fatalf("prepareStartRequest() error = %v", err)
	}
	defer release()
	if agentID != "agent-keep" || req.AgentID != "agent-keep" {
		t.Fatalf("agent_id = req %q / result %q, want agent-keep", req.AgentID, agentID)
	}
}

func TestPrepareStartRequestRejectsAgentIDWhenCollisionCheckFails(t *testing.T) {
	cwd := wantStartCWD(t)
	svc := &service{threadStore: &stubThreadStore{existsErr: errors.New("db unavailable")}}
	_, _, release, err := svc.prepareStartRequest(context.Background(), StartRequest{
		AgentID:  "agent-db-error",
		Name:     "worker",
		Provider: "codex",
		CWD:      cwd,
	})
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("prepareStartRequest() error = nil, want collision check failure")
	}
	if !strings.Contains(err.Error(), "check agent_id") {
		t.Fatalf("prepareStartRequest() error = %v, want check agent_id context", err)
	}
}

func TestPrepareStartRequestConcurrentChildReservationsAreUnique(t *testing.T) {
	cwd := wantStartCWD(t)
	svc := &service{threadStore: &stubThreadStore{}}
	const n = 2
	start := make(chan struct{})
	ids := make(chan string, n)
	releases := make(chan func(), n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	workersDone := make(chan struct{})
	registerThreadGoroutineCleanup(t, workersDone, "agent id reservation")
	for range n {
		go func() {
			defer wg.Done()
			<-start
			_, agentID, release, err := svc.prepareStartRequest(context.Background(), StartRequest{
				ParentAgentID: "agent-parent",
				Name:          "worker",
				Provider:      "codex",
				CWD:           cwd,
			})
			if err != nil {
				errs <- err
				return
			}
			ids <- agentID
			releases <- release
		}()
	}
	close(start)
	wg.Wait()
	close(workersDone)
	close(ids)
	close(releases)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("prepareStartRequest() error = %v", err)
		}
	}
	for release := range releases {
		release()
	}
	seen := make(map[string]struct{}, n)
	for id := range ids {
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate child agent_id reservation: %q", id)
		}
		seen[id] = struct{}{}
	}
	if _, ok := seen["agent-parent-1"]; !ok {
		t.Fatalf("reserved ids = %#v, want agent-parent-1", seen)
	}
	if _, ok := seen["agent-parent-2"]; !ok {
		t.Fatalf("reserved ids = %#v, want agent-parent-2", seen)
	}
}

func TestPrepareStartRequestFailsFastOnInvalidRuntimeMode(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "mystery")
	svc := &service{threadStore: &stubThreadStore{}}

	_, _, release, err := svc.prepareStartRequest(context.Background(), StartRequest{
		AgentID:  "agent-invalid-runtime",
		Provider: "codex",
		CWD:      wantStartCWD(t),
	})
	if release != nil {
		release()
	}
	if err == nil || !strings.Contains(err.Error(), "invalid SUPER_DOLPHIN_RUNTIME_MODE") {
		t.Fatalf("prepareStartRequest() error = %v, want invalid runtime mode fail-fast", err)
	}
}
