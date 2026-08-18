package remoteci

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCleanupOrphanedRemoteJobECIResolutionAndAbsence(t *testing.T) {
	const jobID = "job-cleanup"
	for _, test := range []struct {
		name        string
		target      gate.RemoteCleanupTarget
		found       []eci.ContainerGroup
		wantDelete  string
		wantFind    bool
		wantFailure string
	}{
		{name: "known provider ID", target: cleanupECITarget(jobID, "eci-known"), wantDelete: "eci-known"},
		{name: "missing provider ID already absent", target: cleanupECITarget(jobID, ""), wantFind: true},
		{name: "missing provider ID resolves exact name and tag", target: cleanupECITarget(jobID, ""), found: []eci.ContainerGroup{{ID: "eci-found", Name: "sdci-cleanup-s00", Tags: []eci.ContainerGroupTag{{Key: "super-dolphin-job", Value: jobID}}}}, wantFind: true, wantDelete: "eci-found"},
		{name: "multiple matches fail closed", target: cleanupECITarget(jobID, ""), found: []eci.ContainerGroup{{ID: "eci-one", Name: "sdci-cleanup-s00", Tags: []eci.ContainerGroupTag{{Key: "super-dolphin-job", Value: jobID}}}, {ID: "eci-two", Name: "sdci-cleanup-s00", Tags: []eci.ContainerGroupTag{{Key: "super-dolphin-job", Value: jobID}}}}, wantFind: true, wantFailure: "ambiguous"},
		{name: "tag drift fails closed", target: cleanupECITarget(jobID, ""), found: []eci.ContainerGroup{{ID: "eci-found", Name: "sdci-cleanup-s00", Tags: []eci.ContainerGroupTag{{Key: "super-dolphin-job", Value: "job-other"}}}}, wantFind: true, wantFailure: "drifted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := &cleanupJournalRuntime{found: test.found}
			err := CleanupOrphanedRemoteJob(context.Background(), jobID, cleanupJournalConfig(), cleanupJournalStore{journal: gate.RemoteCleanupJournal{JobID: jobID, Targets: []gate.RemoteCleanupTarget{test.target}}}, &cleanupJournalObjectStore{}, runtime)
			if test.wantFailure != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantFailure) {
					t.Fatalf("CleanupOrphanedRemoteJob() error = %v, want %q", err, test.wantFailure)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if runtime.deleted != test.wantDelete || runtime.findCalls != cleanupJournalBoolToInt(test.wantFind) {
				t.Fatalf("runtime deleted=%q findCalls=%d, want delete=%q find=%t", runtime.deleted, runtime.findCalls, test.wantDelete, test.wantFind)
			}
		})
	}
}

func TestCleanupOrphanedRemoteJobOSSPrefixAndRepeatAreIdempotent(t *testing.T) {
	const jobID = "job-cleanup-oss"
	objectStore := &cleanupJournalObjectStore{deleteErr: errors.New("already absent")}
	journal := gate.RemoteCleanupJournal{JobID: jobID, Targets: []gate.RemoteCleanupTarget{{Kind: gate.RemoteCleanupTargetOSSPrefix, Key: "source/job-cleanup-oss/"}}}
	for attempt := range 2 {
		if err := CleanupOrphanedRemoteJob(context.Background(), jobID, cleanupJournalConfig(), cleanupJournalStore{journal: journal}, objectStore, &cleanupJournalRuntime{}); err != nil {
			t.Fatalf("cleanup attempt %d: %v", attempt+1, err)
		}
	}
	if objectStore.deleteCalls != 2 || objectStore.confirmCalls != 2 {
		t.Fatalf("OSS cleanup calls delete=%d confirm=%d, want 2/2", objectStore.deleteCalls, objectStore.confirmCalls)
	}
}

func TestCleanupOrphanedRemoteJobRequiresAbsenceProof(t *testing.T) {
	const jobID = "job-cleanup-proof"
	runtime := &cleanupJournalRuntime{confirmErr: errors.New("provider unavailable")}
	err := CleanupOrphanedRemoteJob(context.Background(), jobID, cleanupJournalConfig(), cleanupJournalStore{journal: gate.RemoteCleanupJournal{JobID: jobID, Targets: []gate.RemoteCleanupTarget{cleanupECITarget(jobID, "eci-proof")}}}, &cleanupJournalObjectStore{}, runtime)
	if err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("CleanupOrphanedRemoteJob() error = %v, want absence proof failure", err)
	}
}

func cleanupECITarget(jobID, providerID string) gate.RemoteCleanupTarget {
	return gate.RemoteCleanupTarget{Kind: gate.RemoteCleanupTargetECIContainerGroup, Key: "sdci-cleanup-s00", ProviderID: providerID, DeterministicName: "sdci-cleanup-s00", JobTag: jobID}
}

func cleanupJournalConfig() CleanupJournalConfig {
	return CleanupJournalConfig{PollInterval: time.Millisecond, Timeout: time.Second}
}

func cleanupJournalBoolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

type cleanupJournalStore struct{ journal gate.RemoteCleanupJournal }

func (store cleanupJournalStore) LoadRemoteCleanupJournal(string) (gate.RemoteCleanupJournal, error) {
	return store.journal, nil
}

type cleanupJournalObjectStore struct {
	deleteErr, confirmErr     error
	deleteCalls, confirmCalls int
}

func (store *cleanupJournalObjectStore) DeletePrefix(context.Context, string) error {
	store.deleteCalls++
	return store.deleteErr
}

func (store *cleanupJournalObjectStore) ConfirmPrefixEmpty(context.Context, string) (bool, error) {
	store.confirmCalls++
	return store.confirmErr == nil, store.confirmErr
}

type cleanupJournalRuntime struct {
	found                          []eci.ContainerGroup
	findErr, deleteErr, confirmErr error
	findCalls                      int
	deleted                        string
}

func (runtime *cleanupJournalRuntime) FindContainerGroupsByNameAndJobTag(_ context.Context, _, _ string) ([]eci.ContainerGroup, error) {
	runtime.findCalls++
	return runtime.found, runtime.findErr
}

func (runtime *cleanupJournalRuntime) DeleteContainerGroup(_ context.Context, id string) error {
	runtime.deleted = id
	return runtime.deleteErr
}

func (runtime *cleanupJournalRuntime) ConfirmContainerGroupAbsent(context.Context, string) (bool, error) {
	return runtime.confirmErr == nil, runtime.confirmErr
}
