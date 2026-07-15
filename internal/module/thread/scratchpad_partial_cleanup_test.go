package thread

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
)

func TestLifecycleReturnsPrivatePartialScratchpadCleanupErrorAfterFinalization(t *testing.T) {
	cleanupErr := errors.New("remove /private/secret/scratchpad: permission denied")
	for _, tc := range []struct {
		name       string
		operation  func(*service) error
		wantStatus string
	}{
		{name: "stop", operation: func(s *service) error { return s.Stop(context.Background(), "thread-1") }, wantStatus: statusStopped},
		{name: "archive", operation: func(s *service) error { return s.Archive(context.Background(), "thread-1") }, wantStatus: statusArchived},
		{name: "delete", operation: func(s *service) error { return s.Delete(context.Background(), "thread-1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newScratchpadCleanupService(t)
			svc.scratchpadCleanup = func(string) error { return cleanupErr }
			events := 0
			svc.emitStopped = func(threaddto.Stopped) { events++ }

			err := tc.operation(svc)
			var partial *lifecyclePartialCleanupError
			if !errors.As(err, &partial) || !errors.Is(err, cleanupErr) {
				t.Fatalf("operation error = %v, want typed partial cleanup error", err)
			}
			if strings.Contains(err.Error(), "/private/secret/scratchpad") {
				t.Fatalf("operation error exposed scratchpad path: %v", err)
			}
			if events != 1 {
				t.Fatalf("stopped events = %d, want finalization event", events)
			}
			if tc.wantStatus != "" {
				store := svc.threadStore.(*stubThreadStore)
				if store.status.Status != tc.wantStatus {
					t.Fatalf("durable status = %q, want %q", store.status.Status, tc.wantStatus)
				}
			}
		})
	}
}

func TestLifecycleAggregatesTurnCleanupErrorsAfterFinalization(t *testing.T) {
	firstErr := errors.New("provider turn cleanup failed")
	secondErr := errors.New("agent turn cleanup failed")
	for _, tc := range []struct {
		name       string
		operation  func(*service) error
		wantStatus string
		wantReason string
	}{
		{name: "stop", operation: func(s *service) error { return s.Stop(context.Background(), "thread-1") }, wantStatus: statusStopped, wantReason: "thread_stopped"},
		{name: "archive", operation: func(s *service) error { return s.Archive(context.Background(), "thread-1") }, wantStatus: statusArchived, wantReason: "thread_archived"},
		{name: "delete", operation: func(s *service) error { return s.Delete(context.Background(), "thread-1") }, wantReason: "thread_deleted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newScratchpadCleanupService(t)
			turns := &stubTurnService{cleanupErrors: map[string]error{
				"provider-thread-1": firstErr,
				"agent-1":           secondErr,
			}}
			svc.turns = turns
			events := 0
			svc.emitStopped = func(threaddto.Stopped) { events++ }

			err := tc.operation(svc)
			var partial *lifecyclePartialCleanupError
			if !errors.As(err, &partial) || !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
				t.Fatalf("operation error = %v, want typed joined turn cleanup errors", err)
			}
			assertCleanupCalls(t, turns.cleanupCalls, map[string]struct{}{
				"thread-1:" + tc.wantReason:          {},
				"provider-thread-1:" + tc.wantReason: {},
				"agent-1:" + tc.wantReason:           {},
			})
			if events != 1 {
				t.Fatalf("stopped events = %d, want 1 after durable finalization", events)
			}
			if tc.wantStatus != "" {
				store := svc.threadStore.(*stubThreadStore)
				if store.status.Status != tc.wantStatus {
					t.Fatalf("durable status = %q, want %q", store.status.Status, tc.wantStatus)
				}
			}
		})
	}
}

func TestCleanupThreadScratchpadPropagatesPathResolutionError(t *testing.T) {
	svc := &service{}
	if err := svc.cleanupThreadScratchpadRecord(context.Background(), "thread-1", nil); err == nil {
		t.Fatal("cleanupThreadScratchpadRecord() error = nil, want offline config resolution failure")
	}
}

func TestStartScratchpadCleanupPropagatesFailure(t *testing.T) {
	cleanupErr := errors.New("scratchpad cleanup failed")
	svc := &service{scratchpadCleanup: func(string) error { return cleanupErr }}
	_, cleanup, err := svc.prepareScratchpadBuildCtx(
		StartRequest{CWD: t.TempDir(), Config: map[string]any{"scratchpadEnabled": true}},
		"thread-1",
		contract.BuildCtx{},
	)
	if err != nil {
		t.Fatalf("prepareScratchpadBuildCtx() error = %v", err)
	}
	if err := cleanup(); !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanup() error = %v, want %v", err, cleanupErr)
	}
}

func TestLifecycleScratchpadCleanupPropagatesFailure(t *testing.T) {
	cleanupErr := errors.New("scratchpad cleanup failed")
	active := true
	if err := runScratchpadCleanup(&active, func() error { return cleanupErr }); !errors.Is(err, cleanupErr) {
		t.Fatalf("runScratchpadCleanup() error = %v, want %v", err, cleanupErr)
	}
}

func TestPromptSnapshotScratchpadCleanupJoinsMainResult(t *testing.T) {
	mainErr := errors.New("prompt rebuild failed")
	cleanupErr := errors.New("scratchpad cleanup failed")
	err := mainErr
	joinScratchpadCleanup(&err, nil, func() error { return cleanupErr })
	if !errors.Is(err, mainErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("joined error = %v, want prompt and cleanup failures", err)
	}
}

func TestScratchpadCleanupPreservesMainErrorWhenCleanupSucceeds(t *testing.T) {
	mainErr := errors.New("main failure")
	err := mainErr
	joinScratchpadCleanup(&err, nil, func() error { return nil })
	if err != mainErr {
		t.Fatalf("joined error = %v, want original error identity", err)
	}
}

func TestScratchpadPartialCleanupJoinPreservesMainErrorWithoutCleanupFailure(t *testing.T) {
	mainErr := errors.New("store failure")
	if err := joinScratchpadPartialCleanupError("delete", mainErr, nil); err != mainErr {
		t.Fatalf("joined error = %v, want original store error identity", err)
	}
}

func TestSpawnScratchpadCleanupPropagatesFailure(t *testing.T) {
	cleanupErr := errors.New("scratchpad cleanup failed")
	active := true
	err := cleanupPendingSpawn(
		context.Background(),
		&service{},
		&active,
		func() error { return cleanupErr },
		nil,
		"agent-1",
	)
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("cleanupPendingSpawn() error = %v, want %v", err, cleanupErr)
	}
}
