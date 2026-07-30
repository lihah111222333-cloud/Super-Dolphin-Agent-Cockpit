package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

func TestCoordinatorWriteTransactionReservesBeforeSnapshotRead(t *testing.T) {
	checkpoint := coordinatorTestCheckpoint(t)
	first, second := openCoordinatorStorePair(t, checkpoint)
	record := createCoordinatorLockTestJob(t, first, "first", "1")
	tx := beginCoordinatorLockSnapshot(t, first, record.JobID)
	defer tx.Rollback()
	repositoryRoot := mustWorkingDirectory(t)
	secondPlan := mustTestGatePlan(t, "2")

	writerStarted := make(chan struct{})
	writerDone := make(chan struct{})
	writerGroup := errgroup.Group{}
	writerGroup.Go(func() error {
		close(writerStarted)
		defer close(writerDone)
		_, err := second.createJob(
			context.Background(), "hook-store-lock-second", "job-store-lock-second",
			repositoryRoot, secondPlan, localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
		)
		return err
	})
	<-writerStarted
	assertCoordinatorWriterBlocked(t, writerDone)
	commitCoordinatorLockSnapshot(t, tx, record.JobID)
	assertCoordinatorWriterResumed(t, writerDone)
	if err := writerGroup.Wait(); err != nil {
		t.Fatalf("concurrent writer after commit: %v", err)
	}
}

func openCoordinatorStorePair(
	t *testing.T,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) (*coordinatorStore, *coordinatorStore) {
	t.Helper()
	first, err := openCoordinatorStore(context.Background(), checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := first.close(); err != nil {
			t.Errorf("close first store: %v", err)
		}
	})
	second, err := openCoordinatorStore(context.Background(), checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := second.close(); err != nil {
			t.Errorf("close second store: %v", err)
		}
	})
	return first, second
}

func createCoordinatorLockTestJob(
	t *testing.T,
	store *coordinatorStore,
	suffix string,
	treeCharacter string,
) coordinatorJobRecord {
	t.Helper()
	record, err := store.createJob(
		context.Background(), "hook-store-lock-"+suffix, "job-store-lock-"+suffix,
		mustWorkingDirectory(t), mustTestGatePlan(t, treeCharacter), localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func beginCoordinatorLockSnapshot(t *testing.T, store *coordinatorStore, jobID string) *sql.Tx {
	t.Helper()
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var state string
	if err := tx.QueryRowContext(
		context.Background(), "SELECT state FROM coordinator_jobs WHERE job_id = ?", jobID,
	).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if jobState(state) != jobStateQueued {
		t.Fatalf("lock test job state = %q", state)
	}
	return tx
}

func assertCoordinatorWriterBlocked(t *testing.T, writerDone <-chan struct{}) {
	t.Helper()
	select {
	case <-writerDone:
		t.Fatal("concurrent writer committed while read-modify-write transaction was active")
	case <-time.After(100 * time.Millisecond):
	}
}

func commitCoordinatorLockSnapshot(t *testing.T, tx *sql.Tx, jobID string) {
	t.Helper()
	if _, err := tx.ExecContext(
		context.Background(), "UPDATE coordinator_jobs SET error_text = error_text WHERE job_id = ?", jobID,
	); err != nil {
		t.Fatalf("upgrade coordinator transaction to writer: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertCoordinatorWriterResumed(t *testing.T, writerDone <-chan struct{}) {
	t.Helper()
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent writer did not resume after commit")
	}
}
