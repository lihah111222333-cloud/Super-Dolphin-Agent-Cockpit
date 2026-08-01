package remoteci

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

type coordinatorStore struct {
	mu             sync.Mutex
	uploads        []string
	deletes        []string
	uploadBatches  []int
	deletePrefixes []string
	objects        map[string][]byte
	uploadBarrier  *coordinatorOverlapBarrier
}

func (store *coordinatorStore) Upload(ctx context.Context, localPath string, key string) error {
	if info, err := os.Stat(localPath); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("upload source is not a regular file")
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if store.uploadBarrier != nil && strings.HasSuffix(key, ".request.json") {
		if err := store.uploadBarrier.wait(ctx, coordinatorJobIDFromObjectKey(key)); err != nil {
			return err
		}
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.uploads = append(store.uploads, key)
	if store.objects == nil {
		store.objects = make(map[string][]byte)
	}
	store.objects[key] = data
	return nil
}

func (store *coordinatorStore) UploadDirectory(
	ctx context.Context,
	localPath string,
	prefix string,
	_ int,
) error {
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.uploadBatches = append(store.uploadBatches, len(entries))
	store.mu.Unlock()
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("batch upload fixture contains a non-file entry")
		}
		if err := store.Upload(ctx, filepath.Join(localPath, entry.Name()), prefix+entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

func (store *coordinatorStore) DownloadIfExists(ctx context.Context, key string, localPath string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	store.mu.Lock()
	data, ok := store.objects[key]
	data = append([]byte(nil), data...)
	store.mu.Unlock()
	if !ok {
		return false, nil
	}
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func (store *coordinatorStore) List(_ context.Context, prefix string) ([]string, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var keys []string
	for key := range store.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (store *coordinatorStore) DeletePrefix(_ context.Context, prefix string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.deletePrefixes = append(store.deletePrefixes, prefix)
	for key := range store.objects {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		store.deletes = append(store.deletes, key)
		delete(store.objects, key)
	}
	return nil
}

type coordinatorRuntime struct {
	mu            sync.Mutex
	creates       []eci.CreateRequest
	deletes       []string
	logs          map[string]string
	failAt        int
	tamperLog     bool
	failReport    bool
	failureLog    string
	status        string
	initLog       string
	groupState    eci.ContainerGroup
	createBarrier *coordinatorOverlapBarrier
	deleteBarrier *coordinatorOverlapBarrier
	describes     [][]string
	mutateReport  func(*gate.PlanExecutionReport)
}

func (runtime *coordinatorRuntime) CreateContainerGroup(ctx context.Context, request eci.CreateRequest) (eci.ContainerGroup, error) {
	if runtime.createBarrier != nil {
		if err := runtime.createBarrier.wait(ctx, request.Tags["super-dolphin-job"]); err != nil {
			return eci.ContainerGroup{}, err
		}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.creates = append(runtime.creates, request)
	if runtime.failAt > 0 && len(runtime.creates) == runtime.failAt {
		return eci.ContainerGroup{}, fmt.Errorf("injected create failure")
	}
	id := fmt.Sprintf("eci-%d", len(runtime.creates))
	log, err := runtime.reportLog(request)
	if err != nil {
		return eci.ContainerGroup{}, err
	}
	if runtime.logs == nil {
		runtime.logs = make(map[string]string)
	}
	runtime.logs[id] = log
	return eci.ContainerGroup{ID: id, Name: request.ContainerGroupName}, nil
}

func (runtime *coordinatorRuntime) reportLog(request eci.CreateRequest) (string, error) {
	report, err := reportFromCreateRequest(request)
	if err != nil {
		return "", err
	}
	if runtime.failReport {
		forceFailedCoordinatorReport(&report, runtime.failureLog)
	}
	if runtime.mutateReport != nil {
		runtime.mutateReport(&report)
	}
	chunks, err := gate.EncodePlanExecutionReportChunks(report)
	if err != nil {
		return "", err
	}
	log := "worker prelude\n" + strings.Join(chunks, "\n") + "\n" + runtime.failureLog
	if runtime.tamperLog {
		log = "worker report unavailable\n"
	}
	return log, nil
}

func coordinatorJobIDFromObjectKey(key string) string {
	for part := range strings.SplitSeq(key, "/") {
		if strings.HasPrefix(part, "job-") {
			return part
		}
	}
	return ""
}

func (runtime *coordinatorRuntime) DescribeContainerGroups(ctx context.Context, ids ...string) ([]eci.ContainerGroup, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtime.mu.Lock()
	runtime.describes = append(runtime.describes, slices.Clone(ids))
	status := runtime.status
	if status == "" {
		status = "Succeeded"
	}
	groupState := runtime.groupState
	runtime.mu.Unlock()
	groups := make([]eci.ContainerGroup, len(ids))
	for index, id := range ids {
		groups[index] = groupState
		groups[index].ID = id
		groups[index].Name = "shard"
		groups[index].Status = status
	}
	return groups, nil
}

func (runtime *coordinatorRuntime) DescribeContainerLog(_ context.Context, groupID string, containerName string) (string, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if containerName == "materializer" {
		return runtime.initLog, nil
	}
	return runtime.logs[groupID], nil
}

func (runtime *coordinatorRuntime) DeleteContainerGroup(ctx context.Context, groupID string) error {
	if runtime.deleteBarrier != nil {
		if err := runtime.deleteBarrier.wait(ctx, groupID); err != nil {
			return err
		}
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.deletes = append(runtime.deletes, groupID)
	return nil
}

func TestCoordinatorRunCompletesAndCleansRemoteShards(t *testing.T) {
	repository, input := remoteRunFixture(t)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	input.RepositoryRoot = repository
	plannedSet := mustBuildRemoteExecutionShardSet(t, input)
	result, err := coordinator.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != gate.ResultStatusPassed || !result.CleanupComplete || len(result.Shards) != len(plannedSet.Shards) {
		t.Fatalf("Run() result = %+v", result)
	}
	if !validObjectDigest(result.CandidateCLIManifestSHA256) {
		t.Fatalf("Run() candidate CLI manifest digest = %q", result.CandidateCLIManifestSHA256)
	}
	assertRemoteDurationSampleCoverage(t, result, plannedSet)
	assertCoordinatorRunSideEffects(t, store, runtime, plannedSet, input)
}

func mustBuildRemoteExecutionShardSet(t *testing.T, input RunInput) gate.ContainerShardSet {
	t.Helper()
	plan, catalog, _, err := buildRemotePlan(input)
	if err != nil {
		t.Fatalf("buildRemotePlan() error = %v", err)
	}
	set, err := buildRemoteExecutionShardSet(plan, catalog, nil, input)
	if err != nil {
		t.Fatalf("buildRemoteExecutionShardSet() error = %v", err)
	}
	return set
}

func assertCoordinatorRunSideEffects(
	t *testing.T,
	store *coordinatorStore,
	runtime *coordinatorRuntime,
	plannedSet gate.ContainerShardSet,
	input RunInput,
) {
	t.Helper()
	if len(runtime.creates) != len(plannedSet.Shards) || len(runtime.deletes) != len(plannedSet.Shards) {
		t.Fatalf("runtime creates=%d deletes=%d", len(runtime.creates), len(runtime.deletes))
	}
	temporary, persistent := partitionCoordinatorUploads(store.uploads)
	if len(temporary) != 4+len(plannedSet.Shards) || len(store.deletes) != len(temporary) {
		t.Fatalf("store temporary=%v persistent=%v deletes=%v", temporary, persistent, store.deletes)
	}
	expectedCached := 0
	for _, shard := range plannedSet.Shards {
		expectedCached += len(shard.GateIDs)
	}
	if len(persistent) != expectedCached*2 {
		t.Fatalf("persistent workload cache uploads=%d want=%d", len(persistent), expectedCached*2)
	}
	assertCoordinatorCacheSideEffects(t, store, persistent, expectedCached)
	assertRemoteSourceObjectPrefix(t, temporary, runtime.creates)
	for _, request := range runtime.creates {
		assertRemoteCreateRequestIdentity(t, request, input)
		assertRemoteCreateRequestVolumes(t, request, input)
	}
}

func assertCoordinatorCacheSideEffects(
	t *testing.T,
	store *coordinatorStore,
	persistent []string,
	expectedCached int,
) {
	t.Helper()
	if !reflect.DeepEqual(store.uploadBatches, []int{expectedCached, expectedCached}) {
		t.Fatalf("persistent workload cache batches=%v want receipt and marker batches of %d", store.uploadBatches, expectedCached)
	}
	receipts, markers := assertPublishedCacheObjects(t, persistent)
	if receipts != expectedCached || markers != expectedCached {
		t.Fatalf("persistent workload cache receipts=%d markers=%d want=%d each", receipts, markers, expectedCached)
	}
	if len(store.deletePrefixes) != 1 ||
		!strings.HasPrefix(store.deletePrefixes[0], "baseline-artifacts/source-deltas/job-") {
		t.Fatalf("temporary object delete prefixes=%v", store.deletePrefixes)
	}
}

func assertPublishedCacheObjects(t *testing.T, persistent []string) (int, int) {
	t.Helper()
	receipts, markers := 0, 0
	markerPhase := false
	for _, key := range persistent {
		switch {
		case strings.Contains(key, "/receipts/") && strings.HasSuffix(key, ".receipt"):
			if markerPhase {
				t.Fatalf("receipt %q was uploaded after marker publication began", key)
			}
			receipts++
		case strings.HasSuffix(key, ".pass"):
			markerPhase = true
			markers++
		default:
			t.Fatalf("unexpected persistent workload cache object %q", key)
		}
	}
	return receipts, markers
}

func TestRemoteExecutionShardResourcesUsesLargestWorkloadClass(t *testing.T) {
	_, input := remoteRunFixture(t)
	catalog := gate.WorkloadCatalog{
		Version: 1,
		Workloads: []gate.Workload{
			{ID: string(gate.GateIDWhitespaceCheck), Kind: gate.WorkloadKindGuard, Shardable: true},
			{ID: string(gate.GateIDBackendTestWithGuard), Kind: gate.WorkloadKindGoTest, Shardable: true},
		},
	}
	shards := []gate.ContainerShard{{
		IdentityDigest: "sha256:" + strings.Repeat("a", 64),
		GateIDs: []gate.GateID{
			gate.GateIDWhitespaceCheck,
			gate.GateIDBackendTestWithGuard,
		},
	}}
	resources, err := remoteExecutionShardResources(testRemoteResourcePolicy(), nil, catalog, shards, input)
	if err != nil {
		t.Fatalf("remoteExecutionShardResources() error = %v", err)
	}
	if len(resources) != 1 || resources[0].CPU != 4 || resources[0].MemoryGiB != 16 {
		t.Fatalf("resources = %#v", resources)
	}
}

func updateCoordinatorInputTarget(t *testing.T, repository string, input *RunInput) {
	t.Helper()
	commit := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	input.Commit, input.Tree = commit, tree
	input.Source = gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	}
}

func partitionCoordinatorUploads(uploads []string) (temporary []string, persistent []string) {
	for _, key := range uploads {
		if strings.Contains(key, "/passed-workloads/") {
			persistent = append(persistent, key)
			continue
		}
		temporary = append(temporary, key)
	}
	return temporary, persistent
}

func assertRemoteCreateRequestIdentity(t *testing.T, request eci.CreateRequest, input RunInput) {
	t.Helper()
	if !reflect.DeepEqual(request.Command, remoteWorkerSupervisorCommand("/opt/super-dolphin-gate/bin/super-dolphin-gate")) ||
		len(request.Args) < 2 || request.Args[0] != "worker" || request.Args[1] != "run-shard" ||
		!reflect.DeepEqual(request.Environment, remoteWorkerEnvironment(10*time.Minute)) ||
		!reflect.DeepEqual(request.InitContainer.Command, []string{"/bin/sh"}) ||
		!reflect.DeepEqual(request.InitContainer.Args, []string{"-c", remoteCandidateGateBootstrapSH}) ||
		request.InitContainer.Environment["SSL_CERT_FILE"] != remoteDataCacheCAFile ||
		request.InitContainer.Environment[remoteBaselineManifestEnvironment] != input.AnchorManifest {
		t.Fatalf("create request identity = %+v", request)
	}
	assertCandidateGateEnvironment(t, request, input)
	assertECIEnvironmentLengths(t, "worker", request.Environment)
	assertECIEnvironmentLengths(t, "materializer", request.InitContainer.Environment)
}

func assertECIEnvironmentLengths(t *testing.T, container string, environment map[string]string) {
	t.Helper()
	const maxECIEnvironmentValueBytes = 256
	for key, value := range environment {
		if len(value) > maxECIEnvironmentValueBytes {
			t.Fatalf("%s environment %q length=%d exceeds ECI limit %d", container, key, len(value), maxECIEnvironmentValueBytes)
		}
	}
}

func assertRemoteCreateRequestVolumes(t *testing.T, request eci.CreateRequest, input RunInput) {
	t.Helper()
	assertCoordinatorVolumeField(t, "data cache bucket", request.DataCacheBucket, input.DataCacheBucket)
	assertCoordinatorVolumeField(t, "base volume path", request.BaseVolume.Path, input.DataCachePath)
	assertCoordinatorVolumeField(t, "expanded volume name", request.ExpandedVolume.Name, "expanded-data")
	assertCoordinatorVolumeMount(t, request.MainVolumeMounts[0], "/bootstrap", "", true)
	assertCoordinatorVolumeMount(t, request.MainVolumeMounts[1], "/opt/super-dolphin-gate", "", true)
	assertCoordinatorVolumeMount(t, request.MainVolumeMounts[2], remoteXKBCompMountPath, remoteXKBCompSubPath, true)
	assertCoordinatorVolumeMount(t, request.MainVolumeMounts[3], remoteXKBDataMountPath, remoteXKBDataSubPath, true)
	if request.InitVolumeMounts[1].Name != remoteCurrentGateVolumeName || request.InitVolumeMounts[1].MountPath != "/candidate-bootstrap" || !request.InitVolumeMounts[1].ReadOnly {
		t.Fatalf("candidate bootstrap mount = %+v, want read-only candidate artifact directory", request.InitVolumeMounts[1])
	}
}

func assertCoordinatorVolumeField(t *testing.T, name string, got string, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func assertCoordinatorVolumeMount(t *testing.T, mount eci.VolumeMount, path string, subPath string, readOnly bool) {
	t.Helper()
	if mount.MountPath != path {
		t.Fatalf("volume mount path = %q, want %q", mount.MountPath, path)
	}
	if mount.SubPath != subPath {
		t.Fatalf("volume mount subpath = %q, want %q", mount.SubPath, subPath)
	}
	if mount.ReadOnly != readOnly {
		t.Fatalf("volume mount readonly = %t, want %t", mount.ReadOnly, readOnly)
	}
}

func assertRemoteSourceObjectPrefix(t *testing.T, uploads []string, creates []eci.CreateRequest) {
	t.Helper()
	const prefix = "baseline-artifacts/source-deltas/job-0123456789abcdef01234567/"
	for _, key := range uploads {
		if !strings.HasPrefix(key, prefix) {
			t.Fatalf("uploaded object key = %q", key)
		}
	}
	for _, request := range creates {
		if !strings.HasPrefix(request.InitContainer.Environment["SUPER_DOLPHIN_REMOTE_REQUEST_KEY"], prefix) {
			t.Fatalf("remote request key = %q", request.InitContainer.Environment["SUPER_DOLPHIN_REMOTE_REQUEST_KEY"])
		}
	}
}

func assertRemoteDurationSampleCoverage(t *testing.T, result RunResult, plannedSet gate.ContainerShardSet) {
	t.Helper()
	if len(result.DurationSamples) != len(plannedSet.WorkloadPlan.Catalog.Workloads) {
		t.Fatalf("duration samples=%d workloads=%d", len(result.DurationSamples), len(plannedSet.WorkloadPlan.Catalog.Workloads))
	}
}

func TestCoordinatorRunMarksOnlyCanonicalPreCommitTreeAuthoritative(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	input.Commit = ""
	input.Source = gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{
			SHA: input.Tree, ParentCommitSHA: input.Base,
		},
		SourceTreeSHA: input.Tree,
	}
	input.Entrypoint = gate.CIEntrypointGitPreCommit
	coordinator := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{})
	result, err := coordinator.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Authoritative || result.Entrypoint != gate.CIEntrypointGitPreCommit {
		t.Fatalf("result = %#v", result)
	}

	input.Entrypoint = gate.CIEntrypointManualCLI
	result, err = coordinator.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run(manual) error = %v", err)
	}
	if result.Authoritative {
		t.Fatalf("manual result unexpectedly authoritative: %#v", result)
	}
}

func TestCoordinatorRunCleansCreatedStateAfterPartialCreateFailure(t *testing.T) {
	repository, input := remoteRunFixture(t)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{failAt: 2}
	coordinator := newTestCoordinator(t, store, runtime)
	input.RepositoryRoot = repository
	result, err := coordinator.Run(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "create remote CI shard") {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.CleanupComplete || len(runtime.deletes) != 1 {
		t.Fatalf("partial cleanup result=%+v deletes=%v", result, runtime.deletes)
	}
}

func TestCoordinatorRunRejectsMissingWorkerReport(t *testing.T) {
	repository, input := remoteRunFixture(t)
	store := &coordinatorStore{}
	exitCode := int64(137)
	runtime := &coordinatorRuntime{
		tamperLog: true,
		status:    "Failed",
		initLog:   "materialize exploded",
		groupState: eci.ContainerGroup{
			Containers: []eci.ContainerStatus{{
				Name: "worker",
				CurrentState: eci.ContainerState{
					State:    "Terminated",
					ExitCode: &exitCode,
					Reason:   "OOMKilled",
					Message:  "memory limit exceeded",
				},
			}},
			Events: []eci.ContainerGroupEvent{{
				Type:          "Warning",
				Reason:        "DeadlineExceeded",
				Message:       "worker exceeded active deadline",
				Count:         1,
				LastTimestamp: "2026-07-27T08:04:00Z",
			}, {
				Type:          "Warning",
				Reason:        "BackOff",
				Message:       "worker exited",
				Count:         2,
				LastTimestamp: "2026-07-27T08:03:00Z",
			}, {
				Type:          "Normal",
				Reason:        "Pulled",
				Message:       "image ready",
				Count:         1,
				LastTimestamp: "2026-07-27T08:02:00Z",
			}, {
				Type:          "Normal",
				Reason:        "Scheduled",
				Message:       "worker scheduled",
				Count:         1,
				LastTimestamp: "2026-07-27T08:01:00Z",
			}},
		},
	}
	coordinator := newTestCoordinator(t, store, runtime)
	input.RepositoryRoot = repository
	result, err := coordinator.Run(context.Background(), input)
	if err == nil || result.Status == gate.ResultStatusPassed || !result.CleanupComplete {
		t.Fatalf("Run() result=%+v error=%v", result, err)
	}
	for _, fragment := range []string{"status=Failed", "materialize exploded", "exit_code=137", "OOMKilled", "BackOff", "DeadlineExceeded", "index=", "estimated_duration_ms=", "gates="} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("Run() diagnostic error = %v, missing %q", err, fragment)
		}
	}
}

func TestCoordinatorRunReturnsFailedWorkerDiagnostic(t *testing.T) {
	repository, input := remoteRunFixture(t)
	const failureLog = "go: fixture package failed before execution\n"
	runtime := &coordinatorRuntime{failReport: true, failureLog: failureLog}
	coordinator := newTestCoordinator(t, &coordinatorStore{}, runtime)
	input.RepositoryRoot = repository
	result, err := coordinator.Run(context.Background(), input)
	if !errors.Is(err, ErrGateFailed) || !strings.Contains(err.Error(), strings.TrimSpace(failureLog)) {
		t.Fatalf("Run() result=%+v error=%v", result, err)
	}
	if result.Status != gate.ResultStatusFailed || !result.CleanupComplete {
		t.Fatalf("Run() result=%+v", result)
	}
}

func newTestCoordinator(t *testing.T, store ObjectStore, runtime Runtime) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(CoordinatorConfig{
		Bucket: "ci-bucket", SourcePrefix: "baseline-artifacts/source-deltas/",
		WorkloadCachePrefix: "baseline-artifacts/source-deltas/passed-workloads/v1/",
		InternalOSSEndpoint: "oss-cn-shenzhen-internal.aliyuncs.com",
		WorkerRoleName:      "worker-role", WorkerTimeout: 10 * time.Minute,
		PollInterval: time.Millisecond, CleanupTimeout: time.Second,
		ResourcePolicy:      testRemoteResourcePolicy(),
		CandidateCLIBuilder: testCandidateCLIBuilder(t),
	}, store, runtime)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.now = func() time.Time { return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC) }
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234567", nil }
	return coordinator
}

func testCandidateCLIBuilder(t *testing.T) CandidateCLIBuilder {
	t.Helper()
	return func(_ context.Context, _ RunInput, tempRoot string) (string, error) {
		path := filepath.Join(tempRoot, "candidate-cli")
		if err := os.WriteFile(path, []byte("candidate cli\n"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
}

func TestCoordinatorRunBuildsCandidateCLIOnceBeforeShardFanout(t *testing.T) {
	_, input := remoteRunFixture(t)
	input.MaxShards = 3
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	builds := 0
	coordinator.config.CandidateCLIBuilder = func(_ context.Context, _ RunInput, tempRoot string) (string, error) {
		builds++
		path := filepath.Join(tempRoot, "candidate-cli-three-shard")
		if err := os.WriteFile(path, []byte("candidate cli\n"), 0o700); err != nil {
			return "", err
		}
		return path, nil
	}
	if _, err := coordinator.Run(context.Background(), input); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if builds != 1 || len(runtime.creates) != 2 {
		t.Fatalf("candidate CLI builds = %d, shard creates = %d", builds, len(runtime.creates))
	}
	if len(store.uploads) < 4 || !strings.HasSuffix(store.uploads[2], ".candidate-cli") || !strings.HasSuffix(store.uploads[3], ".manifest.json") {
		t.Fatalf("candidate artifact upload order = %v", store.uploads)
	}
}

func TestBuildShardRequestsSharesOneCandidateCLIArtifactAcrossThreeShards(t *testing.T) {
	_, input := remoteRunFixture(t)
	tempRoot := t.TempDir()
	builds := 0
	builder := func(_ context.Context, _ RunInput, destination string) (string, error) {
		builds++
		path := filepath.Join(destination, "candidate-cli-three-shards")
		return path, os.WriteFile(path, []byte("candidate cli\n"), 0o700)
	}
	candidate, _, _, _, err := buildRemoteCandidateCLIArtifact(context.Background(), builder, input, "job-0123456789abcdef01234567", tempRoot, "baseline-artifacts/source-deltas/")
	if err != nil {
		t.Fatalf("buildRemoteCandidateCLIArtifact() error = %v", err)
	}
	assets, err := buildRemoteAssets(context.Background(), input, "job-0123456789abcdef01234567", tempRoot, "baseline-artifacts/source-deltas/")
	if err != nil {
		t.Fatalf("buildRemoteAssets() error = %v", err)
	}
	shards := make([]gate.ContainerShard, 3)
	for index := range shards {
		shards[index] = gate.ContainerShard{Index: uint8(index), IdentityDigest: input.RunnerIdentityDigest, Profile: input.Profile, PlanDigest: input.PolicyDigest, SourceTreeSHA: input.Tree, GateIDs: []gate.GateID{gate.GateID(fmt.Sprintf("test:shard:%d", index))}}
	}
	requests, _, err := buildShardRequestsWithCandidate("baseline-artifacts/source-deltas/", "job-0123456789abcdef01234567", shards, assets.artifact, assets.patchKey, assets.manifestKey, assets.manifestDigest, candidate, input)
	if err != nil {
		t.Fatalf("buildShardRequestsWithCandidate() error = %v", err)
	}
	if builds != 1 || len(requests) != 3 {
		t.Fatalf("candidate CLI builds = %d, shard requests = %d", builds, len(requests))
	}
	for _, request := range requests {
		if request.CandidateCLI != candidate {
			t.Fatalf("shard candidate CLI = %#v, want %#v", request.CandidateCLI, candidate)
		}
	}
}

func TestCoordinatorRunStopsBeforeShardFanoutWhenCandidateCLIBuilderFails(t *testing.T) {
	_, input := remoteRunFixture(t)
	store := &coordinatorStore{}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	coordinator.config.CandidateCLIBuilder = func(context.Context, RunInput, string) (string, error) {
		return "", errors.New("injected candidate CLI build failure")
	}
	if _, err := coordinator.Run(context.Background(), input); err == nil || !strings.Contains(err.Error(), "injected candidate CLI build failure") {
		t.Fatalf("Run() error = %v", err)
	}
	if len(runtime.creates) != 0 || len(store.uploads) != 0 {
		t.Fatalf("builder failure fanned out: creates=%d uploads=%v", len(runtime.creates), store.uploads)
	}
}

func testRemoteResourcePolicy() shardresource.Policy {
	return shardresource.Policy{
		Classes: []shardresource.Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "standard", VCPU: 4, MemoryGiB: 8},
			{ID: "memory", VCPU: 4, MemoryGiB: 16},
			{ID: "maximum", VCPU: 8, MemoryGiB: 32},
		},
		Bootstrap: shardresource.BootstrapClasses{
			Guard: "small", NodeTest: "standard", GoTest: "memory",
		},
		HeadroomPercent: 25, MinSamplesToDownsize: 5,
	}
}

func remoteRunFixture(t *testing.T) (string, RunInput) {
	t.Helper()
	repository := t.TempDir()
	runCoordinatorGit(t, repository, "init", "--quiet")
	runCoordinatorGit(t, repository, "config", "user.email", "remote-ci@example.invalid")
	runCoordinatorGit(t, repository, "config", "user.name", "Remote CI")
	writeCoordinatorFixture(t, repository, "fixture.txt", "base\n")
	writeCoordinatorFixture(t, repository, "frontend-app/package.json", "{}\n")
	runCoordinatorGit(t, repository, "add", "fixture.txt", "frontend-app/package.json")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "base")
	base := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	baseTree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	writeCoordinatorFixture(t, repository, "fixture.txt", "head\n")
	runCoordinatorGit(t, repository, "add", "fixture.txt")
	runCoordinatorGit(t, repository, "commit", "--quiet", "-m", "head")
	commit := coordinatorGitOutput(t, repository, "rev-parse", "HEAD")
	tree := coordinatorGitOutput(t, repository, "rev-parse", "HEAD^{tree}")
	digest := "sha256:" + strings.Repeat("a", 64)
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
		Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
	})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := gate.BuildExpandedWorkloadCatalog(plan, gate.DefaultWorkloadBootstrapPolicy(), gate.WorkloadInventory{})
	if err != nil {
		t.Fatal(err)
	}
	ledger := gate.DurationLedger{Version: 1}
	for _, workload := range catalog.Workloads {
		ledger.Samples = append(ledger.Samples, gate.DurationSample{
			Bucket: gate.DurationBucket{
				WorkloadID: workload.ID, CommandDigest: workload.CommandDigest,
				Platform: "linux/arm64", Runner: digest, Toolchain: digest,
			},
			Succeeded: true, DurationMS: 15_000,
		})
	}
	return repository, RunInput{
		RepositoryRoot: repository,
		Commit:         commit, Tree: tree, Base: base,
		RunnerBaseCommit: base, RunnerBaseTree: baseTree,
		Source: gate.SourceSpec{
			Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
			Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
		},
		Profile: gate.ProfileLocalFast, Entrypoint: gate.CIEntrypointManualCLI,
		MaxShards: 2, Platform: "linux/arm64",
		PolicyDigest:           digest,
		ToolchainDigest:        digest,
		LedgerSnapshot:         gate.DurationLedgerSnapshot{Generation: 1, Ledger: ledger},
		RunnerImage:            "registry.example/runner@" + digest,
		RunnerIdentityDigest:   digest,
		BaselineManifestDigest: "sha256:" + strings.Repeat("c", 64),
		AnchorGeneration:       1,
		AnchorManifest:         "sha256:" + strings.Repeat("c", 64),
		AnchorCommit:           base, AnchorTree: baseTree,
		RunnerConfigDigest:           "sha256:" + strings.Repeat("b", 64),
		GateBinarySHA256:             digest,
		CandidateGateSourceSHA256:    digest,
		CandidateGateToolchainSHA256: digest,
		RuntimeSeedSHA256:            digest,
		DataCacheBucket:              "super-dolphin-ci",
		DataCachePath:                "/super-dolphin/ci/base/generation-1",
	}
}

func reportFromCreateRequest(request eci.CreateRequest) (gate.PlanExecutionReport, error) {
	if len(request.Args) != 8 || request.Args[0] != "worker" || request.Args[1] != "run-shard" {
		return gate.PlanExecutionReport{}, fmt.Errorf("unexpected worker args: %v", request.Args)
	}
	gateIDs := strings.Split(request.Args[7], ",")
	report := gate.PlanExecutionReport{
		SchemaVersion: 1, Profile: gate.Profile(request.Args[3]), PlanDigest: request.Args[5],
	}
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	emptyDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(nil))
	for _, id := range gateIDs {
		report.Gates = append(report.Gates, gate.PlanGateExecution{
			GateID: gate.GateID(id), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: now, CompletedAt: now.Add(time.Second), LogDigest: emptyDigest,
		})
	}
	return report, nil
}

func forceFailedCoordinatorReport(report *gate.PlanExecutionReport, failureLog string) {
	log := []byte(failureLog)
	digest := sha256.Sum256(log)
	report.Gates[0].Status = gate.ResultStatusFailed
	report.Gates[0].ExitCode = 1
	report.Gates[0].Log = log
	report.Gates[0].LogDigest = fmt.Sprintf("sha256:%x", digest)
}

func runCoordinatorGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func coordinatorGitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = repository
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(output))
}

func writeCoordinatorFixture(t *testing.T, repository string, relative string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(repository, relative)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, relative), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
