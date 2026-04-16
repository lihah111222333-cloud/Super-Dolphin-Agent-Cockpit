package memory

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

type recordingTeamSync struct {
	startCalls []teamSyncCall
	stopCalls  []string
}

type teamSyncCall struct {
	threadID string
	buildCtx contract.BuildCtx
}

func (r *recordingTeamSync) StartSession(_ context.Context, threadID string, buildCtx contract.BuildCtx) error {
	r.startCalls = append(r.startCalls, teamSyncCall{threadID: threadID, buildCtx: buildCtx})
	return nil
}

func (r *recordingTeamSync) StopSession(_ context.Context, threadID string) error {
	r.stopCalls = append(r.stopCalls, threadID)
	return nil
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

func TestRegisterMemoryHooksWiresTeamSyncToThreadLifecycle(t *testing.T) {
	dispatcher := event.NewDispatcher()
	repoRoot := t.TempDir()
	store := &stubThreadMetadataStore{meta: &contract.ThreadMetadata{
		ThreadID:       "thread-1",
		Cwd:            repoRoot,
		ConfigOverride: []byte(`{"runtime":{"gitRoot":"` + repoRoot + `","sessionFlags":{"memory_kairos":true},"isWorktree":true}}`),
	}}
	syncer := &recordingTeamSync{}
	app := fx.New(
		fx.NopLogger,
		fx.Supply(dispatcher),
		fx.Provide(
			func() threadMetadataStore { return store },
			func() teampkg.Lifecycle { return syncer },
		),
		fx.Invoke(registerMemoryHooks),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := app.Stop(stopCtx); err != nil {
			t.Fatalf("app.Stop() error = %v", err)
		}
	}()

	event.Publish(dispatcher, threaddto.Started{ThreadID: "thread-1", CWD: repoRoot})
	event.Publish(dispatcher, threaddto.Stopped{ThreadID: "thread-1"})

	deadline := time.After(2 * time.Second)
	for len(syncer.startCalls) == 0 || len(syncer.stopCalls) == 0 {
		select {
		case <-deadline:
			t.Fatalf("team sync calls = starts=%#v stops=%#v", syncer.startCalls, syncer.stopCalls)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if syncer.startCalls[0].threadID != "thread-1" {
		t.Fatalf("StartSession threadID = %q, want thread-1", syncer.startCalls[0].threadID)
	}
	if syncer.startCalls[0].buildCtx.CWD != repoRoot {
		t.Fatalf("StartSession buildCtx.CWD = %q, want %q", syncer.startCalls[0].buildCtx.CWD, repoRoot)
	}
	if syncer.startCalls[0].buildCtx.GitRoot != repoRoot {
		t.Fatalf("StartSession buildCtx.GitRoot = %q, want %q", syncer.startCalls[0].buildCtx.GitRoot, repoRoot)
	}
	if !syncer.startCalls[0].buildCtx.IsWorktree {
		t.Fatal("StartSession buildCtx.IsWorktree = false, want true")
	}
	if !syncer.startCalls[0].buildCtx.SessionFlags["memory_kairos"] {
		t.Fatalf("StartSession SessionFlags = %#v, want memory_kairos=true", syncer.startCalls[0].buildCtx.SessionFlags)
	}
	if syncer.stopCalls[0] != "thread-1" {
		t.Fatalf("StopSession threadID = %q, want thread-1", syncer.stopCalls[0])
	}
}
