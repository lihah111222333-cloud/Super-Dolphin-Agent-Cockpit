package remoteci

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteWorkloadCacheIdentityIgnoresBatchShardAndRequesterFields(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	input := remoteWorkloadCacheInputFixture()
	first, err := remoteWorkloadCacheEntries(
		"source/passed-workloads/v1/",
		[]gate.Workload{workload},
		map[string]string{workload.ID: "sha256:" + strings.Repeat("a", 64)},
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	input.Commit = strings.Repeat("2", 40)
	input.Tree = strings.Repeat("3", 40)
	input.Profile = gate.ProfileRelease
	input.Entrypoint = gate.CIEntrypointRelease
	input.MaxShards = 63
	input.LedgerSnapshot.Generation = 99
	input.ForceRerun = true
	input.RequesterFingerprint = gate.RequesterFingerprint("sha256:" + strings.Repeat("f", 64))
	second, err := remoteWorkloadCacheEntries(
		"source/passed-workloads/v1/",
		[]gate.Workload{workload},
		map[string]string{workload.ID: "sha256:" + strings.Repeat("a", 64)},
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	if first[0].key != second[0].key {
		t.Fatalf("batch-only changes altered cache key: %q != %q", first[0].key, second[0].key)
	}
}

func TestRemoteWorkloadCacheIdentitySeparatesPlatforms(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	amd64 := remoteWorkloadCacheEntryFixture(t, workload, remoteWorkloadCacheInputFixture())
	armInput := remoteWorkloadCacheInputFixture()
	armInput.Platform = "linux/arm64"
	arm64 := remoteWorkloadCacheEntryFixture(t, workload, armInput)
	if amd64.environmentDigest == arm64.environmentDigest || amd64.key == arm64.key {
		t.Fatalf("platforms shared cache identity: amd64=%q arm64=%q", amd64.key, arm64.key)
	}
}

func TestRemoteWorkloadCacheIdentityIgnoresSourceOnlyBaselineRefresh(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	beforeInput := remoteWorkloadCacheInputFixture()
	before := remoteWorkloadCacheEntryFixture(t, workload, beforeInput)
	afterInput := beforeInput
	afterInput.Commit = strings.Repeat("a", 40)
	afterInput.Tree = strings.Repeat("b", 40)
	afterInput.BaselineManifestDigest = "sha256:" + strings.Repeat("c", 64)
	after := remoteWorkloadCacheEntryFixture(t, workload, afterInput)
	if before.key != after.key {
		t.Fatalf("source-only baseline refresh changed cache key: %q != %q", before.key, after.key)
	}
}

func TestRemoteWorkloadCacheIdentityIgnoresCoordinatorOnlyBaselineFields(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	beforeInput := remoteWorkloadCacheInputFixture()
	before := remoteWorkloadCacheEntryFixture(t, workload, beforeInput)
	afterInput := beforeInput
	afterInput.PolicyDigest = "sha256:" + strings.Repeat("a", 64)
	afterInput.RunnerConfigDigest = "sha256:" + strings.Repeat("b", 64)
	afterInput.GateBinarySHA256 = "sha256:" + strings.Repeat("c", 64)
	afterInput.RuntimeSeedSHA256 = "sha256:" + strings.Repeat("d", 64)
	after := remoteWorkloadCacheEntryFixture(t, workload, afterInput)
	if before.key != after.key {
		t.Fatalf("coordinator-only baseline refresh changed stable cache key: %q != %q", before.key, after.key)
	}
}

func TestRemoteWorkloadCacheIdentityIgnoresAnchorProvenance(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	beforeInput := remoteWorkloadCacheInputFixture()
	before := remoteWorkloadCacheEntryFixture(t, workload, beforeInput)
	afterInput := beforeInput
	afterInput.OCIProjectCache = cloneBaselineOCIProjectCache(afterInput.OCIProjectCache)
	afterInput.OCIProjectCache.ContentManifestSHA256 = "sha256:" + strings.Repeat("a", 64)
	after := remoteWorkloadCacheEntryFixture(t, workload, afterInput)
	if before.key != after.key {
		t.Fatalf("anchor provenance changed semantic cache key: %q != %q", before.key, after.key)
	}
}

func TestRemoteWorkloadCacheIdentityIgnoresExecutionProvenanceButSeparatesToolchain(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	baseInput := remoteWorkloadCacheInputFixture()
	base := remoteWorkloadCacheEntryFixture(t, workload, baseInput)
	runnerInput := baseInput
	runnerInput.RunnerImage = "registry.example/runtime@sha256:" + strings.Repeat("a", 64)
	runner := remoteWorkloadCacheEntryFixture(t, workload, runnerInput)
	toolchainInput := baseInput
	toolchainInput.ToolchainDigest = "sha256:" + strings.Repeat("b", 64)
	toolchain := remoteWorkloadCacheEntryFixture(t, workload, toolchainInput)
	workerInput := baseInput
	workerInput.RunnerIdentityDigest = "sha256:" + strings.Repeat("c", 64)
	worker := remoteWorkloadCacheEntryFixture(t, workload, workerInput)
	if base.key != runner.key || base.key != worker.key {
		t.Fatalf("runner provenance changed semantic key: base=%q runner=%q worker=%q", base.key, runner.key, worker.key)
	}
	if base.key == toolchain.key {
		t.Fatalf("toolchain change reused semantic key: base=%q toolchain=%q", base.key, toolchain.key)
	}
	if base.receiptKey == runner.receiptKey || base.receiptKey == worker.receiptKey {
		t.Fatalf("execution provenance did not create an immutable receipt: base=%q runner=%q worker=%q", base.receiptKey, runner.receiptKey, worker.receiptKey)
	}
}

func TestUploadPassedWorkloadCacheCommitsMarkerAfterReceipt(t *testing.T) {
	entry := remoteWorkloadCacheEntryFixture(
		t,
		remoteWorkloadCacheWorkloadFixture(),
		remoteWorkloadCacheInputFixture(),
	)
	store := &coordinatorStore{}
	err := uploadPassedWorkloadCache(
		context.Background(),
		store,
		t.TempDir(),
		[]passedWorkloadCacheUpload{
			{workloadID: entry.workloadID, prefix: entry.prefix, key: entry.key, data: encodeRemoteWorkloadCacheMarker(entry), commit: true},
			{workloadID: entry.workloadID, prefix: entry.receiptPrefix, key: entry.receiptKey, data: encodeRemoteWorkloadCacheReceipt(entry)},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.uploads) != 2 || store.uploads[0] != entry.receiptKey || store.uploads[1] != entry.key {
		t.Fatalf("PASS publication order = %v, want receipt then marker", store.uploads)
	}
	cached, err := loadPassedWorkloadCache(
		context.Background(), store, time.Now, []remoteWorkloadCacheEntry{entry}, false,
	)
	if err != nil || len(cached) != 1 {
		t.Fatalf("published marker did not bind a readable receipt: cached=%#v error=%v", cached, err)
	}
}

func TestPassedWorkloadCacheExecutionReceiptIsFreshAndContentAddressed(t *testing.T) {
	entry := remoteWorkloadCacheEntryFixture(t, remoteWorkloadCacheWorkloadFixture(), remoteWorkloadCacheInputFixture())
	first, err := remoteWorkloadCacheEntryForExecution(entry)
	if err != nil {
		t.Fatal(err)
	}
	second, err := remoteWorkloadCacheEntryForExecution(entry)
	if err != nil {
		t.Fatal(err)
	}
	if first.key != second.key {
		t.Fatalf("execution receipt changed semantic PASS key: %q != %q", first.key, second.key)
	}
	if first.receiptKey == second.receiptKey || first.receiptNonce == second.receiptNonce {
		t.Fatalf("real executions shared receipt: first=%q second=%q", first.receiptKey, second.receiptKey)
	}
	if _, err := validateRemoteWorkloadCacheEntries([]remoteWorkloadCacheEntry{first}); err != nil {
		t.Fatalf("execution receipt entry is invalid: %v", err)
	}
}

func TestRemoteWorkloadCachePassMarkerIsSharedAcrossWorktrees(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	firstInput := remoteWorkloadCacheInputFixture()
	firstInput.RepositoryRoot = "/workspace/.worktrees/agent-a"
	first := remoteWorkloadCacheEntryFixture(t, workload, firstInput)
	secondInput := firstInput
	secondInput.RepositoryRoot = "/workspace/.worktrees/agent-b"
	second := remoteWorkloadCacheEntryFixture(t, workload, secondInput)
	if first.key != second.key {
		t.Fatalf("worktree paths changed cache identity: %q != %q", first.key, second.key)
	}
	store := &coordinatorStore{objects: map[string][]byte{
		first.key:        encodeRemoteWorkloadCacheMarker(first),
		first.receiptKey: encodeRemoteWorkloadCacheReceipt(first),
	}}
	cached, err := loadPassedWorkloadCache(
		context.Background(), store, time.Now, []remoteWorkloadCacheEntry{second}, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cached[workload.ID]; !ok {
		t.Fatalf("second worktree did not reuse first worktree PASS marker: %#v", cached)
	}
}

func TestLoadPassedWorkloadCachePromotesFallbackMarkersToWorkerKey(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	input := remoteWorkloadCacheInputFixture()
	stable := remoteWorkloadCacheEntryFixture(t, workload, input)
	fallbacks := map[string]func(string, []remoteWorkloadCacheEntry, RunInput) ([]remoteWorkloadCacheEntry, error){
		"legacy exact identity": remoteLegacyWorkloadCacheEntries,
	}
	for name, build := range fallbacks {
		t.Run(name, func(t *testing.T) {
			fallbackEntries, err := build(
				"source/passed-workloads/v1/", []remoteWorkloadCacheEntry{stable}, input,
			)
			if err != nil {
				t.Fatal(err)
			}
			fallback := fallbackEntries[0]
			store := &coordinatorStore{objects: map[string][]byte{
				fallback.key:        encodeRemoteWorkloadCacheMarker(fallback),
				fallback.receiptKey: encodeRemoteWorkloadCacheReceipt(fallback),
			}}
			cached, err := loadPassedWorkloadCacheWithLegacy(
				context.Background(), store, time.Now,
				[]remoteWorkloadCacheEntry{stable}, []remoteWorkloadCacheEntry{fallback}, false,
			)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := cached[workload.ID]; !ok {
				t.Fatalf("fallback PASS marker was not reused: %#v", cached)
			}
			if _, ok := store.objects[stable.key]; ok {
				t.Fatal("fallback reuse published a stable marker without an execution receipt")
			}
			second, err := loadPassedWorkloadCacheWithLegacy(
				context.Background(), store, time.Now,
				[]remoteWorkloadCacheEntry{stable}, []remoteWorkloadCacheEntry{fallback}, false,
			)
			if err != nil || len(second) != 1 {
				t.Fatalf("fallback PASS was not reusable after the first lookup: cached=%#v error=%v", second, err)
			}
		})
	}
}

func TestLegacyWorkloadCacheMigrationMissesWhenRunnerIdentityChanges(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	firstInput := remoteWorkloadCacheInputFixture()
	stable := remoteWorkloadCacheEntryFixture(t, workload, firstInput)
	legacyEntries, err := remoteLegacyWorkloadCacheEntries(
		"source/passed-workloads/v1/", []remoteWorkloadCacheEntry{stable}, firstInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	secondInput := firstInput
	secondInput.RunnerIdentityDigest = "sha256:" + strings.Repeat("e", 64)
	second := remoteWorkloadCacheEntryFixture(t, workload, secondInput)
	secondLegacyEntries, err := remoteLegacyWorkloadCacheEntries(
		"source/passed-workloads/v1/", []remoteWorkloadCacheEntry{second}, secondInput,
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &coordinatorStore{objects: map[string][]byte{
		legacyEntries[0].key:        encodeRemoteWorkloadCacheMarker(legacyEntries[0]),
		legacyEntries[0].receiptKey: encodeRemoteWorkloadCacheReceipt(legacyEntries[0]),
	}}
	cached, err := loadPassedWorkloadCacheWithLegacy(
		context.Background(), store, time.Now,
		[]remoteWorkloadCacheEntry{second}, secondLegacyEntries, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 0 {
		t.Fatalf("runner identity drift reused legacy PASS: %#v", cached)
	}
}

func TestLoadPassedWorkloadCacheValidatesMarkerContent(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	entry := remoteWorkloadCacheEntryFixture(t, workload, remoteWorkloadCacheInputFixture())
	store := &coordinatorStore{objects: map[string][]byte{
		entry.key:        encodeRemoteWorkloadCacheMarker(entry),
		entry.receiptKey: encodeRemoteWorkloadCacheReceipt(entry),
	}}
	observedAt := time.Date(2026, time.July, 28, 1, 2, 3, 0, time.UTC)
	cached, err := loadPassedWorkloadCache(
		context.Background(),
		store,
		func() time.Time { return observedAt },
		[]remoteWorkloadCacheEntry{entry},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution, ok := cached[workload.ID]; !ok || !execution.StartedAt.Equal(observedAt) {
		t.Fatalf("validated cache result = %#v", cached)
	}

	store.objects[entry.key] = []byte("corrupt")
	_, err = loadPassedWorkloadCache(
		context.Background(),
		store,
		time.Now,
		[]remoteWorkloadCacheEntry{entry},
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "marker shape is invalid") {
		t.Fatalf("corrupt marker error = %v", err)
	}

	store.objects[entry.key] = encodeRemoteWorkloadCacheMarker(entry)
	store.objects[entry.receiptKey] = []byte("corrupt")
	_, err = loadPassedWorkloadCache(
		context.Background(),
		store,
		time.Now,
		[]remoteWorkloadCacheEntry{entry},
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "receipt shape is invalid") {
		t.Fatalf("corrupt receipt error = %v", err)
	}

	delete(store.objects, entry.receiptKey)
	cached, err = loadPassedWorkloadCache(
		context.Background(),
		store,
		time.Now,
		[]remoteWorkloadCacheEntry{entry},
		false,
	)
	if err != nil || len(cached) != 0 {
		t.Fatalf("marker without receipt cached=%#v error=%v", cached, err)
	}
}

type countingWorkloadCacheStore struct {
	*coordinatorStore
	mu        sync.Mutex
	downloads []string
	lists     []string
}

func (store *countingWorkloadCacheStore) DownloadIfExists(
	ctx context.Context,
	key string,
	localPath string,
) (bool, error) {
	store.mu.Lock()
	store.downloads = append(store.downloads, key)
	store.mu.Unlock()
	return store.coordinatorStore.DownloadIfExists(ctx, key, localPath)
}

func (store *countingWorkloadCacheStore) List(ctx context.Context, prefix string) ([]string, error) {
	store.mu.Lock()
	store.lists = append(store.lists, prefix)
	store.mu.Unlock()
	return store.coordinatorStore.List(ctx, prefix)
}

func TestLoadPassedWorkloadCacheListsOnceAndDownloadsOnlyExistingMarkers(t *testing.T) {
	const entryCount = 4096
	base := remoteWorkloadCacheEntryFixture(
		t,
		remoteWorkloadCacheWorkloadFixture(),
		remoteWorkloadCacheInputFixture(),
	)
	entries := make([]remoteWorkloadCacheEntry, entryCount)
	for index := range entries {
		suffix := fmt.Sprintf("%064x", index+1)
		entry := base
		entry.workloadID = fmt.Sprintf("workload-%04d", index)
		entry.inputDigest = "sha256:" + suffix
		entry.identityDigest = remoteWorkloadCacheIdentityDigest(
			entry.environmentDigest,
			entry.executionDigest,
			entry.inputDigest,
		)
		entry.key = entry.prefix + strings.TrimPrefix(entry.identityDigest, "sha256:") + ".pass"
		entry.receiptKey = remoteWorkloadCacheReceiptKey(entry, remoteWorkloadCacheReceiptDigest(entry))
		entries[index] = entry
	}
	hitIndexes := []int{17, 2048, 4095}
	objects := map[string][]byte{base.prefix + "unrelated.tmp": []byte("ignored")}
	for _, index := range hitIndexes {
		objects[entries[index].key] = encodeRemoteWorkloadCacheMarker(entries[index])
		objects[entries[index].receiptKey] = encodeRemoteWorkloadCacheReceipt(entries[index])
	}
	store := &countingWorkloadCacheStore{
		coordinatorStore: &coordinatorStore{objects: objects},
	}
	cached, err := loadPassedWorkloadCache(context.Background(), store, time.Now, entries, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.lists) != 1 || store.lists[0] != base.prefix {
		t.Fatalf("cache lists = %v, want one listing of %q", store.lists, base.prefix)
	}
	if len(store.downloads) != 2*len(hitIndexes) {
		t.Fatalf("cache downloads = %d, want marker and receipt for %d listed hits", len(store.downloads), len(hitIndexes))
	}
	if len(cached) != len(hitIndexes) {
		t.Fatalf("cached workloads = %d, want %d", len(cached), len(hitIndexes))
	}
	for _, index := range hitIndexes {
		if _, ok := cached[entries[index].workloadID]; !ok {
			t.Fatalf("listed workload %q was not reused", entries[index].workloadID)
		}
	}
}

func newWorkloadCacheTestLedger(t *testing.T) *gate.DurationLedgerStore {
	t.Helper()
	ledger, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	return ledger
}

func recordPassedWorkloadCacheProof(
	t *testing.T,
	ledger *gate.DurationLedgerStore,
	entry remoteWorkloadCacheEntry,
	observedAt time.Time,
) {
	t.Helper()
	passed := map[string]gate.PlanGateExecution{
		entry.workloadID: {
			GateID: gate.GateID(entry.workloadID), Status: gate.ResultStatusPassed,
		},
	}
	if err := recordPassedWorkloadCacheProofs(ledger, []remoteWorkloadCacheEntry{entry}, passed, observedAt); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPassedWorkloadCacheSQLiteHitDoesNotAccessOSS(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	entry := remoteWorkloadCacheEntryFixture(t, workload, remoteWorkloadCacheInputFixture())
	ledger := newWorkloadCacheTestLedger(t)
	observedAt := time.Date(2026, time.July, 30, 1, 2, 3, 0, time.UTC)
	recordPassedWorkloadCacheProof(t, ledger, entry, observedAt)
	store := &countingWorkloadCacheStore{
		coordinatorStore: &coordinatorStore{objects: make(map[string][]byte)},
	}
	cached, err := loadPassedWorkloadCacheWithSQLite(
		context.Background(), store, ledger, func() time.Time { return observedAt },
		[]remoteWorkloadCacheEntry{entry}, nil, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution, ok := cached[workload.ID]; !ok || execution.Status != gate.ResultStatusPassed {
		t.Fatalf("SQLite cache result = %#v", cached)
	}
	if len(store.lists) != 0 || len(store.downloads) != 0 {
		t.Fatalf("SQLite hit accessed OSS: lists=%v downloads=%v", store.lists, store.downloads)
	}
}

func TestLoadPassedWorkloadCacheSQLiteReusesEquivalentWorkloadAlias(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	entry := remoteWorkloadCacheEntryFixture(t, workload, remoteWorkloadCacheInputFixture())
	ledger := newWorkloadCacheTestLedger(t)
	observedAt := time.Date(2026, time.July, 30, 1, 2, 3, 0, time.UTC)
	recordPassedWorkloadCacheProof(t, ledger, entry, observedAt)
	alias := entry
	alias.workloadID = entry.workloadID + "-release"
	store := &countingWorkloadCacheStore{
		coordinatorStore: &coordinatorStore{objects: make(map[string][]byte)},
	}
	cached, err := loadPassedWorkloadCacheWithSQLite(
		context.Background(), store, ledger, func() time.Time { return observedAt },
		[]remoteWorkloadCacheEntry{alias}, nil, false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if execution, ok := cached[alias.workloadID]; !ok || execution.Status != gate.ResultStatusPassed {
		t.Fatalf("SQLite alias cache result = %#v", cached)
	}
	if len(store.lists) != 0 || len(store.downloads) != 0 {
		t.Fatalf("SQLite alias hit accessed OSS: lists=%v downloads=%v", store.lists, store.downloads)
	}
}

func corruptWorkloadCacheProofInput(
	t *testing.T,
	ledger *gate.DurationLedgerStore,
	entry remoteWorkloadCacheEntry,
) {
	t.Helper()
	database, err := sql.Open("sqlite", ledger.AuthorityPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close corrupt proof fixture database: %v", err)
		}
	})
	if _, err := database.Exec(
		`UPDATE ci_workload_pass_proofs SET input_digest = ? WHERE identity_digest = ?`,
		"sha256:"+strings.Repeat("f", 64), entry.identityDigest,
	); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPassedWorkloadCacheSQLiteFailsClosedOnMismatchedProofWithoutOSS(t *testing.T) {
	workload := remoteWorkloadCacheWorkloadFixture()
	entry := remoteWorkloadCacheEntryFixture(t, workload, remoteWorkloadCacheInputFixture())
	ledger := newWorkloadCacheTestLedger(t)
	recordPassedWorkloadCacheProof(t, ledger, entry, time.Now().UTC())
	corruptWorkloadCacheProofInput(t, ledger, entry)
	store := &countingWorkloadCacheStore{coordinatorStore: &coordinatorStore{objects: map[string][]byte{
		entry.key: encodeRemoteWorkloadCacheMarker(entry),
	}}}
	_, err := loadPassedWorkloadCacheWithSQLite(
		context.Background(), store, ledger, time.Now, []remoteWorkloadCacheEntry{entry}, nil, false,
	)
	if err == nil || !strings.Contains(err.Error(), "conflicts with expected workload identity") {
		t.Fatalf("mismatched SQLite proof error = %v", err)
	}
	if len(store.lists) != 0 || len(store.downloads) != 0 {
		t.Fatalf("mismatched SQLite proof accessed OSS: lists=%v downloads=%v", store.lists, store.downloads)
	}
}

func TestGoPackagePassMarkerAcceptsPassedExecutionWithoutTimings(t *testing.T) {
	workload, err := gate.NewGoPackageWorkload(
		gate.GateIDBackendTestWithGuard,
		"./internal/example",
		1_000,
	)
	if err != nil {
		t.Fatal(err)
	}
	execution := gate.PlanGateExecution{
		Status:   gate.ResultStatusPassed,
		ExitCode: 0,
	}
	if !remoteExecutionCanPublishPassMarker(workload.ID, execution) {
		t.Fatal("passed Go package execution without timings did not publish a PASS marker")
	}
	execution.TestTimings = []gate.GoTestTiming{{
		Name: "TestSkipped", Status: gate.GoTestStatusSkip,
	}}
	if !remoteExecutionCanPublishPassMarker(workload.ID, execution) {
		t.Fatal("passed skip-only Go package execution did not publish a PASS marker")
	}
	execution.ExitCode = 1
	if remoteExecutionCanPublishPassMarker(workload.ID, execution) {
		t.Fatal("failed Go package execution published a PASS marker")
	}
}

func TestExactGoTestPassMarkerRequiresTargetPassTiming(t *testing.T) {
	workload := fingerprintGoTestWorkload(t, "TestValue", "./internal/example")
	execution := gate.PlanGateExecution{
		Status:   gate.ResultStatusPassed,
		ExitCode: 0,
	}
	if remoteExecutionCanPublishPassMarker(workload.ID, execution) {
		t.Fatal("exact Go test without target timing published a PASS marker")
	}
	execution.TestTimings = []gate.GoTestTiming{{
		Name: "TestValue", Status: gate.GoTestStatusSkip,
	}}
	if remoteExecutionCanPublishPassMarker(workload.ID, execution) {
		t.Fatal("skipped exact Go test published a PASS marker")
	}
	execution.TestTimings = append(execution.TestTimings, gate.GoTestTiming{
		Name: "TestOther", Status: gate.GoTestStatusPass, DurationMS: 1,
	})
	if remoteExecutionCanPublishPassMarker(workload.ID, execution) {
		t.Fatal("unrelated passing Go test published a PASS marker")
	}
	execution.TestTimings = append(execution.TestTimings, gate.GoTestTiming{
		Name: "TestValue", Status: gate.GoTestStatusPass, DurationMS: 1,
	})
	if !remoteExecutionCanPublishPassMarker(workload.ID, execution) {
		t.Fatal("passing exact Go test did not publish a PASS marker")
	}
}

func remoteWorkloadCacheEntryFixture(
	t *testing.T,
	workload gate.Workload,
	input RunInput,
) remoteWorkloadCacheEntry {
	t.Helper()
	entries, err := remoteWorkloadCacheEntries(
		"source/passed-workloads/v1/",
		[]gate.Workload{workload},
		map[string]string{workload.ID: "sha256:" + strings.Repeat("a", 64)},
		input,
	)
	if err != nil {
		t.Fatal(err)
	}
	return entries[0]
}

func remoteWorkloadCacheWorkloadFixture() gate.Workload {
	return gate.Workload{
		ID:                  string(gate.GateIDWhitespaceCheck),
		Kind:                gate.WorkloadKindGuard,
		CommandDigest:       strings.Repeat("1", 64),
		BootstrapEstimateMS: 1000,
		Shardable:           true,
	}
}

func remoteWorkloadCacheInputFixture() RunInput {
	return RunInput{
		Commit:                 strings.Repeat("0", 40),
		Tree:                   strings.Repeat("1", 40),
		Profile:                gate.ProfileLocalFast,
		Entrypoint:             gate.CIEntrypointGitPreCommit,
		MaxShards:              5,
		Platform:               "linux/amd64",
		PolicyDigest:           "sha256:" + strings.Repeat("2", 64),
		ToolchainDigest:        "sha256:" + strings.Repeat("3", 64),
		RunnerImage:            "registry.example/runtime@sha256:" + strings.Repeat("4", 64),
		RunnerConfigDigest:     "sha256:" + strings.Repeat("5", 64),
		GateBinarySHA256:       "sha256:" + strings.Repeat("6", 64),
		RuntimeSeedSHA256:      "sha256:" + strings.Repeat("8", 64),
		LedgerSnapshot:         gate.DurationLedgerSnapshot{Generation: 1},
		RunnerIdentityDigest:   "sha256:" + strings.Repeat("9", 64),
		BaselineManifestDigest: "sha256:" + strings.Repeat("8", 64),
		OCIProjectCache: &BaselineOCIProjectCache{
			Image: "registry.example/runtime@sha256:" + strings.Repeat("4", 64), ContentManifestSHA256: "sha256:" + strings.Repeat("a", 64),
			MainTree: strings.Repeat("1", 40), ToolchainDigest: "sha256:" + strings.Repeat("3", 64), Platform: "linux/amd64", CachePath: OCIProjectGoBuildCachePath,
		},
	}
}
