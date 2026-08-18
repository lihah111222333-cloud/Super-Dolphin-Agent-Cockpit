package remoteci

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCreateJournaledContainerGroupPersistsIntentBeforeProviderSideEffect(t *testing.T) {
	const jobID = "job-cleanup-order"
	const name = "sdci-cleanup-order-s00"
	store, err := gate.NewDurationLedgerStore(t.TempDir() + "/cleanup.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InitializeLocalAuthority(); err != nil {
		t.Fatal(err)
	}
	if err := store.BeginRemoteCleanupJournal(jobID, "source/"+jobID+"/"); err != nil {
		t.Fatal(err)
	}
	runtime := &cleanupIntentRuntime{t: t, store: store, jobID: jobID, name: name}
	coordinator := &Coordinator{runtime: runtime}
	id, err := coordinator.createJournaledContainerGroup(context.Background(), jobID, 0, RunInput{LedgerStore: store}, eci.CreateRequest{ContainerGroupName: name})
	if err != nil {
		t.Fatal(err)
	}
	if id != "eci-cleanup-order" || !runtime.created {
		t.Fatalf("created ID=%q providerCalled=%t", id, runtime.created)
	}
	journal, err := store.LoadRemoteCleanupJournal(jobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(journal.Targets) != 2 || journal.Targets[0].ProviderID != id {
		t.Fatalf("cleanup journal after provider ACK = %#v", journal)
	}
}

type cleanupIntentRuntime struct {
	t       *testing.T
	store   *gate.DurationLedgerStore
	jobID   string
	name    string
	created bool
}

func (runtime *cleanupIntentRuntime) CreateContainerGroup(_ context.Context, request eci.CreateRequest) (eci.ContainerGroup, error) {
	runtime.t.Helper()
	journal, err := runtime.store.LoadRemoteCleanupJournal(runtime.jobID)
	if err != nil {
		runtime.t.Fatal(err)
	}
	if request.ContainerGroupName != runtime.name || len(journal.Targets) != 2 || journal.Targets[0].DeterministicName != runtime.name || journal.Targets[0].ProviderID != "" {
		runtime.t.Fatalf("provider observed incomplete cleanup intent: request=%#v journal=%#v", request, journal)
	}
	runtime.created = true
	return eci.ContainerGroup{ID: "eci-cleanup-order", Name: runtime.name}, nil
}

func (*cleanupIntentRuntime) DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error) {
	return nil, errors.New("unexpected DescribeContainerGroups")
}

func (*cleanupIntentRuntime) DescribeContainerLog(context.Context, string, string) (string, error) {
	return "", errors.New("unexpected DescribeContainerLog")
}

func (*cleanupIntentRuntime) DeleteContainerGroup(context.Context, string) error {
	return errors.New("unexpected DeleteContainerGroup")
}

func (*cleanupIntentRuntime) ConfirmContainerGroupAbsent(context.Context, string) (bool, error) {
	return false, errors.New("unexpected ConfirmContainerGroupAbsent")
}
