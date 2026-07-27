package thread

import (
	"context"
	"errors"
	"testing"
)

func TestArchivePersistenceDoesNotSplitWhenAtomicWriteFails(t *testing.T) {
	t.Parallel()

	injected := errors.New("injected atomic archive failure")
	svc, threadStore := newAtomicArchiveFailureService(statusCreated, false, injected)
	err := svc.Archive(context.Background(), "thread-archive-atomic")
	if !errors.Is(err, injected) {
		t.Fatalf("Archive() error = %v, want injected atomic error", err)
	}
	if threadStore.status.Status == statusArchived {
		t.Fatalf("Archive() persisted thread status %q after atomic write failed", threadStore.status.Status)
	}
}

func TestUnarchivePersistenceDoesNotSplitWhenAtomicWriteFails(t *testing.T) {
	t.Parallel()

	injected := errors.New("injected atomic unarchive failure")
	svc, threadStore := newAtomicArchiveFailureService(statusArchived, true, injected)
	err := svc.Unarchive(context.Background(), "thread-archive-atomic")
	if !errors.Is(err, injected) {
		t.Fatalf("Unarchive() error = %v, want injected atomic error", err)
	}
	if threadStore.status.Status == statusCreated {
		t.Fatalf("Unarchive() persisted thread status %q after atomic write failed", threadStore.status.Status)
	}
}

func newAtomicArchiveFailureService(
	status string,
	archived bool,
	injected error,
) (*service, *recordingThreadStore) {
	bindingStore := &stubThreadBindingStore{binding: &BindingRecord{
		AgentID: "agent-archive-atomic", CodexThreadID: "thread-archive-atomic", Archived: archived,
	}}
	threadStore := &recordingThreadStore{stubThreadStore: &stubThreadStore{thread: &ThreadRecord{
		ThreadID: "thread-archive-atomic", AgentID: "agent-archive-atomic", Status: status,
	}}}
	svc := &service{bindingStore: bindingStore, threadStore: threadStore}
	archiveState := attachStubArchiveStateStore(svc)
	archiveState.err = injected
	return svc, threadStore
}
