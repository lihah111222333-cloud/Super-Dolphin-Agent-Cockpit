package gate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestDurationLedgerStoreRoundTripAndCAS(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	first := testDurationLedger(101)
	snapshot, err := store.CompareAndSwap(0, first)
	if err != nil {
		t.Fatalf("CompareAndSwap(create) error = %v", err)
	}
	if snapshot.Generation != 1 || !reflect.DeepEqual(snapshot.Ledger, first) {
		t.Fatalf("CompareAndSwap(create) = %#v, want generation 1 and %#v", snapshot, first)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Generation != snapshot.Generation || !reflect.DeepEqual(loaded.Ledger, snapshot.Ledger) {
		t.Fatalf("Load() = %#v, want %#v", loaded, snapshot)
	}

	second := testDurationLedger(202)
	snapshot, err = store.CompareAndSwap(loaded.Generation, second)
	if err != nil {
		t.Fatalf("CompareAndSwap(update) error = %v", err)
	}
	if snapshot.Generation != 2 || !reflect.DeepEqual(snapshot.Ledger, second) {
		t.Fatalf("CompareAndSwap(update) = %#v, want generation 2 and %#v", snapshot, second)
	}
}

func TestDurationLedgerStoreRejectsStrictJSONAndStaleGeneration(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, testDurationLedger(101)); err != nil {
		t.Fatalf("CompareAndSwap(create) error = %v", err)
	}
	if _, err := store.CompareAndSwap(0, testDurationLedger(202)); !errors.Is(err, ErrDurationLedgerConflict) {
		t.Fatalf("CompareAndSwap(stale) error = %v, want ErrDurationLedgerConflict", err)
	}

	invalid := []byte(`{"generation":1,"ledger":{"version":1,"samples":[]},"extra":true}`)
	if err := os.WriteFile(store.path, invalid, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("Load() error = nil for unknown JSON field")
	}
}

func TestDurationLedgerStoreConcurrentCASFailsOneWriter(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, testDurationLedger(101)); err != nil {
		t.Fatalf("CompareAndSwap(create) error = %v", err)
	}

	start := make(chan struct{})
	errorsByWriter := make(chan error, 2)
	var group errgroup.Group
	for _, duration := range []int64{202, 303} {
		duration := duration
		group.Go(func() error {
			<-start
			_, err := store.CompareAndSwap(1, testDurationLedger(duration))
			errorsByWriter <- err
			return nil
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent CAS group error = %v", err)
	}
	close(errorsByWriter)

	successes := 0
	failures := 0
	for err := range errorsByWriter {
		if err == nil {
			successes++
			continue
		}
		if errors.Is(err, ErrDurationLedgerConflict) || errors.Is(err, ErrDurationLedgerBusy) {
			failures++
			continue
		}
		t.Fatalf("CompareAndSwap(concurrent) error = %v", err)
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent CAS successes=%d failures=%d, want 1 and 1", successes, failures)
	}
}

func TestDurationLedgerStoreAppendSamplesMergesConcurrentWriters(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, DurationLedger{Version: durationLedgerVersion}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var group errgroup.Group
	for _, duration := range []int64{101, 202, 303, 404} {
		duration := duration
		group.Go(func() error {
			<-start
			_, err := store.AppendSamples([]DurationSample{
				testDurationSample("unit", testWorkloadDigest, true, duration),
			})
			return err
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("AppendSamples(concurrent) error = %v", err)
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Ledger.Samples) != 4 || snapshot.Generation != 5 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestDurationLedgerStoreAppendSamplesCompactsEachExactBucket(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, DurationLedger{Version: durationLedgerVersion}); err != nil {
		t.Fatal(err)
	}
	samples := make([]DurationSample, 0, 16)
	for duration := int64(1); duration <= 12; duration++ {
		samples = append(samples, testDurationSample("unit", testWorkloadDigest, true, duration))
	}
	for duration := int64(101); duration <= 104; duration++ {
		samples = append(samples, testDurationSample("unit", testWorkloadDigest, false, duration))
	}
	snapshot, err := store.AppendSamples(samples)
	if err != nil {
		t.Fatal(err)
	}
	wantDurations := []int64{5, 6, 7, 8, 9, 10, 11, 12, 103, 104}
	gotDurations := make([]int64, 0, len(snapshot.Ledger.Samples))
	for _, sample := range snapshot.Ledger.Samples {
		gotDurations = append(gotDurations, sample.DurationMS)
	}
	if !reflect.DeepEqual(gotDurations, wantDurations) {
		t.Fatalf("compacted durations = %v, want %v", gotDurations, wantDurations)
	}
}

func TestDurationLedgerStoreAppendSamplesRetainsThreeRecentExecutions(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, DurationLedger{Version: durationLedgerVersion}); err != nil {
		t.Fatal(err)
	}
	samples := make([]DurationSample, 0, 4)
	for index, marker := range []string{"a", "b", "c", "d"} {
		samples = append(samples, testDurationSample("unit", strings.Repeat(marker, 64), true, int64(index+1)))
	}
	snapshot, err := store.AppendSamples(samples)
	if err != nil {
		t.Fatal(err)
	}
	wantDurations := []int64{2, 3, 4}
	gotDurations := make([]int64, 0, len(snapshot.Ledger.Samples))
	for _, sample := range snapshot.Ledger.Samples {
		gotDurations = append(gotDurations, sample.DurationMS)
	}
	if !reflect.DeepEqual(gotDurations, wantDurations) {
		t.Fatalf("retained execution durations = %v, want %v", gotDurations, wantDurations)
	}
}

func newTestDurationLedgerStore(t *testing.T) *DurationLedgerStore {
	t.Helper()
	store, err := NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.json"))
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	return store
}

func testDurationLedger(durationMS int64) DurationLedger {
	return DurationLedger{
		Version: durationLedgerVersion,
		Samples: []DurationSample{testDurationSample("unit", testWorkloadDigest, true, durationMS)},
	}
}
