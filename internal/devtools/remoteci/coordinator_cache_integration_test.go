package remoteci

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

func TestCoordinatorRunReusesAllPassedWorkloadsWithoutECI(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	firstRuntime := &coordinatorRuntime{}
	if _, err := newTestCoordinator(t, store, firstRuntime).Run(context.Background(), input); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	uploadCount := len(store.uploads)
	secondRuntime := &coordinatorRuntime{}
	result, err := newTestCoordinator(t, store, secondRuntime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("cached Run() error = %v", err)
	}
	if len(secondRuntime.creates) != 0 || len(result.DurationSamples) != 0 || len(store.uploads) != uploadCount {
		t.Fatalf("cached Run() creates=%d samples=%d uploads=%d want uploads=%d", len(secondRuntime.creates), len(result.DurationSamples), len(store.uploads), uploadCount)
	}
	if len(result.Shards) != 0 || len(result.CacheMissWorkloads) != 0 || len(result.ReusedWorkloads) == 0 {
		t.Fatalf("cached Run() result = %+v", result)
	}
}

func TestCoordinatorRunReusesSemanticPassAcrossProvenanceRefreshWithoutECI(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	if _, err := newTestCoordinator(t, store, &coordinatorRuntime{}).Run(context.Background(), input); err != nil {
		t.Fatalf("seed Run() error = %v", err)
	}
	entries := mustRemoteWorkloadCacheEntries(
		t,
		"baseline-artifacts/source-deltas/passed-workloads/v1/",
		repository,
		input,
	)
	store.mu.Lock()
	receiptKey := workloadCacheReceiptKey(t, entries[0], store.objects[entries[0].key])
	receipt := append([]byte(nil), store.objects[receiptKey]...)
	store.mu.Unlock()
	if !strings.Contains(string(receipt), "runner "+input.RunnerIdentityDigest) {
		t.Fatalf("actual PASS receipt is missing its runner provenance: %q", receipt)
	}
	input.GateBinarySHA256 = "sha256:" + strings.Repeat("a", 64)
	input.RuntimeSeedSHA256 = "sha256:" + strings.Repeat("b", 64)
	input.RunnerConfigDigest = "sha256:" + strings.Repeat("c", 64)
	input.PolicyDigest = "sha256:" + strings.Repeat("d", 64)
	input.RunnerIdentityDigest = "sha256:" + strings.Repeat("e", 64)
	input.OCIProjectCache = cloneBaselineOCIProjectCache(input.OCIProjectCache)
	input.OCIProjectCache.ContentManifestSHA256 = "sha256:" + strings.Repeat("f", 64)
	runtime := &coordinatorRuntime{}
	result, err := newTestCoordinator(t, store, runtime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("provenance refresh Run() error = %v", err)
	}
	if len(runtime.creates) != 0 || len(result.CacheMissWorkloads) != 0 || len(result.ReusedWorkloads) == 0 {
		t.Fatalf("provenance-only refresh created ECI=%d misses=%v reused=%v", len(runtime.creates), result.CacheMissWorkloads, result.ReusedWorkloads)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if string(store.objects[receiptKey]) != string(receipt) {
		t.Fatal("cache hit overwrote the original PASS provenance receipt")
	}
}

func TestCoordinatorRunQueriesSQLiteBeforeOSS(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	ledgerStore, err := gate.NewDurationLedgerStore(
		filepath.Join(t.TempDir(), "duration-ledger.sqlite"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerStore.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	input.LedgerStore = ledgerStore
	store := &countingWorkloadCacheStore{coordinatorStore: &coordinatorStore{}}
	if _, err := newTestCoordinator(t, store, &coordinatorRuntime{}).Run(
		context.Background(),
		input,
	); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	listCount := len(store.lists)
	downloadCount := len(store.downloads)
	secondRuntime := &coordinatorRuntime{}
	result, err := newTestCoordinator(t, store, secondRuntime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("cached Run() error = %v", err)
	}
	if len(store.lists) != listCount || len(store.downloads) != downloadCount {
		t.Fatalf(
			"SQLite replay accessed OSS: lists=%d->%d downloads=%d->%d",
			listCount,
			len(store.lists),
			downloadCount,
			len(store.downloads),
		)
	}
	if len(secondRuntime.creates) != 0 || len(result.CacheMissWorkloads) != 0 {
		t.Fatalf(
			"SQLite replay created ECI=%d cache misses=%v",
			len(secondRuntime.creates),
			result.CacheMissWorkloads,
		)
	}
}

func TestWorkloadCacheLookupExpandsGoTestsOnlyAfterParentMiss(t *testing.T) {
	_, input := remoteGoTestCacheRunFixture(t)
	_, catalog, _, err := buildRemotePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	lookup, err := prepareRemoteWorkloadCacheLookup(
		context.Background(),
		"passed/",
		now,
		input,
		catalog,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(lookup.workerWorkloads) != 1 ||
		len(lookup.inputDigests) != 1 ||
		len(lookup.cacheEntries) != 1 {
		t.Fatalf(
			"parent lookup expanded speculative children: workloads=%d digests=%d entries=%d",
			len(lookup.workerWorkloads),
			len(lookup.inputDigests),
			len(lookup.cacheEntries),
		)
	}
	parentID := lookup.workerWorkloads[0].ID
	parentHit := map[string]gate.PlanGateExecution{
		parentID: {
			GateID: gate.GateID(parentID),
			Status: gate.ResultStatusPassed,
		},
	}
	if err := prepareRemoteGoTestResumeLookup(
		context.Background(),
		"passed/",
		now,
		input,
		parentHit,
		&lookup,
	); err != nil {
		t.Fatal(err)
	}
	if len(lookup.resume.entries) != 0 {
		t.Fatalf("parent PASS expanded %d child fingerprints", len(lookup.resume.entries))
	}

	if err := prepareRemoteGoTestResumeLookup(
		context.Background(),
		"passed/",
		now,
		input,
		map[string]gate.PlanGateExecution{},
		&lookup,
	); err != nil {
		t.Fatal(err)
	}
	if len(lookup.resume.entries) != 2 {
		t.Fatalf("parent miss expanded %d child fingerprints, want 2", len(lookup.resume.entries))
	}
}

func TestWorkloadCacheProbeReusesCloudTestFingerprintFromAnotherWorktree(t *testing.T) {
	repository, input := remoteGoTestCacheRunFixture(t)
	store := &coordinatorStore{}
	if _, err := newTestCoordinator(
		t,
		store,
		&coordinatorRuntime{mutateReport: failGoTestCacheFixtureReport},
	).Run(context.Background(), input); !errors.Is(err, ErrGateFailed) {
		t.Fatalf("seed cloud test marker Run() error = %v", err)
	}

	secondWorktree := t.TempDir() + "/local-caller"
	runCoordinatorGit(t, "", "clone", "--quiet", "--no-hardlinks", repository, secondWorktree)
	input.RepositoryRoot = secondWorktree
	input.Inventory = gate.WorkloadInventory{GoTests: []gate.GoTestTarget{{
		Package: "./internal/a",
		Name:    "TestValue",
	}}}
	probe, err := NewWorkloadCacheProbe(
		"baseline-artifacts/source-deltas/passed-workloads/v1/",
		store,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := probe.Probe(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertSingleReusedGoTest(t, result, "./internal/a", "TestValue")
}

func TestCoordinatorRunScalesGoTestCacheAcrossConcurrentWorktrees(t *testing.T) {
	repository, input := remoteGoTestCacheRunFixture(t)
	store := &coordinatorStore{}
	seedFailedGoTestCache(t, store, input)
	completionRuntime := &coordinatorRuntime{mutateReport: passGoTestCacheFixtureReport}
	completed, err := newTestCoordinator(t, store, completionRuntime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("complete test cache Run() error = %v", err)
	}
	assertOnlyGoTestWasExecuted(t, completed, completionRuntime, "TestRetry")
	uploadCount := len(store.uploads)
	runtimes, err := runCachedGoTestWorktrees(t, repository, input, store, 8)
	if err != nil {
		t.Fatalf("concurrent cross-worktree cache runs: %v", err)
	}
	assertCoordinatorRuntimesDidNotCreate(t, runtimes)
	if len(store.uploads) != uploadCount {
		t.Fatalf("all-hit scale run uploads=%d, want unchanged %d", len(store.uploads), uploadCount)
	}
}

func TestCoordinatorRunPlansShardsAfterCacheLookup(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	if _, err := newTestCoordinator(t, store, &coordinatorRuntime{}).Run(context.Background(), input); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	entries := mustRemoteWorkloadCacheEntries(
		t,
		"baseline-artifacts/source-deltas/passed-workloads/v1/",
		repository,
		input,
	)
	misses := []int{0, len(entries) - 1}
	store.mu.Lock()
	for _, index := range misses {
		delete(store.objects, entries[index].key)
	}
	store.mu.Unlock()

	runtime := &coordinatorRuntime{}
	result, err := newTestCoordinator(t, store, runtime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("incremental Run() error = %v", err)
	}
	if len(result.CacheMissWorkloads) != 2 || len(runtime.creates) != 1 || len(result.Shards) != 1 {
		t.Fatalf(
			"cache misses=%v creates=%d shards=%d",
			result.CacheMissWorkloads,
			len(runtime.creates),
			len(result.Shards),
		)
	}
	if gateIDs := mustRemoteRequestGateIDs(t, runtime.creates[0].Args); len(gateIDs) != 2 {
		t.Fatalf("two lightweight misses were not packed into one shard: %v", runtime.creates[0].Args)
	}
}

func mustRemoteWorkloadCacheEntries(
	t *testing.T,
	prefix string,
	repository string,
	input RunInput,
) []remoteWorkloadCacheEntry {
	t.Helper()
	_, catalog, _, err := buildRemotePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	workloads := remoteShardableWorkloads(catalog)
	inputDigests, err := remoteWorkloadInputDigests(context.Background(), repository, input.Tree, workloads)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := remoteWorkloadCacheEntries(prefix, workloads, inputDigests, input)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func mustRemoteRequestGateIDs(t *testing.T, args []string) []string {
	t.Helper()
	for index, arg := range args {
		if arg != "--gates" {
			continue
		}
		if index+1 >= len(args) || strings.TrimSpace(args[index+1]) == "" {
			t.Fatalf("remote request has an empty --gates value: %v", args)
		}
		gateIDs := strings.Split(args[index+1], ",")
		if slices.Contains(gateIDs, "") {
			t.Fatalf("remote request has an invalid --gates value: %v", args)
		}
		return gateIDs
	}
	t.Fatalf("remote request is missing --gates: %v", args)
	return nil
}

func TestCoordinatorRunForceRerunBypassesPassedWorkloadCache(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	if _, err := newTestCoordinator(t, store, &coordinatorRuntime{}).Run(context.Background(), input); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	entries := mustRemoteWorkloadCacheEntries(t, "baseline-artifacts/source-deltas/passed-workloads/v1/", repository, input)
	store.mu.Lock()
	firstMarker := append([]byte(nil), store.objects[entries[0].key]...)
	firstReceiptKey := workloadCacheReceiptKey(t, entries[0], firstMarker)
	store.mu.Unlock()
	input.ForceRerun = true
	runtime := &coordinatorRuntime{}
	result, err := newTestCoordinator(t, store, runtime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("forced Run() error = %v", err)
	}
	if len(runtime.creates) == 0 || len(result.DurationSamples) == 0 {
		t.Fatalf("forced Run() creates=%d samples=%d", len(runtime.creates), len(result.DurationSamples))
	}
	if len(result.ReusedWorkloads) != 0 || len(result.CacheMissWorkloads) == 0 {
		t.Fatalf("forced Run() result = %+v", result)
	}
	for index, shard := range result.Shards {
		if shard.ContainerGroup == "" || len(shard.ExecutedWorkloads) == 0 {
			t.Fatalf("forced shard %d = %+v", index, shard)
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	secondMarker := store.objects[entries[0].key]
	secondReceiptKey := workloadCacheReceiptKey(t, entries[0], secondMarker)
	if firstReceiptKey == secondReceiptKey || string(firstMarker) == string(secondMarker) {
		t.Fatalf("force rerun did not switch PASS receipt: marker=%q receipt=%q", secondMarker, secondReceiptKey)
	}
	if _, ok := store.objects[firstReceiptKey]; !ok {
		t.Fatalf("force rerun removed previous immutable receipt %q", firstReceiptKey)
	}
	if _, ok := store.objects[secondReceiptKey]; !ok {
		t.Fatalf("force rerun did not publish receipt %q", secondReceiptKey)
	}
}

func workloadCacheReceiptKey(t *testing.T, entry remoteWorkloadCacheEntry, marker []byte) string {
	t.Helper()
	lines := strings.Split(string(marker), "\n")
	if len(lines) != 8 || !strings.HasPrefix(lines[6], "receipt ") {
		t.Fatalf("invalid PASS marker: %q", marker)
	}
	receiptDigest := strings.TrimPrefix(lines[6], "receipt ")
	if !validRemoteWorkloadCacheDigest(receiptDigest) {
		t.Fatalf("invalid PASS receipt digest: %q", receiptDigest)
	}
	return remoteWorkloadCacheReceiptKey(entry, receiptDigest)
}

func TestCoordinatorRunReusesUnaffectedFrontendWorkloadsAcrossTrees(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	if _, err := newTestCoordinator(t, store, &coordinatorRuntime{}).Run(context.Background(), input); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	writeCoordinatorFixture(t, repository, "fixture.txt", "unrelated backend change\n")
	runCoordinatorGit(t, repository, "add", "fixture.txt")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "unrelated")
	updateCoordinatorInputTarget(t, repository, &input)
	runtime := &coordinatorRuntime{}
	result, err := newTestCoordinator(t, store, runtime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("incremental Run() error = %v", err)
	}
	reused := make(map[gate.GateID]struct{})
	for _, id := range result.ReusedWorkloads {
		reused[id] = struct{}{}
	}
	for _, id := range []gate.GateID{
		gate.GateIDFrontendLint, gate.GateIDFrontendTest,
		gate.GateIDFrontendBuild, gate.GateIDFrontendEmbedVerify,
	} {
		if _, ok := reused[id]; !ok {
			t.Fatalf("frontend workload %q was not reused: %+v", id, result)
		}
		for _, request := range runtime.creates {
			if slices.Contains(mustRemoteRequestGateIDs(t, request.Args), string(id)) {
				t.Fatalf("ECI request unexpectedly reruns cached workload %q: %v", id, request.Args)
			}
		}
	}
}

func TestCoordinatorRunWarnsButCachesSlowPassedWorkload(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	firstRuntime := &coordinatorRuntime{mutateReport: func(report *gate.PlanExecutionReport) {
		for index := range report.Gates {
			if report.Gates[index].GateID == gate.GateIDWhitespaceCheck {
				report.Gates[index].CompletedAt = report.Gates[index].StartedAt.Add(gate.FullCITargetDuration + time.Millisecond)
			}
		}
	}}
	first := mustRunCoordinator(t, store, firstRuntime, input, "slow passing workload")
	if first.Status != gate.ResultStatusPassed || len(first.OptimizationWarnings) != 1 {
		t.Fatalf("slow passing result = %+v", first)
	}
	secondRuntime := &coordinatorRuntime{}
	second := mustRunCoordinator(t, store, secondRuntime, input, "slow passing workload retry")
	if len(secondRuntime.creates) != 0 || !slices.Contains(second.ReusedWorkloads, gate.GateIDWhitespaceCheck) {
		t.Fatalf("slow passing workload was rerun: creates=%d reused=%v", len(secondRuntime.creates), second.ReusedWorkloads)
	}
}

func TestCoordinatorRunSharesPassedGoTestsAcrossWorktrees(t *testing.T) {
	repository, input := remoteGoTestCacheRunFixture(t)
	store := &coordinatorStore{}
	seedFailedGoTestCache(t, store, input)

	secondRepository := t.TempDir() + "/second-worktree"
	runCoordinatorGit(t, "", "clone", "--quiet", "--no-hardlinks", repository, secondRepository)
	input.RepositoryRoot = secondRepository
	secondRuntime := &coordinatorRuntime{mutateReport: passGoTestCacheFixtureReport}
	second := mustRunCoordinator(t, store, secondRuntime, input, "cross-worktree retry")
	assertOnlyGoTestWasExecuted(t, second, secondRuntime, "TestRetry")
	if !containsGoTestWorkload(t, second.ReusedWorkloads, "TestValue") {
		t.Fatalf("cross-worktree retry did not reuse TestValue: %v", second.ReusedWorkloads)
	}
	assertAllHitGoTestCache(t, store, input)
}

func TestCoordinatorRunReusesUnchangedExactTestsAfterSiblingChange(t *testing.T) {
	repository, input := remoteGoTestCacheRunFixture(t)
	store := &coordinatorStore{}
	seedFailedGoTestCache(t, store, input)

	commitFingerprintChange(t, repository, "internal/a/a_test.go", `package a

import "testing"

func TestValue(t *testing.T) {}
func TestRetry(t *testing.T) { t.Helper() }
`)
	updateCoordinatorInputTarget(t, repository, &input)
	runtime := &coordinatorRuntime{mutateReport: passGoTestCacheFixtureReport}
	result := mustRunCoordinator(t, store, runtime, input, "sibling test change")

	assertOnlyGoTestWasExecuted(t, result, runtime, "TestRetry")
	if !containsGoTestWorkload(t, result.ReusedWorkloads, "TestValue") {
		t.Fatalf("sibling test change did not reuse TestValue: %v", result.ReusedWorkloads)
	}
}

func TestCoordinatorRunResumesTimedOutGoPackageFromMissingTests(t *testing.T) {
	_, input := remoteGoTestCacheRunFixture(t)
	store := &coordinatorStore{}
	firstRuntime := &coordinatorRuntime{mutateReport: timeoutGoTestCacheFixtureReport}
	first, err := newTestCoordinator(t, store, firstRuntime).Run(context.Background(), input)
	if !errors.Is(err, ErrGateFailed) || first.Status != gate.ResultStatusFailed {
		t.Fatalf("timed-out package result=%+v error=%v", first, err)
	}

	retryRuntime := &coordinatorRuntime{mutateReport: passGoTestCacheFixtureReport}
	retry := mustRunCoordinator(t, store, retryRuntime, input, "timed-out package retry")
	assertOnlyGoTestWasExecuted(t, retry, retryRuntime, "TestRetry")
	if !containsGoTestWorkload(t, retry.ReusedWorkloads, "TestValue") {
		t.Fatalf("timed-out package retry did not reuse TestValue: %v", retry.ReusedWorkloads)
	}
}

func TestCoordinatorRunDoesNotProjectAllPassedTestsAfterPackageFinalFailure(t *testing.T) {
	_, input := remoteGoTestCacheRunFixture(t)
	store := &coordinatorStore{}
	failedRuntime := &coordinatorRuntime{mutateReport: allPassedGoTestsButPackageFailsFixtureReport}
	if _, err := newTestCoordinator(t, store, failedRuntime).Run(context.Background(), input); !errors.Is(err, ErrGateFailed) {
		t.Fatalf("seed final package failure Run() error = %v", err)
	}
	retryRuntime := &coordinatorRuntime{}
	retry, err := newTestCoordinator(t, store, retryRuntime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("retry Run() error = %v", err)
	}
	assertOnlyGoPackageWasExecuted(t, retry, retryRuntime)
}

func TestCoordinatorRunBindsResultCatalogDigestToEffectiveProjection(t *testing.T) {
	_, input := remoteGoTestCacheRunFixture(t)
	ledgerStore, err := gate.NewDurationLedgerStore(
		filepath.Join(t.TempDir(), "duration-ledger.sqlite"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledgerStore.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	input.LedgerStore = ledgerStore
	store := &coordinatorStore{}
	seedFailedGoTestCache(t, store, input)
	coordinator := newTestCoordinator(t, store, &coordinatorRuntime{mutateReport: passGoTestCacheFixtureReport})
	coordinator.newID = func() (string, error) {
		return "job-fedcba9876543210fedcba98", nil
	}
	requestedCatalog, requestedDigest := remoteRequestedCatalogAndDigest(t, input)
	selection, err := coordinator.lookupPassedWorkloads(context.Background(), input, requestedCatalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	effectiveDigest := remoteCatalogDigest(t, selection.catalog)
	if effectiveDigest == requestedDigest {
		t.Fatal("test-level projection did not change the effective catalog")
	}
	result, err := coordinator.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.CatalogDigest != effectiveDigest {
		t.Fatalf("run catalog digest = %q, want effective %q", result.CatalogDigest, effectiveDigest)
	}
	assertEffectiveCatalogWasRecorded(t, ledgerStore, result, selection.catalog, effectiveDigest)
}

func remoteRequestedCatalogAndDigest(t *testing.T, input RunInput) (gate.WorkloadCatalog, string) {
	t.Helper()
	_, catalog, _, err := buildRemotePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	return catalog, remoteCatalogDigest(t, catalog)
}

func remoteCatalogDigest(t *testing.T, catalog gate.WorkloadCatalog) string {
	t.Helper()
	digest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func assertEffectiveCatalogWasRecorded(
	t *testing.T,
	ledgerStore *gate.DurationLedgerStore,
	result RunResult,
	catalog gate.WorkloadCatalog,
	effectiveDigest string,
) {
	t.Helper()
	recordedCatalog, err := ledgerStore.LoadWorkloadCatalogRecord(effectiveDigest)
	if err != nil {
		t.Fatal(err)
	}
	if len(recordedCatalog.Catalog.Workloads) != len(catalog.Workloads) {
		t.Fatalf("recorded effective catalog workloads = %d, want %d", len(recordedCatalog.Catalog.Workloads), len(catalog.Workloads))
	}
	recordedRun, err := ledgerStore.LoadRemoteCIRun(result.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if recordedRun.CatalogDigest != effectiveDigest {
		t.Fatalf("recorded catalog digest = %q, want %q", recordedRun.CatalogDigest, effectiveDigest)
	}
	if recordedRun.Status != gate.ResultStatusPassed {
		t.Fatalf("recorded status = %q, want passed", recordedRun.Status)
	}
}

func assertSingleReusedGoTest(
	t *testing.T,
	result WorkloadCacheProbeResult,
	packageTarget string,
	testName string,
) {
	t.Helper()
	if len(result.CacheMissWorkloads) != 0 || len(result.ReusedWorkloads) != 1 {
		t.Fatalf("cache probe result = %+v", result)
	}
	_, kind, target, targeted, err := gate.ParseWorkloadID(string(result.ReusedWorkloads[0]))
	if err != nil || !targeted || kind != gate.WorkloadTargetGoTest {
		t.Fatalf("reused workload = %q kind=%q targeted=%v error=%v", result.ReusedWorkloads[0], kind, targeted, err)
	}
	testTarget, err := gate.ParseGoTestTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if testTarget.Package != packageTarget || testTarget.Name != testName {
		t.Fatalf("reused exact test target = %+v", testTarget)
	}
}

func seedFailedGoTestCache(t *testing.T, store *coordinatorStore, input RunInput) RunResult {
	t.Helper()
	runtime := &coordinatorRuntime{mutateReport: failGoTestCacheFixtureReport}
	result, err := newTestCoordinator(t, store, runtime).Run(context.Background(), input)
	if !errors.Is(err, ErrGateFailed) || result.Status != gate.ResultStatusFailed {
		t.Fatalf("seed failed-test cache result=%+v error=%v", result, err)
	}
	return result
}

func runCachedGoTestWorktrees(
	t *testing.T,
	repository string,
	input RunInput,
	store *coordinatorStore,
	count int,
) ([]*coordinatorRuntime, error) {
	t.Helper()
	roots := make([]string, count)
	for index := range roots {
		roots[index] = fmt.Sprintf("%s/worktree-%02d", t.TempDir(), index)
		runCoordinatorGit(t, "", "clone", "--quiet", "--no-hardlinks", repository, roots[index])
	}
	runtimes := make([]*coordinatorRuntime, count)
	var runs errgroup.Group
	for index := range roots {
		runtimes[index] = &coordinatorRuntime{}
		runs.Go(func() error {
			runInput := input
			runInput.RepositoryRoot = roots[index]
			result, err := newTestCoordinator(t, store, runtimes[index]).Run(context.Background(), runInput)
			if err != nil {
				return err
			}
			if len(result.Shards) != 0 || len(result.CacheMissWorkloads) != 0 {
				return fmt.Errorf("worktree %d had cache misses: %+v", index, result.CacheMissWorkloads)
			}
			return nil
		})
	}
	return runtimes, runs.Wait()
}

func assertCoordinatorRuntimesDidNotCreate(t *testing.T, runtimes []*coordinatorRuntime) {
	t.Helper()
	for index, runtime := range runtimes {
		if len(runtime.creates) != 0 {
			t.Fatalf("worktree %d created %d ECI groups", index, len(runtime.creates))
		}
	}
}

func mustRunCoordinator(
	t *testing.T,
	store *coordinatorStore,
	runtime *coordinatorRuntime,
	input RunInput,
	label string,
) RunResult {
	t.Helper()
	result, err := newTestCoordinator(t, store, runtime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("%s Run() error = %v", label, err)
	}
	return result
}

func assertAllHitGoTestCache(t *testing.T, store *coordinatorStore, input RunInput) {
	t.Helper()
	thirdRuntime := &coordinatorRuntime{}
	third := mustRunCoordinator(t, store, thirdRuntime, input, "all-hit cross-worktree")
	if len(thirdRuntime.creates) != 0 || len(third.Shards) != 0 || len(third.CacheMissWorkloads) != 0 {
		t.Fatalf("all-hit cross-worktree Run() created ECI: result=%+v creates=%d", third, len(thirdRuntime.creates))
	}
	if !containsGoTestWorkload(t, third.ReusedWorkloads, "TestValue") ||
		!containsGoTestWorkload(t, third.ReusedWorkloads, "TestRetry") {
		t.Fatalf("all-hit cross-worktree Run() reused=%v", third.ReusedWorkloads)
	}
}

func TestCoordinatorRunGoTestCacheInvalidationAndForceRerunUseWholePackage(t *testing.T) {
	repository, input := remoteGoTestCacheRunFixture(t)
	store := &coordinatorStore{}
	if _, err := newTestCoordinator(t, store, &coordinatorRuntime{mutateReport: failGoTestCacheFixtureReport}).Run(context.Background(), input); !errors.Is(err, ErrGateFailed) {
		t.Fatalf("seed failed-test cache Run() error = %v", err)
	}

	input.ForceRerun = true
	forcedRuntime := &coordinatorRuntime{}
	forced, err := newTestCoordinator(t, store, forcedRuntime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("forced Run() error = %v", err)
	}
	assertOnlyGoPackageWasExecuted(t, forced, forcedRuntime)

	input.ForceRerun = false
	commitFingerprintChange(t, repository, "internal/b/b.go", "package b\n\nconst Value = 9\n")
	updateCoordinatorInputTarget(t, repository, &input)
	changedRuntime := &coordinatorRuntime{}
	changed, err := newTestCoordinator(t, store, changedRuntime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("changed production input Run() error = %v", err)
	}
	assertOnlyGoPackageWasExecuted(t, changed, changedRuntime)
}

func TestCoordinatorRunSplitsOverBudgetGoPackageBeforePlanning(t *testing.T) {
	_, input := remoteGoTestCacheRunFixture(t)
	for index := range input.LedgerSnapshot.Ledger.Samples {
		input.LedgerSnapshot.Ledger.Samples[index].DurationMS = gate.FullCITargetDurationMS + 20_000
	}
	runtime := &coordinatorRuntime{mutateReport: passGoTestCacheFixtureReport}
	result, err := newTestCoordinator(t, &coordinatorStore{}, runtime).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(result.CacheMissWorkloads) != 2 || len(runtime.creates) != 2 {
		t.Fatalf("over-budget misses=%v creates=%d, want two test shards", result.CacheMissWorkloads, len(runtime.creates))
	}
	if !containsGoTestWorkload(t, result.CacheMissWorkloads, "TestValue") ||
		!containsGoTestWorkload(t, result.CacheMissWorkloads, "TestRetry") {
		t.Fatalf("over-budget misses=%v, want exact top-level tests", result.CacheMissWorkloads)
	}
}

func remoteGoTestCacheRunFixture(t *testing.T) (string, RunInput) {
	t.Helper()
	repository := newFingerprintRepository(t)
	base := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	baseTree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	commitFingerprintChange(t, repository, "docs/head.md", "head\n")
	commit := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	digest := "sha256:" + strings.Repeat("a", 64)
	sourceSpec := gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	}
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, sourceSpec)
	if err != nil {
		t.Fatal(err)
	}
	inventory := gate.WorkloadInventory{GoPackages: []string{"./internal/a"}}
	catalog, err := gate.BuildSelectedTestWorkloadCatalog(plan, inventory)
	if err != nil {
		t.Fatal(err)
	}
	ledger := gate.DurationLedger{Version: 1}
	for _, workload := range catalog.Workloads {
		ledger.Samples = append(ledger.Samples, gate.DurationSample{
			Bucket: gate.DurationBucket{
				WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
				Platform: "linux/amd64", Runner: digest, Toolchain: digest,
			},
			Succeeded: true, DurationMS: 5_000,
		})
	}
	return repository, RunInput{
		RepositoryRoot: repository, Commit: commit, Tree: tree, Base: base,
		RunnerBaseCommit: base, RunnerBaseTree: baseTree,
		Source: sourceSpec, Profile: gate.ProfileLocalFast, Entrypoint: gate.CIEntrypointManualCLI,
		MaxShards: 4, Platform: "linux/amd64", PolicyDigest: digest, ToolchainDigest: digest,
		LedgerSnapshot: gate.DurationLedgerSnapshot{Generation: 1, Ledger: ledger},
		Inventory:      inventory, SelectedTests: true,
		RunnerImage: "registry.example/runner@" + digest, RunnerIdentityDigest: digest,
		BaselineManifestDigest: "sha256:" + strings.Repeat("c", 64),
		RunnerConfigDigest:     "sha256:" + strings.Repeat("b", 64), GateBinarySHA256: digest, CandidateGateSourceSHA256: digest,
		CandidateGateToolchainSHA256: digest, RuntimeSeedSHA256: digest,
		OCIProjectCache: &BaselineOCIProjectCache{Image: "registry.example/runner@" + digest, ContentManifestSHA256: "sha256:" + strings.Repeat("c", 64), MainTree: baseTree, ToolchainDigest: digest, Platform: "linux/amd64", CachePath: OCIProjectGoBuildCachePath},
	}
}

func failGoTestCacheFixtureReport(report *gate.PlanExecutionReport) {
	report.SchemaVersion = 2
	for index := range report.Gates {
		_, kind, _, targeted, err := gate.ParseWorkloadID(string(report.Gates[index].GateID))
		if err != nil || !targeted || kind != gate.WorkloadTargetGoPackage {
			continue
		}
		log := []byte("TestRetry failed\n")
		report.Gates[index].Status = gate.ResultStatusFailed
		report.Gates[index].ExitCode = 1
		report.Gates[index].Log = log
		report.Gates[index].LogDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(log))
		report.Gates[index].TestTimings = []gate.GoTestTiming{
			{Name: "TestValue", Status: gate.GoTestStatusPass, DurationMS: 10},
			{Name: "TestRetry", Status: gate.GoTestStatusFail, DurationMS: 20},
		}
	}
}

func timeoutGoTestCacheFixtureReport(report *gate.PlanExecutionReport) {
	report.SchemaVersion = 2
	for index := range report.Gates {
		_, kind, _, targeted, err := gate.ParseWorkloadID(string(report.Gates[index].GateID))
		if err != nil || !targeted || kind != gate.WorkloadTargetGoPackage {
			continue
		}
		log := []byte("package test deadline exceeded after TestValue passed\n")
		report.Gates[index].Status = gate.ResultStatusFailed
		report.Gates[index].ExitCode = 1
		report.Gates[index].Log = log
		report.Gates[index].LogDigest = fmt.Sprintf("sha256:%x", sha256.Sum256(log))
		report.Gates[index].TestTimings = []gate.GoTestTiming{{
			Name: "TestValue", Status: gate.GoTestStatusPass, DurationMS: 10,
		}}
	}
}

func allPassedGoTestsButPackageFailsFixtureReport(report *gate.PlanExecutionReport) {
	failGoTestCacheFixtureReport(report)
	for index := range report.Gates {
		_, kind, _, targeted, err := gate.ParseWorkloadID(string(report.Gates[index].GateID))
		if err != nil || !targeted || kind != gate.WorkloadTargetGoPackage {
			continue
		}
		report.Gates[index].TestTimings[1].Status = gate.GoTestStatusPass
	}
}

func passGoTestCacheFixtureReport(report *gate.PlanExecutionReport) {
	// This synthetic runtime predates materializer-timing v4 records. Its stored
	// executions are intentionally migrated to explicit not_measured profiles.
	report.SchemaVersion = 2
	for index := range report.Gates {
		_, kind, target, targeted, err := gate.ParseWorkloadID(string(report.Gates[index].GateID))
		if err != nil || !targeted || kind != gate.WorkloadTargetGoTest {
			continue
		}
		testTarget, err := gate.ParseGoTestTarget(target)
		if err != nil {
			continue
		}
		report.Gates[index].TestTimings = []gate.GoTestTiming{{
			Name: testTarget.Name, Status: gate.GoTestStatusPass, DurationMS: 20,
		}}
	}
}

func assertOnlyGoTestWasExecuted(
	t *testing.T,
	result RunResult,
	runtime *coordinatorRuntime,
	name string,
) {
	t.Helper()
	if len(runtime.creates) != 1 || len(result.CacheMissWorkloads) != 1 {
		t.Fatalf("retry creates=%d misses=%v", len(runtime.creates), result.CacheMissWorkloads)
	}
	if !containsGoTestWorkload(t, result.CacheMissWorkloads, name) {
		t.Fatalf("retry misses=%v, want only %s", result.CacheMissWorkloads, name)
	}
}

func assertOnlyGoPackageWasExecuted(t *testing.T, result RunResult, runtime *coordinatorRuntime) {
	t.Helper()
	if len(runtime.creates) != 1 || len(result.CacheMissWorkloads) != 1 {
		t.Fatalf("whole-package creates=%d misses=%v", len(runtime.creates), result.CacheMissWorkloads)
	}
	_, kind, _, targeted, err := gate.ParseWorkloadID(string(result.CacheMissWorkloads[0]))
	if err != nil || !targeted || kind != gate.WorkloadTargetGoPackage {
		t.Fatalf("whole-package miss=%q kind=%q targeted=%v error=%v", result.CacheMissWorkloads[0], kind, targeted, err)
	}
}

func containsGoTestWorkload(t *testing.T, ids []gate.GateID, name string) bool {
	t.Helper()
	for _, id := range ids {
		_, kind, target, targeted, err := gate.ParseWorkloadID(string(id))
		if err != nil {
			t.Fatal(err)
		}
		if !targeted || kind != gate.WorkloadTargetGoTest {
			continue
		}
		testTarget, err := gate.ParseGoTestTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		if testTarget.Name == name {
			return true
		}
	}
	return false
}
