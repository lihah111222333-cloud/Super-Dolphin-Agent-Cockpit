package thread

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/idempotency"
)

func TestStartLaunchIntentRetainedPendingTerminalKeepsKey(t *testing.T) {
	for name, terminate := range map[string]func(context.Context, *service, string) error{
		"stop":    func(ctx context.Context, svc *service, threadID string) error { return svc.Stop(ctx, threadID) },
		"archive": func(ctx context.Context, svc *service, threadID string) error { return svc.Archive(ctx, threadID) },
		"delete":  func(ctx context.Context, svc *service, threadID string) error { return svc.Delete(ctx, threadID) },
	} {
		t.Run(name, func(t *testing.T) {
			statusErr := errors.New("status update failed")
			threads := &cleanupCountingThreadStore{statusErr: statusErr}
			svc := &service{threadStore: threads}
			req := StartRequest{LaunchIntentID: "launch_018f00e0-39fc-72ac-a47a-2a858c75d111", Provider: "codex", CWD: wantStartCWD(t), DeferSpawn: true}
			first, err := svc.Start(context.Background(), req)
			if err != nil {
				t.Fatalf("first Start() error = %v", err)
			}
			threads.thread.PendingLaunch = true
			cause := errors.New("post-launch cleanup uncertain")
			if err := svc.cleanupFailedPendingLaunch(context.Background(), first.ThreadID, first.AgentID, idempotency.Retain(cause)); !errors.Is(err, statusErr) {
				t.Fatalf("cleanupFailedPendingLaunch() error = %v, want status failure", err)
			}
			threads.statusErr = nil
			upsertsBefore := threads.upsertCount
			if err := terminate(context.Background(), svc, first.ThreadID); err != nil {
				t.Fatalf("terminal pending launch error = %v", err)
			}
			if _, err := svc.Start(context.Background(), req); !errors.Is(err, cause) {
				t.Fatalf("second Start() error = %v, want retained cause", err)
			}
			if threads.upsertCount != upsertsBefore {
				t.Fatalf("thread upserts = %d, want %d after retained terminal path", threads.upsertCount, upsertsBefore)
			}
		})
	}
}
