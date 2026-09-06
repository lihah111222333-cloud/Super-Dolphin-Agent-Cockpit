package orchestrationcommand

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type commandIntentReserveResult struct {
	record Record
	err    error
}

func TestCommandIntentStoreConcurrentReserveAcrossConnectionsHasOneDurableWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration-command-concurrent.sqlite")
	dbOne := openCommandIntentTestDBAtPath(t, path)
	dbTwo := openCommandIntentTestDBAtPath(t, path)
	storeOne, err := NewStore(dbOne)
	if err != nil {
		t.Fatalf("NewStore(dbOne) error = %v", err)
	}
	storeTwo, err := NewStore(dbTwo)
	if err != nil {
		t.Fatalf("NewStore(dbTwo) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := make(chan struct{})
	results := make(chan commandIntentReserveResult, 2)
	var workers sync.WaitGroup
	workers.Go(func() {
		reserveCommandIntentConcurrently(start, results, ctx, storeOne, commandIntentFixture("concurrent-command-one"))
	})
	workers.Go(func() {
		reserveCommandIntentConcurrently(start, results, ctx, storeTwo, commandIntentFixture("concurrent-command-two"))
	})
	close(start)
	workers.Wait()
	close(results)

	var observed []commandIntentReserveResult
	for result := range results {
		observed = append(observed, result)
	}
	if len(observed) != 2 {
		t.Fatalf("concurrent results = %d, want 2", len(observed))
	}
	for index, result := range observed {
		if result.err != nil {
			t.Fatalf("concurrent result[%d] error = %v", index, result.err)
		}
		assertCommandIntentRecord(t, result.record, result.record.Intent.CommandID)
	}
	if observed[0].record.Intent.CommandID != observed[1].record.Intent.CommandID {
		t.Fatalf("concurrent winners differ: %#v vs %#v", observed[0].record, observed[1].record)
	}
	assertCommandIntentRecordEqual(t, observed[1].record, observed[0].record)
	assertCommandIntentRowCount(t, dbOne, 1)
}

func TestCommandIntentStoreCloseReopenReturnsSameKeyRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orchestration-command-reopen.sqlite")
	db := openCommandIntentTestDBAtPath(t, path)
	store, err := NewStore(db)
	if err != nil {
		t.Fatalf("NewStore(first) error = %v", err)
	}
	ctx := context.Background()
	first, err := store.Reserve(ctx, commandIntentFixture("reopen-command"))
	if err != nil {
		t.Fatalf("Reserve(first) error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close first database: %v", err)
	}

	reopenedDB := openCommandIntentTestDBAtPath(t, path)
	reopenedStore, err := NewStore(reopenedDB)
	if err != nil {
		t.Fatalf("NewStore(reopened) error = %v", err)
	}
	reopened, err := reopenedStore.GetByKey(ctx, Key{Namespace: first.Intent.IdempotencyNamespace, Digest: first.Intent.IdempotencyKeyDigest})
	if err != nil {
		t.Fatalf("GetByKey(reopened) error = %v", err)
	}
	assertCommandIntentRecordEqual(t, reopened, first)
	assertCommandIntentRowCount(t, reopenedDB, 1)
}

func reserveCommandIntentConcurrently(start <-chan struct{}, results chan<- commandIntentReserveResult, ctx context.Context, store *Store, intent Intent) {
	<-start
	record, err := store.Reserve(ctx, intent)
	results <- commandIntentReserveResult{record: record, err: err}
}
