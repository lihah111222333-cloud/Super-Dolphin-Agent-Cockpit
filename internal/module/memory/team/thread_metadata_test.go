package team

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
)

type threadMetadataStoreStub struct {
	meta *contract.ThreadMetadata
	err  error
}

func (s threadMetadataStoreStub) GetByThreadID(context.Context, string) (*contract.ThreadMetadata, error) {
	return s.meta, s.err
}

func (s threadMetadataStoreStub) ListAll(context.Context) ([]contract.ThreadMetadata, error) {
	if s.meta == nil {
		return nil, s.err
	}
	return []contract.ThreadMetadata{*s.meta}, s.err
}

type recordingLifecycle struct {
	startCalls int
	buildCtx   contract.BuildCtx
}

func (r *recordingLifecycle) StartSession(_ context.Context, _ string, buildCtx contract.BuildCtx) error {
	r.startCalls++
	r.buildCtx = buildCtx
	return nil
}

func (*recordingLifecycle) StopSession(context.Context, string) error {
	return nil
}

func TestStartSessionFromThreadEventReturnsMetadataParseError(t *testing.T) {
	svc := &recordingLifecycle{}
	err := StartSessionFromThreadEvent(svc, threadMetadataStoreStub{meta: &contract.ThreadMetadata{
		ThreadID:         "thread-1",
		Cwd:              "/repo",
		ConfigOverride:   []byte(`{"runtime":`),
		AgentMemoryScope: "project",
	}}, threaddto.Started{ThreadID: "thread-1", CWD: "/fallback"})
	if err == nil {
		t.Fatal("StartSessionFromThreadEvent() error = nil, want invalid ConfigOverride parse failure")
	}
	if svc.startCalls != 0 {
		t.Fatalf("StartSession calls = %d, want 0 when metadata parse fails", svc.startCalls)
	}
}

func TestStartSessionFromThreadEventReturnsMetadataStoreError(t *testing.T) {
	want := errors.New("metadata store unavailable")
	svc := &recordingLifecycle{}
	err := StartSessionFromThreadEvent(svc, threadMetadataStoreStub{err: want}, threaddto.Started{
		ThreadID: "thread-1",
		CWD:      "/fallback",
	})
	if !errors.Is(err, want) {
		t.Fatalf("StartSessionFromThreadEvent() error = %v, want %v", err, want)
	}
	if svc.startCalls != 0 {
		t.Fatalf("StartSession calls = %d, want 0 when metadata lookup fails", svc.startCalls)
	}
}
