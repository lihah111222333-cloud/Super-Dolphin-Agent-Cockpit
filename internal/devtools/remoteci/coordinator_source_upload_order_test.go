package remoteci

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

// sourceUploadOrderStore records the externally visible asset boundary without
// changing the production ObjectStore contract.  Source assets become ready only
// after the underlying Create has completed, so a request can never race a
// partially uploaded bundle or manifest.
type sourceUploadOrderStore struct {
	*coordinatorStore
	mu                  sync.Mutex
	sourceBundleReady   bool
	sourceManifestReady bool
	requestKeys         map[string]struct{}
	violations          []string
}

func (store *sourceUploadOrderStore) Create(ctx context.Context, localPath string, key string) error {
	request := strings.HasSuffix(key, ".request.json")
	if request {
		store.mu.Lock()
		ready := store.sourceBundleReady && store.sourceManifestReady
		if !ready {
			store.violations = append(store.violations, "shard request upload started before source assets completed")
		}
		store.mu.Unlock()
		if !ready {
			return fmt.Errorf("source assets were not completely uploaded before request %q", key)
		}
	}
	if err := store.coordinatorStore.Create(ctx, localPath, key); err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	switch {
	case strings.HasSuffix(key, ".bundle"):
		store.sourceBundleReady = true
	case strings.HasSuffix(key, ".manifest.json"):
		store.sourceManifestReady = true
	case request:
		if store.requestKeys == nil {
			store.requestKeys = make(map[string]struct{})
		}
		store.requestKeys[key] = struct{}{}
	}
	return nil
}

func (store *sourceUploadOrderStore) sourceAssetsReady() bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.sourceBundleReady && store.sourceManifestReady
}

func (store *sourceUploadOrderStore) requestUploaded(key string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	_, ok := store.requestKeys[key]
	return ok
}

func (store *sourceUploadOrderStore) violationsSnapshot() []string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return slices.Clone(store.violations)
}

func (store *sourceUploadOrderStore) uploadContentsSnapshot() map[string][]byte {
	store.coordinatorStore.mu.Lock()
	defer store.coordinatorStore.mu.Unlock()
	contents := make(map[string][]byte, len(store.coordinatorStore.uploadContents))
	for key, data := range store.coordinatorStore.uploadContents {
		contents[key] = append([]byte(nil), data...)
	}
	return contents
}

type sourceUploadOrderRuntime struct {
	*coordinatorRuntime
	store      *sourceUploadOrderStore
	mu         sync.Mutex
	violations []string
}

func (runtime *sourceUploadOrderRuntime) CreateContainerGroup(ctx context.Context, request eci.CreateRequest) (eci.ContainerGroup, error) {
	requestKey := request.InitContainer.Environment["SUPER_DOLPHIN_REMOTE_REQUEST_KEY"]
	if !runtime.store.sourceAssetsReady() {
		runtime.recordViolation("CreateContainerGroup started before source assets completed")
	}
	if !runtime.store.requestUploaded(requestKey) {
		runtime.recordViolation(fmt.Sprintf("CreateContainerGroup started before shard request %q was uploaded", requestKey))
	}
	if violations := runtime.violationsSnapshot(); len(violations) != 0 {
		return eci.ContainerGroup{}, fmt.Errorf("remote CI asset ordering violation: %s", strings.Join(violations, "; "))
	}
	return runtime.coordinatorRuntime.CreateContainerGroup(ctx, request)
}

func (runtime *sourceUploadOrderRuntime) recordViolation(violation string) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.violations = append(runtime.violations, violation)
}

func (runtime *sourceUploadOrderRuntime) violationsSnapshot() []string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return slices.Clone(runtime.violations)
}

type sourceUploadOrderOutcome struct {
	result RunResult
	err    error
}

type sourceUploadOrderHarness struct {
	requestBarrier *coordinatorOverlapBarrier
	createBarrier  *coordinatorOverlapBarrier
	store          *sourceUploadOrderStore
	runtime        *sourceUploadOrderRuntime
}

func sourceUploadOrderPlan(t *testing.T) (RunInput, gate.ContainerShardSet) {
	t.Helper()
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	plannedSet := mustBuildRemoteExecutionShardSet(t, input)
	if len(plannedSet.Shards) <= 1 {
		t.Fatalf("planned shards=%d, want multiple LPT shards", len(plannedSet.Shards))
	}
	planningSnapshot, err := input.LedgerStore.LoadPlanning(remotePlanningContext(input))
	if err != nil {
		t.Fatalf("LoadPlanning() error = %v", err)
	}
	assertSourceUploadOrderPlanningGeneration(t, plannedSet, planningSnapshot.Generation)
	planningInput := input
	planningInput.LedgerSnapshot = planningSnapshot
	expectedSet := mustBuildRemoteExecutionShardSet(t, planningInput)
	assertSourceUploadOrderLPTPlan(t, plannedSet, expectedSet)
	return input, plannedSet
}

func assertSourceUploadOrderPlanningGeneration(t *testing.T, plannedSet gate.ContainerShardSet, generation uint64) {
	t.Helper()
	if plannedSet.LedgerGeneration != generation ||
		plannedSet.WorkloadPlan.LedgerGeneration != generation {
		t.Fatalf("planned ledger generation = set=%d plan=%d SQLite=%d", plannedSet.LedgerGeneration, plannedSet.WorkloadPlan.LedgerGeneration, generation)
	}
}

func assertSourceUploadOrderLPTPlan(t *testing.T, plannedSet, expectedSet gate.ContainerShardSet) {
	t.Helper()
	if plannedSet.WorkloadPlan.PlanDigest != expectedSet.WorkloadPlan.PlanDigest ||
		!reflect.DeepEqual(plannedSet.WorkloadPlan.Shards, expectedSet.WorkloadPlan.Shards) {
		t.Fatalf("coordinator shard plan does not match SQLite LPT plan: got=%s want=%s", plannedSet.WorkloadPlan.PlanDigest, expectedSet.WorkloadPlan.PlanDigest)
	}
}

func newSourceUploadOrderHarness(plannedSet gate.ContainerShardSet) sourceUploadOrderHarness {
	requestBarrier := newCoordinatorOverlapBarrier(len(plannedSet.Shards), 1)
	createBarrier := newCoordinatorOverlapBarrier(len(plannedSet.Shards), 1)
	store := &sourceUploadOrderStore{
		coordinatorStore: &coordinatorStore{uploadBarrier: requestBarrier},
	}
	runtime := &sourceUploadOrderRuntime{
		coordinatorRuntime: &coordinatorRuntime{createBarrier: createBarrier},
		store:              store,
	}
	return sourceUploadOrderHarness{
		requestBarrier: requestBarrier,
		createBarrier:  createBarrier,
		store:          store,
		runtime:        runtime,
	}
}

func startSourceUploadOrderRun(runs *errgroup.Group, coordinator *Coordinator, input RunInput) <-chan sourceUploadOrderOutcome {
	outcomes := make(chan sourceUploadOrderOutcome, 1)
	runs.Go(func() error {
		result, err := coordinator.Run(context.Background(), input)
		outcomes <- sourceUploadOrderOutcome{result: result, err: err}
		return nil
	})
	return outcomes
}

func awaitSourceUploadOrderRun(t *testing.T, runs *errgroup.Group, outcomes <-chan sourceUploadOrderOutcome) sourceUploadOrderOutcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		if err := runs.Wait(); err != nil {
			t.Fatalf("coordinator run group error = %v", err)
		}
		return outcome
	case <-time.After(5 * time.Second):
		t.Fatal("coordinator did not finish after releasing all shard barriers")
		return sourceUploadOrderOutcome{}
	}
}

func assertSourceUploadOrderRunPassed(t *testing.T, outcome sourceUploadOrderOutcome) {
	t.Helper()
	if outcome.err != nil {
		t.Fatalf("Run() error = %v", outcome.err)
	}
	if outcome.result.Status != gate.ResultStatusPassed || !outcome.result.CleanupComplete {
		t.Fatalf("Run() result = status=%s cleanup=%t", outcome.result.Status, outcome.result.CleanupComplete)
	}
}

func assertSourceAssetsReadyAtRequestBarrier(t *testing.T, store *sourceUploadOrderStore, input RunInput) {
	t.Helper()
	assertUploadedSourceBundleAndManifest(t, store, input)
	if got := len(store.uploadContentsSnapshot()); got != 2 {
		t.Fatalf("request barrier reached with %d completed uploads, want exactly bundle and manifest", got)
	}
	assertSourceUploadOrderNoViolations(t, store, "before shard requests")
}

func assertSourceUploadOrderNoViolations(t *testing.T, store *sourceUploadOrderStore, phase string) {
	t.Helper()
	if violations := store.violationsSnapshot(); len(violations) != 0 {
		t.Fatalf("source upload ordering violations %s: %v", phase, violations)
	}
}

func assertECIAdmissionOrder(t *testing.T, store *sourceUploadOrderStore, runtime *sourceUploadOrderRuntime) {
	t.Helper()
	assertSourceUploadOrderNoViolations(t, store, "before ECI admission")
	if violations := runtime.violationsSnapshot(); len(violations) != 0 {
		t.Fatalf("ECI admission ordering violations: %v", violations)
	}
}

// TestCoordinatorUploadsCompleteSourceAssetsBeforeAnyShardAdmission guards the
// strict phase boundary: bundle + manifest first, then all shard requests, then
// ECI groups.  The barriers also prove that SQLite/LPT planned every shard and
// that one job does not impose a CPU-sized or other artificial concurrency cap.
func TestCoordinatorUploadsCompleteSourceAssetsBeforeAnyShardAdmission(t *testing.T) {
	input, plannedSet := sourceUploadOrderPlan(t)
	harness := newSourceUploadOrderHarness(plannedSet)
	coordinator := newTestCoordinator(t, harness.store, harness.runtime)
	var runs errgroup.Group
	outcomes := startSourceUploadOrderRun(&runs, coordinator, input)
	defer harness.requestBarrier.unblock()
	defer harness.createBarrier.unblock()

	assertCoordinatorBarrierReached(t, harness.requestBarrier, "complete source asset uploads before shard request uploads")
	assertSourceAssetsReadyAtRequestBarrier(t, harness.store, input)

	harness.requestBarrier.unblock()
	assertCoordinatorBarrierReached(t, harness.createBarrier, "all planned shard CreateContainerGroup calls")
	assertECIAdmissionOrder(t, harness.store, harness.runtime)

	harness.createBarrier.unblock()
	outcome := awaitSourceUploadOrderRun(t, &runs, outcomes)
	assertSourceUploadOrderRunPassed(t, outcome)
	assertUploadedShardRequestsMatchLPTPlan(t, harness.store, harness.runtime, plannedSet)
}

type sourceUploadAssets struct {
	bundleKey    string
	manifestKey  string
	bundle       []byte
	manifestData []byte
}

func assertUploadedSourceBundleAndManifest(t *testing.T, store *sourceUploadOrderStore, input RunInput) {
	t.Helper()
	assets := collectSourceUploadAssets(t, store.uploadContentsSnapshot())
	assertSourceUploadAssetsPresent(t, assets)
	manifest := decodeSourceUploadManifest(t, assets.manifestData)
	assertSourceUploadManifestMatchesInput(t, assets, manifest, input.Tree)
}

func collectSourceUploadAssets(t *testing.T, contents map[string][]byte) sourceUploadAssets {
	t.Helper()
	var assets sourceUploadAssets
	for key, data := range contents {
		switch {
		case strings.HasSuffix(key, ".bundle"):
			if assets.bundleKey != "" {
				t.Fatalf("multiple source bundles uploaded: %q and %q", assets.bundleKey, key)
			}
			assets.bundleKey, assets.bundle = key, data
		case strings.HasSuffix(key, ".manifest.json"):
			if assets.manifestKey != "" {
				t.Fatalf("multiple source manifests uploaded: %q and %q", assets.manifestKey, key)
			}
			assets.manifestKey, assets.manifestData = key, data
		}
	}
	return assets
}

func assertSourceUploadAssetsPresent(t *testing.T, assets sourceUploadAssets) {
	t.Helper()
	if assets.bundleKey == "" || len(assets.bundle) == 0 || assets.manifestKey == "" || len(assets.manifestData) == 0 {
		t.Fatalf("source assets uploaded = bundle=%q (%d bytes), manifest=%q (%d bytes)", assets.bundleKey, len(assets.bundle), assets.manifestKey, len(assets.manifestData))
	}
}

func decodeSourceUploadManifest(t *testing.T, data []byte) SourceMaterializationManifest {
	t.Helper()
	var manifest SourceMaterializationManifest
	if err := gate.DecodeStrictJSON(data, &manifest); err != nil {
		t.Fatalf("strict source manifest decode = %v", err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatalf("uploaded source manifest validation = %v", err)
	}
	return manifest
}

func assertSourceUploadManifestMatchesInput(t *testing.T, assets sourceUploadAssets, manifest SourceMaterializationManifest, tree string) {
	t.Helper()
	if manifest.SourceTreeSHA != tree || manifest.Source.SourceTreeSHA != tree {
		t.Fatalf("source manifest tree = %q/%q, want %q", manifest.SourceTreeSHA, manifest.Source.SourceTreeSHA, tree)
	}
	bundleDigest := sha256.Sum256(assets.bundle)
	if got, want := manifest.BundleDigest, fmt.Sprintf("sha256:%x", bundleDigest); got != want {
		t.Fatalf("source manifest bundle digest = %q, want %q", got, want)
	}
	if !strings.Contains(assets.bundleKey, strings.TrimPrefix(manifest.BundleDigest, "sha256:")) {
		t.Fatalf("source bundle key %q is not bound to manifest digest %q", assets.bundleKey, manifest.BundleDigest)
	}
}

func assertUploadedShardRequestsMatchLPTPlan(t *testing.T, store *sourceUploadOrderStore, runtime *sourceUploadOrderRuntime, plannedSet gate.ContainerShardSet) {
	t.Helper()
	requests := decodeUploadedShardRequests(t, store.uploadContentsSnapshot(), len(plannedSet.Shards))
	assertUploadedShardRequestCounts(t, len(requests), len(runtime.creates), len(plannedSet.Shards))
	planned := plannedShardsByIdentity(plannedSet)
	assertShardCreatesMatchRequests(t, runtime.creates, requests, planned)
}

func decodeUploadedShardRequests(t *testing.T, contents map[string][]byte, capacity int) map[string]ShardRequest {
	t.Helper()
	requests := make(map[string]ShardRequest, capacity)
	for key, data := range contents {
		if !strings.HasSuffix(key, ".request.json") {
			continue
		}
		request, err := DecodeShardRequest(data)
		if err != nil {
			t.Fatalf("DecodeShardRequest(%q) = %v", key, err)
		}
		requests[key] = request
	}
	return requests
}

func assertUploadedShardRequestCounts(t *testing.T, requestCount, createCount, plannedCount int) {
	t.Helper()
	if requestCount != plannedCount || createCount != plannedCount {
		t.Fatalf("uploaded shard requests=%d ECI creates=%d planned shards=%d", requestCount, createCount, plannedCount)
	}
}

func plannedShardsByIdentity(plannedSet gate.ContainerShardSet) map[string]gate.ContainerShard {
	planned := make(map[string]gate.ContainerShard, len(plannedSet.Shards))
	for _, shard := range plannedSet.Shards {
		planned[shard.IdentityDigest] = shard
	}
	return planned
}

func assertShardCreatesMatchRequests(t *testing.T, creates []eci.CreateRequest, requests map[string]ShardRequest, planned map[string]gate.ContainerShard) {
	t.Helper()
	for _, create := range creates {
		key := create.InitContainer.Environment["SUPER_DOLPHIN_REMOTE_REQUEST_KEY"]
		request, ok := requests[key]
		if !ok {
			t.Fatalf("ECI create references missing uploaded request %q", key)
		}
		shard, ok := planned[request.ShardIdentity]
		if !ok {
			t.Fatalf("request %q shard identity %q is not in SQLite LPT plan", key, request.ShardIdentity)
		}
		if !slices.Equal(request.GateIDs, shard.GateIDs) || request.PlanDigest != shard.PlanDigest || request.SourceTreeSHA != shard.SourceTreeSHA {
			t.Fatalf("request %q drifted from planned shard: request=%+v shard=%+v", key, request, shard)
		}
	}
}
