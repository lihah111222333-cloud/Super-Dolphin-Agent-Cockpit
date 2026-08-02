package remoteci

import (
	"context"
	"maps"
	"path/filepath"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type compatiblePassFixture struct {
	repository      string
	packageWorkload gate.Workload
	exactWorkload   gate.Workload
	input           RunInput
	oldTree         string
	ledger          *gate.DurationLedgerStore
	observedAt      time.Time
}

func newCompatiblePassFixture(t *testing.T) compatiblePassFixture {
	t.Helper()
	repository := newFingerprintRepository(t)
	packageWorkload, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard, "./internal/a", 1_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	ledger := newCompatiblePassLedger(t)
	fixture := compatiblePassFixture{
		repository: repository, packageWorkload: packageWorkload,
		exactWorkload: fingerprintGoTestWorkload(t, "TestValue", "./internal/a"),
		input:         remoteWorkloadCacheInputFixture(), ledger: ledger,
		oldTree:    coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}"),
		observedAt: time.Date(2026, time.July, 30, 2, 3, 4, 0, time.UTC),
	}
	fixture.input.RepositoryRoot = repository
	recordCompatiblePassEvidence(t, ledger, fixture.oldEntries(t), fixture.oldTree, fixture.observedAt)
	return fixture
}

func newCompatiblePassLedger(t *testing.T) *gate.DurationLedgerStore {
	t.Helper()
	ledger, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	return ledger
}

func (fixture compatiblePassFixture) workloads() []gate.Workload {
	return []gate.Workload{fixture.packageWorkload, fixture.exactWorkload}
}

func (fixture compatiblePassFixture) oldEntries(t *testing.T) []remoteWorkloadCacheEntry {
	t.Helper()
	input := fixture.input
	input.Tree = fixture.oldTree
	entries, err := remoteWorkloadCacheEntries(
		"source/passed-workloads/v1/", fixture.workloads(), fixture.inputDigests(t, fixture.oldTree), input,
	)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func recordCompatiblePassEvidence(
	t *testing.T,
	ledger *gate.DurationLedgerStore,
	entries []remoteWorkloadCacheEntry,
	tree string,
	observedAt time.Time,
) {
	t.Helper()
	if err := recordRemoteWorkloadFingerprints(ledger, entries, tree, observedAt); err != nil {
		t.Fatal(err)
	}
	passed := make(map[string]gate.PlanGateExecution, len(entries))
	for _, entry := range entries {
		passed[entry.workloadID] = gate.PlanGateExecution{
			GateID: gate.GateID(entry.workloadID), Status: gate.ResultStatusPassed,
		}
	}
	if err := recordPassedWorkloadCacheProofs(ledger, entries, passed, observedAt); err != nil {
		t.Fatal(err)
	}
}

func (fixture compatiblePassFixture) codemapEntries(t *testing.T) []remoteWorkloadCacheEntry {
	t.Helper()
	commitFingerprintChange(t, fixture.repository, "docs/doc/codemap/AI_PROJECT_MAP.md", "generated map only\n")
	return fixture.currentEntries(t)
}

func (fixture compatiblePassFixture) currentEntries(t *testing.T) []remoteWorkloadCacheEntry {
	t.Helper()
	input := fixture.input
	input.Tree = coordinatorGitOutput(t, fixture.repository, "rev-parse", "HEAD^{tree}")
	entries, err := remoteWorkloadCacheEntries(
		"source/passed-workloads/v1/", fixture.workloads(), fixture.inputDigests(t, input.Tree), input,
	)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// inputDigests 与兼容提升保持同一父 workload 和 exact-child 摘要边界。
func (fixture compatiblePassFixture) inputDigests(t *testing.T, tree string) map[string]string {
	t.Helper()
	snapshot, err := loadRemoteGitTreeSnapshot(context.Background(), fixture.repository, tree)
	if err != nil {
		t.Fatal(err)
	}
	digests, err := snapshot.remoteWorkloadInputDigests(
		context.Background(), []gate.Workload{fixture.packageWorkload},
	)
	if err != nil {
		t.Fatal(err)
	}
	childDigests, err := snapshot.remoteExactGoTestInputDigests(
		context.Background(), []gate.Workload{fixture.packageWorkload},
	)
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(digests, childDigests)
	return digests
}

func (fixture compatiblePassFixture) promote(
	t *testing.T,
	entries []remoteWorkloadCacheEntry,
	forceRerun bool,
) (map[string]gate.PlanGateExecution, *coordinatorStore) {
	t.Helper()
	store := &coordinatorStore{objects: make(map[string][]byte)}
	promoted, err := promoteCompatiblePassedWorkloadCache(
		context.Background(), fixture.ledger,
		func() time.Time { return fixture.observedAt.Add(time.Minute) },
		fixture.repository, []gate.Workload{fixture.packageWorkload}, entries, forceRerun,
	)
	if err != nil {
		t.Fatal(err)
	}
	return promoted, store
}

func assertCompatiblePassPromotion(
	t *testing.T,
	fixture compatiblePassFixture,
	entries []remoteWorkloadCacheEntry,
	promoted map[string]gate.PlanGateExecution,
	store *coordinatorStore,
) {
	t.Helper()
	for _, workload := range fixture.workloads() {
		if execution, ok := promoted[workload.ID]; !ok || execution.Status != gate.ResultStatusPassed {
			t.Fatalf("codemap-only compatible PASS promotion = %#v", promoted)
		}
	}
	assertNoCompatiblePassMarkers(t, entries, store)
	assertCompatiblePassProofs(t, fixture.ledger, entries)
}

func assertNoCompatiblePassMarkers(t *testing.T, entries []remoteWorkloadCacheEntry, store *coordinatorStore) {
	t.Helper()
	for _, entry := range entries {
		if _, ok := store.objects[entry.key]; ok {
			t.Fatalf("compatible PASS %q published an unverifiable stable marker", entry.workloadID)
		}
	}
	if len(store.uploads) != 0 || len(store.uploadBatches) != 0 {
		t.Fatalf("compatible PASS reuse uploaded objects=%v batches=%v", store.uploads, store.uploadBatches)
	}
}

func assertCompatiblePassProofs(
	t *testing.T,
	ledger *gate.DurationLedgerStore,
	entries []remoteWorkloadCacheEntry,
) {
	t.Helper()
	digests := make([]string, len(entries))
	for index, entry := range entries {
		digests[index] = entry.identityDigest
	}
	proofs, err := ledger.LookupWorkloadPassProofs(digests)
	if err != nil {
		t.Fatal(err)
	}
	for _, digest := range digests {
		if _, ok := proofs[digest]; !ok {
			t.Fatalf("promoted PASS proof %q was not recorded", digest)
		}
	}
}

func TestPromoteCompatiblePassedWorkloadCacheIgnoresCodemapDrift(t *testing.T) {
	fixture := newCompatiblePassFixture(t)
	entries := fixture.codemapEntries(t)
	promoted, store := fixture.promote(t, entries, false)
	assertCompatiblePassPromotion(t, fixture, entries, promoted, store)
}

func TestLookupExactPassedGoTestsPromotesCompatibleFingerprint(t *testing.T) {
	fixture := newCompatiblePassFixture(t)
	entries := fixture.codemapEntries(t)
	input := fixture.input
	input.Tree = coordinatorGitOutput(t, fixture.repository, "rev-parse", "HEAD^{tree}")
	input.LedgerStore = fixture.ledger
	store := &coordinatorStore{objects: make(map[string][]byte)}
	cached := make(map[string]gate.PlanGateExecution)
	lookup := remoteWorkloadCacheLookup{
		workerWorkloads: []gate.Workload{fixture.packageWorkload},
		resume:          remoteGoTestResumeSet{entries: entries[1:]},
	}

	err := lookupExactPassedGoTests(
		context.Background(), store, "source/passed-workloads/v1/",
		func() time.Time { return fixture.observedAt.Add(time.Minute) },
		input, lookup, cached,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution, ok := cached[fixture.exactWorkload.ID]; !ok || execution.Status != gate.ResultStatusPassed {
		t.Fatalf("compatible exact-test PASS was not promoted: %#v", cached)
	}
	second := make(map[string]gate.PlanGateExecution)
	if err := lookupExactPassedGoTests(
		context.Background(), store, "source/passed-workloads/v1/",
		func() time.Time { return fixture.observedAt.Add(2 * time.Minute) },
		input, lookup, second,
	); err != nil {
		t.Fatal(err)
	}
	if execution, ok := second[fixture.exactWorkload.ID]; !ok || execution.Status != gate.ResultStatusPassed {
		t.Fatalf("second compatible exact-test lookup missed: %#v", second)
	}
	assertNoCompatiblePassMarkers(t, entries[1:], store)
}

func TestPromoteCompatiblePassedWorkloadCacheRejectsProductionChange(t *testing.T) {
	fixture := newCompatiblePassFixture(t)
	fixture.codemapEntries(t)
	commitFingerprintChange(t, fixture.repository, "internal/a/a.go", "package a\n\nconst ProductionChanged = true\n")
	promoted, _ := fixture.promote(t, fixture.currentEntries(t), false)
	if len(promoted) != 0 {
		t.Fatalf("production change reused compatible PASS = %#v", promoted)
	}
}

func TestPromoteCompatiblePassedWorkloadCacheForceRerunMisses(t *testing.T) {
	fixture := newCompatiblePassFixture(t)
	promoted, _ := fixture.promote(t, fixture.codemapEntries(t), true)
	if len(promoted) != 0 {
		t.Fatalf("forced rerun reused compatible PASS = %#v", promoted)
	}
}
