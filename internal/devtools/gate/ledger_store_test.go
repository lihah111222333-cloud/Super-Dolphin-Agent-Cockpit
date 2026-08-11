package gate

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/sync/errgroup"
)

func TestDurationLedgerStoreRoundTripAndCAS(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	first := testDurationLedger(101)
	setDurationSampleGenerations(first.Samples, 1)
	snapshot, err := store.CompareAndSwap(0, first)
	if err != nil {
		t.Fatalf("CompareAndSwap(create) error = %v", err)
	}
	assertDurationLedgerSnapshot(t, snapshot, 1, first, "CompareAndSwap(create)")

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertDurationLedgerSnapshot(t, loaded, snapshot.Generation, snapshot.Ledger, "Load()")

	second := testDurationLedger(202)
	setDurationSampleGenerations(second.Samples, 2)
	snapshot, err = store.CompareAndSwap(loaded.Generation, second)
	if err != nil {
		t.Fatalf("CompareAndSwap(update) error = %v", err)
	}
	assertDurationLedgerSnapshot(t, snapshot, 2, second, "CompareAndSwap(update)")
}

func setDurationSampleGenerations(samples []DurationSample, generation uint64) {
	for index := range samples {
		samples[index].AcceptedGeneration = generation
	}
}

func assertDurationLedgerSnapshot(t *testing.T, snapshot DurationLedgerSnapshot, generation uint64, ledger DurationLedger, operation string) {
	t.Helper()
	if snapshot.Generation != generation {
		t.Fatalf("%s generation = %d, want %d", operation, snapshot.Generation, generation)
	}
	if !reflect.DeepEqual(snapshot.Ledger, ledger) {
		t.Fatalf("%s = %#v, want ledger %#v", operation, snapshot.Ledger, ledger)
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
	seedAcceptedGenerationForTest(t, store, 1)
	start := make(chan struct{})
	var group errgroup.Group
	for _, duration := range []int64{101, 202, 303, 404} {
		group.Go(func() error {
			<-start
			_, err := store.AppendSamples(1, []DurationSample{
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
	for _, sample := range snapshot.Ledger.Samples {
		if sample.AcceptedGeneration != 1 {
			t.Fatalf("loaded sample accepted generation = %d, want 1", sample.AcceptedGeneration)
		}
	}
}

func TestDurationLedgerStoreAppendSamplesCompactsEachExactBucket(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, DurationLedger{Version: durationLedgerVersion}); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	samples := make([]DurationSample, 0, 16)
	for duration := int64(1); duration <= 12; duration++ {
		samples = append(samples, testDurationSample("unit", testWorkloadDigest, true, duration))
	}
	for duration := int64(101); duration <= 104; duration++ {
		samples = append(samples, testDurationSample("unit", testWorkloadDigest, false, duration))
	}
	snapshot, err := store.AppendSamples(1, samples)
	if err != nil {
		t.Fatal(err)
	}
	wantDurations := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 101, 102, 103, 104}
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
	seedAcceptedGenerationForTest(t, store, 1)
	samples := make([]DurationSample, 0, 4)
	for index, marker := range []string{"a", "b", "c", "d"} {
		samples = append(samples, testDurationSample("unit", strings.Repeat(marker, 64), true, int64(index+1)))
	}
	snapshot, err := store.AppendSamples(1, samples)
	if err != nil {
		t.Fatal(err)
	}
	wantDurations := []int64{1, 2, 3, 4}
	gotDurations := make([]int64, 0, len(snapshot.Ledger.Samples))
	for _, sample := range snapshot.Ledger.Samples {
		gotDurations = append(gotDurations, sample.DurationMS)
	}
	if !reflect.DeepEqual(gotDurations, wantDurations) {
		t.Fatalf("retained execution durations = %v, want %v", gotDurations, wantDurations)
	}
}

func TestHistoricalRootWriterRequiresAcceptedGenerationAuthority(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	sample := testDurationSample("unit", testWorkloadDigest, true, 1)
	if _, err := store.AppendSamplesFast(1, []DurationSample{sample}); !errors.Is(err, ErrRemoteBaselineStateNotFound) {
		t.Fatalf("append without accepted authority error = %v, want ErrRemoteBaselineStateNotFound", err)
	}
	seedAcceptedGenerationForTest(t, store, 3)
	if _, err := store.AppendSamplesFast(4, []DurationSample{sample}); err == nil || !strings.Contains(err.Error(), "was never accepted") {
		t.Fatalf("append future accepted generation error = %v", err)
	}
	if _, err := store.AppendSamplesFast(2, []DurationSample{sample}); err != nil {
		t.Fatalf("append previously accepted generation: %v", err)
	}
}

func newTestDurationLedgerStore(t *testing.T) *DurationLedgerStore {
	t.Helper()
	store, err := NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	return store
}

func seedAcceptedGenerationForTest(t *testing.T, store *DurationLedgerStore, generation uint64) {
	t.Helper()
	stateJSON := fmt.Sprintf(`{"schema_version":%d,"generation":%d,"execution_provider":%q,"region_id":"cn-shenzhen","image_cache_snapshot_id":"snapshot-%d"}`, cicontract.BaselineStateSchemaVersion, generation, cicontract.ExecutionProviderID, generation)
	stateDigest := sha256.Sum256([]byte(stateJSON))
	seedRemoteBaselineState(t, store, RemoteBaselineStateRecord{
		Generation:  generation,
		StateJSON:   []byte(stateJSON),
		StateSHA256: fmt.Sprintf("sha256:%x", stateDigest),
	})
}

func testDurationLedger(durationMS int64) DurationLedger {
	return DurationLedger{
		Version: durationLedgerVersion,
		Samples: []DurationSample{testDurationSample("unit", testWorkloadDigest, true, durationMS)},
	}
}
