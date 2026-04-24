package memory

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	"github.com/kelindar/event"
)

type recordingTeamSync struct {
	mu         sync.Mutex
	startCalls []teamSyncCall
	stopCalls  []string
}

type teamSyncCall struct {
	threadID string
	buildCtx contract.BuildCtx
}

func (r *recordingTeamSync) StartSession(_ context.Context, threadID string, buildCtx contract.BuildCtx) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startCalls = append(r.startCalls, teamSyncCall{threadID: threadID, buildCtx: buildCtx})
	return nil
}

func (r *recordingTeamSync) StopSession(_ context.Context, threadID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopCalls = append(r.stopCalls, threadID)
	return nil
}

func (r *recordingTeamSync) counts() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.startCalls), len(r.stopCalls)
}

func (r *recordingTeamSync) snapshot() ([]teamSyncCall, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	starts := append([]teamSyncCall(nil), r.startCalls...)
	stops := append([]string(nil), r.stopCalls...)
	return starts, stops
}

type stubThreadMetadataStore struct {
	meta *contract.ThreadMetadata
}

func (s *stubThreadMetadataStore) GetByThreadID(context.Context, string) (*contract.ThreadMetadata, error) {
	return s.meta, nil
}

func (s *stubThreadMetadataStore) ListAll(context.Context) ([]contract.ThreadMetadata, error) {
	if s.meta == nil {
		return nil, nil
	}
	return []contract.ThreadMetadata{*s.meta}, nil
}

func TestMemorySubscribersWireTeamSyncToThreadLifecycle(t *testing.T) {
	dispatcher := event.NewDispatcher()
	repoRoot := t.TempDir()
	store := &stubThreadMetadataStore{meta: &contract.ThreadMetadata{
		ThreadID:       "thread-1",
		Cwd:            repoRoot,
		ConfigOverride: []byte(`{"runtime":{"gitRoot":"` + repoRoot + `","sessionFlags":{"memory_kairos":true},"isWorktree":true}}`),
	}}
	syncer := &recordingTeamSync{}
	coordinator := newTeamSyncCoordinator(syncer, store, nil)
	coordinator.Start()
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := coordinator.Stop(stopCtx); err != nil {
			t.Fatalf("coordinator.Stop() error = %v", err)
		}
	}()
	spec := NewMemorySubscribers(nil, nil, coordinator, memorySubscriberParams{
		ThreadStore: store,
		TeamSync:    syncer,
	}).Spec
	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}
	defer cancel()

	event.Publish(dispatcher, threaddto.Started{ThreadID: "thread-1", CWD: repoRoot})
	event.Publish(dispatcher, threaddto.Stopped{ThreadID: "thread-1"})

	deadline := time.After(2 * time.Second)
	for {
		startCount, stopCount := syncer.counts()
		if startCount > 0 && stopCount > 0 {
			break
		}
		select {
		case <-deadline:
			startCalls, stopCalls := syncer.snapshot()
			t.Fatalf("team sync calls = starts=%#v stops=%#v", startCalls, stopCalls)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	startCalls, stopCalls := syncer.snapshot()

	if startCalls[0].threadID != "thread-1" {
		t.Fatalf("StartSession threadID = %q, want thread-1", startCalls[0].threadID)
	}
	if startCalls[0].buildCtx.CWD != repoRoot {
		t.Fatalf("StartSession buildCtx.CWD = %q, want %q", startCalls[0].buildCtx.CWD, repoRoot)
	}
	if startCalls[0].buildCtx.GitRoot != repoRoot {
		t.Fatalf("StartSession buildCtx.GitRoot = %q, want %q", startCalls[0].buildCtx.GitRoot, repoRoot)
	}
	if !startCalls[0].buildCtx.IsWorktree {
		t.Fatal("StartSession buildCtx.IsWorktree = false, want true")
	}
	if !startCalls[0].buildCtx.SessionFlags["memory_kairos"] {
		t.Fatalf("StartSession SessionFlags = %#v, want memory_kairos=true", startCalls[0].buildCtx.SessionFlags)
	}
	if stopCalls[0] != "thread-1" {
		t.Fatalf("StopSession threadID = %q, want thread-1", stopCalls[0])
	}
}
