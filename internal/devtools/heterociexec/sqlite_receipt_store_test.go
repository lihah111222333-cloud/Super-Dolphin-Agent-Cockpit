package heterociexec

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociworkflow"

	_ "modernc.org/sqlite"
)

func TestSQLiteReceiptStoreCrossInstanceClaimAndTerminalDurability(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch-receipts.sqlite")
	first := openSQLiteReceiptStoreForTest(t, path)
	second := openSQLiteReceiptStoreForTest(t, path)
	batch := commandExecutorBatch()
	digest := commandExecutorCommandDigest(t, []string{"tool"})

	type claimResult struct {
		store   *SQLiteReceiptStore
		claimed bool
		err     error
	}
	results := make(chan claimResult, 2)
	var group sync.WaitGroup
	for _, store := range []*SQLiteReceiptStore{first, second} {
		candidate := store
		group.Go(func() {
			claimed, err := candidate.Claim(context.Background(), batch, digest)
			results <- claimResult{store: candidate, claimed: claimed, err: err}
		})
	}
	group.Wait()
	close(results)

	var winner *SQLiteReceiptStore
	claimedCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("Claim() error = %v", result.err)
		}
		if result.claimed {
			claimedCount++
			winner = result.store
		}
	}
	if claimedCount != 1 {
		t.Fatalf("cross-instance claimed count = %d, want 1", claimedCount)
	}
	result := canonicalBatchResult(batch, heteroci.LeaseStateSucceeded)
	if err := winner.WriteIfAbsent(context.Background(), result); err != nil {
		t.Fatalf("WriteIfAbsent() error = %v", err)
	}
	assertSQLiteReceiptVisible(t, first, batch, digest, result)
	assertSQLiteReceiptVisible(t, second, batch, digest, result)
	requireSQLiteReceiptStoreClose(t, first)
	requireSQLiteReceiptStoreClose(t, second)

	reopened := openSQLiteReceiptStoreForTest(t, path)
	assertSQLiteReceiptVisible(t, reopened, batch, digest, result)
	requireSQLiteReceiptStoreClose(t, reopened)
}

func TestSQLiteReceiptStoreRejectsCommandDriftAndTerminalConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch-receipts.sqlite")
	first := openSQLiteReceiptStoreForTest(t, path)
	defer requireSQLiteReceiptStoreClose(t, first)
	second := openSQLiteReceiptStoreForTest(t, path)
	defer requireSQLiteReceiptStoreClose(t, second)
	batch := commandExecutorBatch()
	digest := commandExecutorCommandDigest(t, []string{"tool"})
	drifted := commandExecutorCommandDigest(t, []string{"tool", "drifted"})

	claimed, err := first.Claim(context.Background(), batch, digest)
	if err != nil || !claimed {
		t.Fatalf("first Claim() = %t, %v", claimed, err)
	}
	if _, err := second.Claim(context.Background(), batch, drifted); !errors.Is(err, ErrSQLiteReceiptConflict) {
		t.Fatalf("drifted Claim() error = %v, want ErrSQLiteReceiptConflict", err)
	}
	if _, _, err := second.Read(context.Background(), batch, drifted); !errors.Is(err, ErrSQLiteReceiptConflict) {
		t.Fatalf("drifted Read() error = %v, want ErrSQLiteReceiptConflict", err)
	}

	result := canonicalBatchResult(batch, heteroci.LeaseStateSucceeded)
	if err := first.WriteIfAbsent(context.Background(), result); err != nil {
		t.Fatalf("WriteIfAbsent() error = %v", err)
	}
	conflict := canonicalBatchResult(batch, heteroci.LeaseStateFailed)
	if err := second.WriteIfAbsent(context.Background(), conflict); !errors.Is(err, ErrSQLiteReceiptConflict) {
		t.Fatalf("conflicting WriteIfAbsent() error = %v, want ErrSQLiteReceiptConflict", err)
	}
	if err := second.WriteIfAbsent(context.Background(), result); err != nil {
		t.Fatalf("idempotent WriteIfAbsent() error = %v", err)
	}
	assertSQLiteReceiptVisible(t, second, batch, digest, result)
}

func TestSQLiteReceiptStoreRequiresClaimAndPersistsClaimTombstone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch-receipts.sqlite")
	store := openSQLiteReceiptStoreForTest(t, path)
	batch := commandExecutorBatch()
	digest := commandExecutorCommandDigest(t, []string{"tool"})
	result := canonicalBatchResult(batch, heteroci.LeaseStateSucceeded)
	if err := store.WriteIfAbsent(context.Background(), result); !errors.Is(err, ErrSQLiteReceiptConflict) {
		t.Fatalf("unclaimed WriteIfAbsent() error = %v, want ErrSQLiteReceiptConflict", err)
	}
	claimed, err := store.Claim(context.Background(), batch, digest)
	if err != nil || !claimed {
		t.Fatalf("Claim() = %t, %v", claimed, err)
	}
	requireSQLiteReceiptStoreClose(t, store)

	reopened := openSQLiteReceiptStoreForTest(t, path)
	defer requireSQLiteReceiptStoreClose(t, reopened)
	claimed, err = reopened.Claim(context.Background(), batch, digest)
	if err != nil || claimed {
		t.Fatalf("reopened Claim() = %t, %v, want durable tombstone", claimed, err)
	}
	if _, found, err := reopened.Read(context.Background(), batch, digest); err != nil || found {
		t.Fatalf("Read(claimed tombstone) = found:%t err:%v, want absent terminal", found, err)
	}
}

func TestSQLiteReceiptStoreFailsFastOnContextCloseAndSchemaDrift(t *testing.T) {
	assertSQLiteReceiptContextAndClose(t)

	for _, test := range []struct {
		name string
		sql  string
	}{
		{name: "unknown object", sql: "CREATE TABLE unrelated(value TEXT)"},
		{name: "drifted receipt table", sql: "CREATE TABLE heteroci_batch_receipts(batch_id TEXT PRIMARY KEY)"},
		{name: "empty schema version drift", sql: "PRAGMA user_version = 7"},
	} {
		t.Run(test.name, func(t *testing.T) { assertSQLiteReceiptSchemaRejects(t, test.sql) })
	}
}

func assertSQLiteReceiptContextAndClose(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "batch-receipts.sqlite")
	store := openSQLiteReceiptStoreForTest(t, path)
	batch := commandExecutorBatch()
	digest := commandExecutorCommandDigest(t, []string{"tool"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Claim(ctx, batch, digest); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Claim() error = %v, want context.Canceled", err)
	}
	claimed, err := store.Claim(context.Background(), batch, digest)
	if err != nil || !claimed {
		t.Fatalf("Claim() after canceled call = %t, %v", claimed, err)
	}
	requireSQLiteReceiptStoreClose(t, store)
	if _, _, err := store.Read(context.Background(), batch, digest); !errors.Is(err, ErrSQLiteReceiptStoreClosed) {
		t.Fatalf("Read() after Close error = %v, want ErrSQLiteReceiptStoreClosed", err)
	}
	if err := store.Close(); !errors.Is(err, ErrSQLiteReceiptStoreClosed) {
		t.Fatalf("second Close() error = %v, want ErrSQLiteReceiptStoreClosed", err)
	}
}

func assertSQLiteReceiptSchemaRejects(t *testing.T, statement string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "batch-receipts.sqlite")
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := database.Exec(statement); err != nil {
		t.Fatalf("create drifted schema: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close drifted schema: %v", err)
	}
	if store, err := OpenSQLiteReceiptStore(context.Background(), path); err == nil {
		_ = store.Close()
		t.Fatal("OpenSQLiteReceiptStore accepted drifted schema")
	}
}

func TestSQLiteReceiptStoreDetectsTamperingAndPersistsAllResultFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "batch-receipts.sqlite")
	store := openSQLiteReceiptStoreForTest(t, path)
	defer requireSQLiteReceiptStoreClose(t, store)
	batch := commandExecutorBatch()
	digest := commandExecutorCommandDigest(t, []string{"tool"})
	result := canonicalBatchResult(batch, heteroci.LeaseStateSucceeded)
	claimed, err := store.Claim(context.Background(), batch, digest)
	if err != nil || !claimed {
		t.Fatalf("Claim() = %t, %v", claimed, err)
	}
	if err := store.WriteIfAbsent(context.Background(), result); err != nil {
		t.Fatalf("WriteIfAbsent() error = %v", err)
	}
	assertPersistedBatchResultFieldClosure(t, path)

	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open tamper database: %v", err)
	}
	if _, err := database.Exec("UPDATE heteroci_batch_receipts SET result_json = ? WHERE batch_id = ? AND material_digest = ?", []byte(`{}`), batch.BatchID, batch.MaterialDigest); err != nil {
		t.Fatalf("tamper receipt: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close tamper database: %v", err)
	}
	if _, _, err := store.Read(context.Background(), batch, digest); err == nil {
		t.Fatal("Read accepted tampered receipt")
	}
}

func openSQLiteReceiptStoreForTest(t *testing.T, path string) *SQLiteReceiptStore {
	t.Helper()
	resolvedDirectory, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", filepath.Dir(path), err)
	}
	resolvedPath := filepath.Join(resolvedDirectory, filepath.Base(path))
	store, err := OpenSQLiteReceiptStore(context.Background(), resolvedPath)
	if err != nil {
		t.Fatalf("OpenSQLiteReceiptStore: %v", err)
	}
	return store
}

func requireSQLiteReceiptStoreClose(t *testing.T, store *SQLiteReceiptStore) {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func commandExecutorCommandDigest(t *testing.T, argv []string) string {
	t.Helper()
	command, err := EncodeCommand(heteroci.PlatformLinuxAMD64, argv)
	if err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	digest, err := DeriveCommandDigest(command)
	if err != nil {
		t.Fatalf("DeriveCommandDigest: %v", err)
	}
	return digest
}

func assertSQLiteReceiptVisible(t *testing.T, store *SQLiteReceiptStore, batch heterociworkflow.ExecutionBatch, commandDigest string, want heterociworkflow.BatchResult) {
	t.Helper()
	got, found, err := store.Read(context.Background(), batch, commandDigest)
	if err != nil || !found {
		t.Fatalf("Read() = found:%t err:%v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}
}

func assertPersistedBatchResultFieldClosure(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open field closure database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close field closure database: %v", err)
		}
	}()
	var raw string
	if err := database.QueryRow("SELECT CAST(result_json AS TEXT) FROM heteroci_batch_receipts WHERE state = 'terminal'").Scan(&raw); err != nil {
		t.Fatalf("read persisted result JSON: %v", err)
	}
	for _, field := range reflect.VisibleFields(reflect.TypeFor[heterociworkflow.BatchResult]()) {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			t.Fatalf("BatchResult field %s lacks a persisted JSON identity", field.Name)
		}
		if !strings.Contains(raw, `"`+name+`"`) {
			t.Fatalf("persisted result JSON omitted BatchResult field %q", name)
		}
	}
}
