package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func mustWorkingDirectory(t *testing.T) string {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func removeCoordinatorTestState(t *testing.T, checkpoint localci.DockerDaemonIdentityCheckpoint) {
	t.Helper()
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		t.Errorf("coordinatorRuntimeRoot() error = %v", err)
		return
	}
	prefixes := []string{
		"localci-coordinator-" + checkpoint.IdentityKey + ".db",
		"localci-scheduler-" + checkpoint.IdentityKey + ".db",
		"localci-scheduler-" + checkpoint.IdentityKey + ".lock",
		"s-" + checkpoint.IdentityKey[:32] + ".sock",
	}
	for _, prefix := range prefixes {
		matches, _ := filepath.Glob(filepath.Join(runtimeRoot, prefix+"*"))
		for _, match := range matches {
			if err := os.Remove(match); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove test state %s: %v", match, err)
			}
		}
	}
}

func productionProvisionOwnerStatus(
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) (jobStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := dialCoordinator(ctx, checkpoint)
	if err != nil {
		return jobStatus{}, err
	}
	defer client.Close()
	records, err := client.store.jobs(ctx)
	if err != nil {
		return jobStatus{}, err
	}
	if len(records) != 1 {
		return jobStatus{}, fmt.Errorf("production provision owner jobs = %d, want 1", len(records))
	}
	return client.Status(ctx, records[0].JobID)
}

func productionProvisionOwnerEvidence(checkpoint localci.DockerDaemonIdentityCheckpoint) string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := dialCoordinator(ctx, checkpoint)
	if err != nil {
		return err.Error()
	}
	defer client.Close()
	records, err := client.store.jobs(ctx)
	if err != nil {
		return "jobs: " + err.Error()
	}
	snapshot, err := client.scheduler.Snapshot(ctx)
	if err != nil {
		return "scheduler: " + err.Error()
	}
	data, err := json.Marshal(struct {
		Jobs      []coordinatorJobRecord    `json:"jobs"`
		Scheduler localci.SchedulerSnapshot `json:"scheduler"`
	}{Jobs: records, Scheduler: snapshot})
	if err != nil {
		return "encode: " + err.Error()
	}
	return string(data)
}

func productionProvisionHookConnector(
	config productionCoordinatorConfig,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) hookCoordinatorConnector {
	return func(ctx context.Context) (hookCoordinator, error) {
		planner, err := newProductionCandidateSubmissionPlanner(ctx, config)
		if err != nil {
			return nil, err
		}
		client, err := newDeferredCoordinatorClient(
			ctx, checkpoint, planner,
			func(connectCtx context.Context) (*localci.SchedulerClient, error) {
				return localci.DialScheduler(connectCtx, checkpoint.SchedulerConfig)
			},
		)
		if err != nil {
			return nil, err
		}
		authority, err := newProductionHookResultReceiptAuthority(ctx, config)
		if err != nil {
			return nil, errors.Join(err, client.Close())
		}
		grants, err := newProductionActionGrantService(config, client.store, authority)
		if err != nil {
			return nil, errors.Join(err, client.Close())
		}
		return &hookCoordinatorBridge{client: client, authority: authority, grants: grants}, nil
	}
}
