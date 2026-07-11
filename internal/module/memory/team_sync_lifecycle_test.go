package memory

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
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
	configOverride, err := json.Marshal(map[string]any{
		"runtime": map[string]any{
			"gitRoot": repoRoot,
			"sessionFlags": map[string]bool{
				"memory_kairos": true,
			},
			"isWorktree": true,
		},
	})
	if err != nil {
		t.Fatalf("marshal config override: %v", err)
	}
	store := &stubThreadMetadataStore{meta: &contract.ThreadMetadata{
		ThreadID:       "thread-1",
		Cwd:            repoRoot,
		ConfigOverride: configOverride,
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

	waitForTeamSyncLifecycle(t, syncer)
	startCalls, stopCalls := syncer.snapshot()
	assertTeamSyncStart(t, startCalls[0], repoRoot)
	assertTeamSyncStop(t, stopCalls[0])
}

func waitForTeamSyncLifecycle(t *testing.T, syncer *recordingTeamSync) {
	t.Helper()
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
}

func assertTeamSyncStart(t *testing.T, call teamSyncCall, repoRoot string) {
	t.Helper()
	if call.threadID != "thread-1" {
		t.Fatalf("StartSession threadID = %q, want thread-1", call.threadID)
	}
	if call.buildCtx.CWD != repoRoot {
		t.Fatalf("StartSession buildCtx.CWD = %q, want %q", call.buildCtx.CWD, repoRoot)
	}
	if call.buildCtx.GitRoot != repoRoot {
		t.Fatalf("StartSession buildCtx.GitRoot = %q, want %q", call.buildCtx.GitRoot, repoRoot)
	}
	if !call.buildCtx.IsWorktree {
		t.Fatal("StartSession buildCtx.IsWorktree = false, want true")
	}
	if !call.buildCtx.SessionFlags["memory_kairos"] {
		t.Fatalf("StartSession SessionFlags = %#v, want memory_kairos=true", call.buildCtx.SessionFlags)
	}
}

func assertTeamSyncStop(t *testing.T, threadID string) {
	t.Helper()
	if threadID != "thread-1" {
		t.Fatalf("StopSession threadID = %q, want thread-1", threadID)
	}
}
