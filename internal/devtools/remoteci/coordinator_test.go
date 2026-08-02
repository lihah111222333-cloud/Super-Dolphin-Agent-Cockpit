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
	"strconv"
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
	deletePrefixes []string
	objects        map[string][]byte
	uploadContents map[string][]byte
	uploadBarrier  *coordinatorOverlapBarrier
}

func (store *coordinatorStore) Create(ctx context.Context, localPath string, key string) error {
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
	if _, exists := store.objects[key]; exists {
		return fmt.Errorf("object %q already exists", key)
	}
	store.objects[key] = data
	if store.uploadContents == nil {
		store.uploadContents = make(map[string][]byte)
	}
	store.uploadContents[key] = append([]byte(nil), data...)
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
	initLogs      map[string]string
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
	creationTime := runtime.groupState.CreationTime
	if creationTime.IsZero() {
		creationTime = time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	}
	materializerStartedAt := creationTime.Add(time.Millisecond)
	if len(runtime.groupState.InitContainers) == 1 && runtime.groupState.InitContainers[0].Name == "materializer" && !runtime.groupState.InitContainers[0].CurrentState.StartTime.IsZero() {
		materializerStartedAt = runtime.groupState.InitContainers[0].CurrentState.StartTime
	}
	sourceStartedAt := materializerStartedAt.Add(time.Millisecond)
	sourceCompletedAt := sourceStartedAt.Add(time.Millisecond)
	compileStartedAt := sourceCompletedAt.Add(time.Millisecond)
	compileCompletedAt := compileStartedAt.Add(time.Millisecond)
	if runtime.initLog == "" {
		timing, timingErr := gate.EncodeShardMaterializationTimingRecord(gate.ShardMaterializationTiming{Measurement: gate.MaterializationMeasurementMeasured, ShardIdentity: request.InitContainer.Environment["SUPER_DOLPHIN_REMOTE_SHARD_IDENTITY"], Source: gate.MaterializationPhaseTiming{StartedAtUnixMS: sourceStartedAt.UnixMilli(), CompletedAtUnixMS: sourceCompletedAt.UnixMilli(), MaterializeMS: sourceCompletedAt.Sub(sourceStartedAt).Milliseconds()}})
		if timingErr != nil {
			return eci.ContainerGroup{}, timingErr
		}
		if runtime.initLogs == nil {
			runtime.initLogs = make(map[string]string)
		}
		runtime.initLogs[id] = timing + "\n" + shardCompileTimingRecordPrefix + "started_at_unix_ms=" + strconv.FormatInt(compileStartedAt.UnixMilli(), 10) + " completed_at_unix_ms=" + strconv.FormatInt(compileCompletedAt.UnixMilli(), 10) + " duration_ms=" + strconv.FormatInt(compileCompletedAt.Sub(compileStartedAt).Milliseconds(), 10) + " cache_metrics=/workspace/work/go-cache/shard-compile.metrics"
	}
	log, err := runtime.reportLog(request, compileCompletedAt.Add(time.Millisecond))
	if err != nil {
		return eci.ContainerGroup{}, err
	}
	if runtime.logs == nil {
		runtime.logs = make(map[string]string)
	}
	runtime.logs[id] = log
	return eci.ContainerGroup{ID: id, Name: request.ContainerGroupName}, nil
}

func (runtime *coordinatorRuntime) reportLog(request eci.CreateRequest, executionStartedAt time.Time) (string, error) {
	report, err := reportFromCreateRequest(request, executionStartedAt)
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
	if groupState.CreationTime.IsZero() {
		groupState.CreationTime = time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	}
	if len(groupState.InitContainers) == 0 {
		groupState.InitContainers = []eci.ContainerStatus{{Name: "materializer", CurrentState: eci.ContainerState{StartTime: groupState.CreationTime.Add(time.Millisecond)}}}
	}
	if status == "Succeeded" && groupState.SucceededTime.IsZero() {
		groupState.SucceededTime = groupState.CreationTime.Add(2 * time.Second)
	}
	if status != "Succeeded" && groupState.FailedTime.IsZero() {
		groupState.FailedTime = groupState.CreationTime.Add(2 * time.Second)
	}
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
		if runtime.initLog == "" {
			return runtime.initLogs[groupID], nil
		}
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
	assertRemoteDurationSampleCoverage(t, result, plannedSet)
	assertCoordinatorRunSideEffects(t, store, runtime, plannedSet)
}

func TestCoordinatorRunExecutesEveryPlannedWorkloadDespiteLegacyPassedWorkloadObject(t *testing.T) {
	repository, input := remoteRunFixture(t)
	store := &coordinatorStore{objects: map[string][]byte{
		"baseline-artifacts/source-deltas/passed-workloads/v1/legacy.pass": []byte("PASS"),
	}}
	runtime := &coordinatorRuntime{}
	coordinator := newTestCoordinator(t, store, runtime)
	input.RepositoryRoot = repository
	plannedSet := mustBuildRemoteExecutionShardSet(t, input)
	result, err := coordinator.Run(context.Background(), input)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != gate.ResultStatusPassed {
		t.Fatalf("Run() status = %s, want %s", result.Status, gate.ResultStatusPassed)
	}
	if len(runtime.creates) != len(plannedSet.Shards) {
		t.Fatalf("legacy passed object skipped ECI execution: creates=%d want=%d", len(runtime.creates), len(plannedSet.Shards))
	}
}

func mustBuildRemoteExecutionShardSet(t *testing.T, input RunInput) gate.ContainerShardSet {
	t.Helper()
	plan, catalog, _, err := buildRemotePlan(input)
	if err != nil {
		t.Fatalf("buildRemotePlan() error = %v", err)
	}
	set, err := buildRemoteExecutionShardSet(plan, catalog, input)
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
) {
	t.Helper()
	if len(runtime.creates) != len(plannedSet.Shards) || len(runtime.deletes) != len(plannedSet.Shards) {
		t.Fatalf("runtime creates=%d deletes=%d", len(runtime.creates), len(runtime.deletes))
	}
	temporary := coordinatorTemporaryUploads(store.uploads)
	wantTemporary := 2 + len(plannedSet.Shards)
	if len(temporary) != wantTemporary || len(store.deletes) != len(temporary) {
		t.Fatalf("store temporary=%v deletes=%v", temporary, store.deletes)
	}
	deleted := slices.Clone(store.deletes)
	sort.Strings(deleted)
	sort.Strings(temporary)
	if !slices.Equal(temporary, deleted) {
		t.Fatalf("temporary uploads=%v must exactly equal cleanup deletes=%v", temporary, store.deletes)
	}
	if len(store.deletePrefixes) != 1 ||
		!strings.HasPrefix(store.deletePrefixes[0], "baseline-artifacts/source-deltas/job-") {
		t.Fatalf("temporary object delete prefixes=%v", store.deletePrefixes)
	}
	assertRemoteSourceObjectPrefix(t, temporary, runtime.creates)
	for _, request := range runtime.creates {
		assertRemoteCreateRequestIdentity(t, request)
		assertRemoteCreateRequestVolumes(t, request)
	}
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

func TestCoordinatorRunRejectsMissingOrMismatchedImageCacheSnapshotID(t *testing.T) {
	for name, snapshotID := range map[string]string{
		"missing":  "",
		"mismatch": "snap-other-baseline",
	} {
		t.Run(name, func(t *testing.T) {
			repository, input := remoteRunFixture(t)
			input.RepositoryRoot = repository
			input.ImageCacheSnapshotID = snapshotID
			_, err := newTestCoordinator(t, &coordinatorStore{}, &coordinatorRuntime{}).Run(context.Background(), input)
			if err == nil || !strings.Contains(err.Error(), "ImageCacheSnapshotID") {
				t.Fatalf("Run() error = %v, want image snapshot binding rejection", err)
			}
		})
	}
}

func TestImageCacheSnapshotFieldRegistry(t *testing.T) {
	for name, value := range map[string]reflect.Type{
		"RunInput":          reflect.TypeFor[RunInput](),
		"CoordinatorConfig": reflect.TypeFor[CoordinatorConfig](),
		"ShardRequest":      reflect.TypeFor[ShardRequest](),
	} {
		t.Run(name, func(t *testing.T) {
			if _, found := value.FieldByName("ImageCacheID"); found {
				t.Fatalf("%s retains the audit-only ImageCacheID field", name)
			}
			if _, found := value.FieldByName("ImageCacheSnapshotID"); !found {
				t.Fatalf("%s is missing ImageCacheSnapshotID", name)
			}
		})
	}
	field, found := reflect.TypeFor[ShardRequest]().FieldByName("ImageCacheSnapshotID")
	if !found || field.Tag.Get("json") != "image_cache_snapshot_id" {
		t.Fatalf("ShardRequest snapshot field = %#v, want image_cache_snapshot_id", field)
	}
}

func TestShardRequestCandidateGateCompileIdentityFieldRegistry(t *testing.T) {
	typeOfRequest := reflect.TypeFor[ShardRequest]()
	for name, tag := range map[string]string{
		"CandidateGateSourceSHA256":    "candidate_gate_source_sha256",
		"CandidateGateToolchainSHA256": "candidate_gate_toolchain_sha256",
	} {
		field, found := typeOfRequest.FieldByName(name)
		if !found || field.Tag.Get("json") != tag {
			t.Fatalf("ShardRequest %s field = %#v, want json %q", name, field, tag)
		}
	}
}

func coordinatorTemporaryUploads(uploads []string) []string {
	return slices.Clone(uploads)
}

func assertRemoteCreateRequestIdentity(t *testing.T, request eci.CreateRequest) {
	t.Helper()
	if !reflect.DeepEqual(request.Command, remoteWorkerSupervisorCommand(gate.ExecutorWorkRoot+"/bin/super-dolphin-gate")) ||
		len(request.Args) < 2 || request.Args[0] != "worker" || request.Args[1] != "run-shard" ||
		!reflect.DeepEqual(request.Environment, remoteWorkerEnvironment(10*time.Minute)) ||
		request.InitContainer.Name != "materializer" {
		t.Fatalf("create request identity = %+v", request)
	}
	assertRemoteInitShardCandidateCompile(t, request)
	assertECIEnvironmentLengths(t, "worker", request.Environment)
	assertECIEnvironmentLengths(t, "materializer", request.InitContainer.Environment)
}

func assertRemoteInitShardCandidateCompile(t *testing.T, request eci.CreateRequest) {
	t.Helper()
	if !reflect.DeepEqual(request.InitContainer.Command, []string{"/bin/sh"}) ||
		!reflect.DeepEqual(request.InitContainer.Args, []string{"-c", remoteShardBootstrapSH}) {
		t.Fatalf("init shard command = %+v", request.InitContainer)
	}
	for _, fragment := range []string{
		`"$accepted_gate" _remote-materialize`,
		`cd /workspace/source`,
		`/opt/super-dolphin-gate/runtime/go/bin/go build -mod=mod -trimpath -buildvcs=false -o "$built_gate" ./cmd/super-dolphin-gate`,
		`test -x "$built_gate"`,
	} {
		if !strings.Contains(request.InitContainer.Args[1], fragment) {
			t.Fatalf("init shard candidate compile command missing %q: %q", fragment, request.InitContainer.Args[1])
		}
	}
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

func assertRemoteCreateRequestVolumes(t *testing.T, request eci.CreateRequest) {
	t.Helper()
	assertCoordinatorVolumeField(t, "expanded volume name", request.ExpandedVolume.Name, "expanded-data")
	assertCoordinatorVolumeMount(t, request.MainVolumeMounts[0], "/opt/super-dolphin-gate", "", true)
	assertCoordinatorVolumeMount(t, request.MainVolumeMounts[1], remoteXKBCompMountPath, remoteXKBCompSubPath, true)
	assertCoordinatorVolumeMount(t, request.MainVolumeMounts[2], remoteXKBDataMountPath, remoteXKBDataSubPath, true)
	if len(request.InitVolumeMounts) != 4 {
		t.Fatalf("init shard volume mounts=%+v, want expanded, source, work, and temp", request.InitVolumeMounts)
	}
	assertCoordinatorVolumeMount(t, request.InitVolumeMounts[0], "/opt/super-dolphin-gate", "", false)
	assertCoordinatorVolumeMount(t, request.InitVolumeMounts[1], gate.ExecutorSourcePath, "", false)
	assertCoordinatorVolumeMount(t, request.InitVolumeMounts[2], gate.ExecutorWorkRoot, "", false)
	assertCoordinatorVolumeMount(t, request.InitVolumeMounts[3], remoteWritableTempMountPath, "", false)
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

	coordinator.newID = func() (string, error) { return "job-0123456789abcdef0123456a", nil }
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
	if !result.CleanupComplete || len(runtime.deletes) != len(runtime.creates)-1 {
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
		InternalOSSEndpoint:  "oss-cn-shenzhen-internal.aliyuncs.com",
		WorkerRoleName:       "worker-role",
		ImageCacheSnapshotID: "snap-accepted-baseline",
		WorkerTimeout:        10 * time.Minute,
		PollInterval:         time.Millisecond, CleanupTimeout: time.Second,
		ResourcePolicy: testRemoteResourcePolicy(),
	}, store, runtime)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { now = now.Add(time.Millisecond); return now }
	coordinator.newID = func() (string, error) { return "job-0123456789abcdef01234567", nil }
	return coordinator
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
	ledgerStore, ledgerSnapshot := newRemoteRunLedgerAuthority(t, ledger)
	return repository, RunInput{
		AcceptedGeneration: 1,
		RepositoryRoot:     repository,
		Commit:             commit, Tree: tree, Base: base,
		RunnerBaseCommit: base, RunnerBaseTree: baseTree,
		Source: gate.SourceSpec{
			Kind: gate.SourceKindCommit, ObjectFormat: gate.GitObjectFormatSHA1,
			Commit: &gate.CommitSource{SHA: commit}, SourceTreeSHA: tree,
		},
		Profile: gate.ProfileLocalFast, Entrypoint: gate.CIEntrypointManualCLI,
		Platform:                     "linux/amd64",
		PolicyDigest:                 digest,
		ToolchainDigest:              digest,
		LedgerSnapshot:               ledgerSnapshot,
		LedgerStore:                  ledgerStore,
		RunnerImage:                  "registry.example/runner@" + digest,
		ImageCacheSnapshotID:         "snap-accepted-baseline",
		RunnerIdentityDigest:         digest,
		BaselineManifestDigest:       "sha256:" + strings.Repeat("c", 64),
		RunnerConfigDigest:           "sha256:" + strings.Repeat("b", 64),
		GateBinarySHA256:             digest,
		CandidateGateSourceSHA256:    digest,
		CandidateGateToolchainSHA256: digest,
		RuntimeSeedSHA256:            digest,
		OCIProjectCache:              &BaselineOCIProjectCache{Image: "registry.example/runner@" + digest, ContentManifestSHA256: "sha256:" + strings.Repeat("c", 64), MainTree: baseTree, ToolchainDigest: digest, Platform: "linux/amd64", CachePath: OCIProjectGoBuildCachePath},
	}
}

// newRemoteRunLedgerAuthority creates the same SQLite authority used by production
// and returns the snapshot read back from it, keeping test planning and persistence aligned.
func newRemoteRunLedgerAuthority(t *testing.T, ledger gate.DurationLedger) (*gate.DurationLedgerStore, gate.DurationLedgerSnapshot) {
	t.Helper()
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatalf("NewDurationLedgerStore() error = %v", err)
	}
	if _, err := store.CompareAndSwap(0, ledger); err != nil {
		t.Fatalf("initialize duration ledger SQLite authority: %v", err)
	}
	seedRemoteCITestAcceptedGeneration(t, store, 1)
	snapshot, err := store.Load()
	if err != nil {
		t.Fatalf("load duration ledger SQLite authority: %v", err)
	}
	if snapshot.Generation != 1 || !reflect.DeepEqual(snapshot.Ledger, ledger) {
		t.Fatalf("duration ledger SQLite snapshot = %#v, want initialized ledger", snapshot)
	}
	return store, snapshot
}

func reportFromCreateRequest(request eci.CreateRequest, executionStartedAt time.Time) (gate.PlanExecutionReport, error) {
	if len(request.Args) != 8 || request.Args[0] != "worker" || request.Args[1] != "run-shard" {
		return gate.PlanExecutionReport{}, fmt.Errorf("unexpected worker args: %v", request.Args)
	}
	gateIDs := strings.Split(request.Args[7], ",")
	report := gate.PlanExecutionReport{
		SchemaVersion: 6, Profile: gate.Profile(request.Args[3]), PlanDigest: request.Args[5],
	}
	emptyDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(nil))
	for _, id := range gateIDs {
		report.Gates = append(report.Gates, gate.PlanGateExecution{
			GateID: gate.GateID(id), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: executionStartedAt, CompletedAt: executionStartedAt.Add(time.Second), LogDigest: emptyDigest,
			ExecutionProfile: gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "miss", CacheMeasurement: "measured", StartupMS: 100, TestBodyMS: 900, TotalMS: 1_000},
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
