package localci

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

const (
	schedulerDockerAcceptanceSwitch = "SUPER_DOLPHIN_LOCALCI_DOCKER_ACCEPTANCE"
	schedulerDockerAcceptanceTag    = "super-dolphin-scheduler-acceptance:fixture"
	schedulerAcceptanceLabel        = "super-dolphin.acceptance"
	schedulerAcceptanceValue        = "scheduler-capacity"
	schedulerJobLabel               = "super-dolphin.acceptance.job"
	timeoutCancellationValue        = "timeout-cancellation"
)

type schedulerDockerAcceptanceResult struct {
	workloadID string
	result     FreshContainerResult
	err        error
}

type schedulerDockerAcceptanceHarness struct {
	configuration freshContainerSmokeConfiguration
	runner        *FreshContainerRunner
	plan          gate.GatePlan
	kernel        *schedulerKernel
	results       chan schedulerDockerAcceptanceResult
	started       chan FreshContainerLifecycleEvent
	workers       errgroup.Group
}

type schedulerDockerInspect struct {
	ID         string `json:"Id"`
	HostConfig struct {
		NanoCPUs       int64  `json:"NanoCpus"`
		Memory         int64  `json:"Memory"`
		Init           *bool  `json:"Init"`
		NetworkMode    string `json:"NetworkMode"`
		ReadonlyRootfs bool   `json:"ReadonlyRootfs"`
	} `json:"HostConfig"`
	Mounts []struct {
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
}

func TestSchedulerRealDockerCapacityFIFOAcceptance(t *testing.T) {
	if os.Getenv(schedulerDockerAcceptanceSwitch) != "1" {
		t.Skip("real Docker scheduler acceptance is disabled")
	}
	harness := newSchedulerDockerAcceptanceHarness(t)
	firstWave := harness.startFirstWave(t)
	assertSchedulerAcceptanceContainers(t, firstWave)
	assertNoSchedulerAcceptanceContainer(t, "job-4")
	first := harness.releaseCapacityAndStartFourth(t)
	harness.finish(t)
	assertNoSchedulerAcceptanceContainers(t)
	t.Logf("max_running=3 fourth_initial_state=queued first_completed=%s fourth_started_fifo=true resources=4cpu/8GiB init=true network=none source_readonly=true all_removed=true", first)
}

func TestFreshContainerRealDockerTimeoutCancellationAcceptance(t *testing.T) {
	if os.Getenv(schedulerDockerAcceptanceSwitch) != "1" {
		t.Skip("real Docker timeout and cancellation acceptance is disabled")
	}
	configuration := buildSchedulerDockerAcceptanceFixture(t)
	runner, err := NewFreshContainerRunner(configuration.SeccompPath, configuration.TrustedSourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	assertSchedulerAcceptanceProfileDeadline(t, runner, configuration, gate.ProfilePush, 10*time.Minute)
	assertSchedulerAcceptanceProfileDeadline(t, runner, configuration, gate.ProfileRelease, 30*time.Minute)
	assertSchedulerAcceptanceTimeout(t, runner, configuration)
	assertSchedulerAcceptanceCancellation(t, runner, configuration)
	assertNoDockerContainersForAcceptanceValue(t, timeoutCancellationValue)
	t.Log("normal_deadline=10m release_deadline=30m timeout_killed_removed=true cancellation_killed_removed=true")
}

func newSchedulerDockerAcceptanceHarness(t *testing.T) *schedulerDockerAcceptanceHarness {
	t.Helper()
	configuration := buildSchedulerDockerAcceptanceFixture(t)
	runner, err := NewFreshContainerRunner(configuration.SeccompPath, configuration.TrustedSourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: configuration.SourceTreeSHA}, SourceTreeSHA: configuration.SourceTreeSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	kernel := newTestSchedulerKernel(t)
	for sequence := 1; sequence <= 4; sequence++ {
		id := workloadID(fmt.Sprintf("job-%d", sequence))
		mustEnqueue(t, kernel, workloadSpec{
			id: id, invocationID: invocationID(fmt.Sprintf("invocation-%d", sequence)),
			enqueueSeq: uint64(sequence), kind: workloadJob,
		})
	}
	return &schedulerDockerAcceptanceHarness{
		configuration: configuration, runner: runner, plan: plan, kernel: kernel,
		results: make(chan schedulerDockerAcceptanceResult, 4), started: make(chan FreshContainerLifecycleEvent, 4),
	}
}

func (harness *schedulerDockerAcceptanceHarness) startFirstWave(t *testing.T) []FreshContainerLifecycleEvent {
	t.Helper()
	reservations, err := harness.kernel.reserveRunnable()
	if err != nil {
		t.Fatal(err)
	}
	if len(reservations) != maxActiveWorkloads {
		t.Fatalf("initial reservations=%d want=%d", len(reservations), maxActiveWorkloads)
	}
	if harness.kernel.state("job-4") != stateQueued {
		t.Fatalf("fourth state=%s want=%s", harness.kernel.state("job-4"), stateQueued)
	}
	for _, reservation := range reservations {
		harness.startContainer(t, reservation.workloadID)
	}
	return waitSchedulerAcceptanceStarts(t, harness.started, 3)
}

func (harness *schedulerDockerAcceptanceHarness) releaseCapacityAndStartFourth(t *testing.T) string {
	t.Helper()
	first := waitSchedulerAcceptanceResult(t, harness.results)
	assertPassedSchedulerAcceptanceResult(t, first)
	if err := harness.kernel.complete(workloadID(first.workloadID), statePassed); err != nil {
		t.Fatal(err)
	}
	next, err := harness.kernel.reserveRunnable()
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].workloadID != "job-4" {
		t.Fatalf("post-completion reservation=%#v, want job-4", next)
	}
	harness.startContainer(t, next[0].workloadID)
	fourth := waitSchedulerAcceptanceStarts(t, harness.started, 1)
	if fourth[0].ContainerID == "" {
		t.Fatal("fourth FIFO workload did not create a container after capacity was released")
	}
	return first.workloadID
}

func (harness *schedulerDockerAcceptanceHarness) finish(t *testing.T) {
	t.Helper()
	for range 3 {
		completed := waitSchedulerAcceptanceResult(t, harness.results)
		assertPassedSchedulerAcceptanceResult(t, completed)
		if err := harness.kernel.complete(workloadID(completed.workloadID), statePassed); err != nil {
			t.Fatal(err)
		}
	}
	if err := harness.workers.Wait(); err != nil {
		t.Fatal(err)
	}
}

func (harness *schedulerDockerAcceptanceHarness) startContainer(t *testing.T, id workloadID) {
	t.Helper()
	startSchedulerAcceptanceContainer(
		t, &harness.workers, harness.runner, harness.configuration, harness.plan, id, harness.started, harness.results,
	)
}

func assertPassedSchedulerAcceptanceResult(t *testing.T, completed schedulerDockerAcceptanceResult) {
	t.Helper()
	if completed.err != nil {
		t.Fatalf("completed workload=%s error=%v", completed.workloadID, completed.err)
	}
	if completed.result.Status != gate.ResultStatusPassed || !completed.result.Container.Removed {
		t.Fatalf("completed workload=%s result=%#v", completed.workloadID, completed.result)
	}
}

func assertSchedulerAcceptanceProfileDeadline(
	t *testing.T,
	runner *FreshContainerRunner,
	configuration freshContainerSmokeConfiguration,
	profile gate.Profile,
	want time.Duration,
) {
	t.Helper()
	request := schedulerAcceptanceRequest(t, configuration, profile, "deadline-"+string(profile))
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("profile %s real Docker run: %v", profile, err)
	}
	if result.Status != gate.ResultStatusPassed || !result.Container.Removed {
		t.Fatalf("profile %s result=%#v", profile, result)
	}
	if got := result.Deadline.Sub(result.StartedAt); got != want {
		t.Fatalf("profile %s deadline=%s want=%s", profile, got, want)
	}
}

func assertSchedulerAcceptanceTimeout(t *testing.T, runner *FreshContainerRunner, configuration freshContainerSmokeConfiguration) {
	t.Helper()
	request := schedulerAcceptanceRequest(t, configuration, gate.ProfilePush, "timeout")
	request.Deadline = time.Now().UTC().Add(time.Second)
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil {
		t.Fatal("real Docker timeout returned nil error")
	}
	assertTerminatedSchedulerAcceptanceResult(t, "timeout", result, gate.ResultStatusTimeout)
}

func assertSchedulerAcceptanceCancellation(t *testing.T, runner *FreshContainerRunner, configuration freshContainerSmokeConfiguration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	request := schedulerAcceptanceRequest(t, configuration, gate.ProfilePush, "cancel")
	request.LifecycleHook = func(_ context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase == FreshContainerPhaseStarted {
			started <- struct{}{}
		}
		return nil
	}
	results := make(chan schedulerDockerAcceptanceResult, 1)
	group := errgroup.Group{}
	group.Go(func() error {
		result, err := runner.RunFreshContainer(ctx, request)
		results <- schedulerDockerAcceptanceResult{workloadID: "cancel", result: result, err: err}
		return nil
	})
	waitSchedulerAcceptanceSignal(t, started)
	cancel()
	completed := waitSchedulerAcceptanceResult(t, results)
	if completed.err == nil {
		t.Fatal("real Docker cancellation returned nil error")
	}
	assertTerminatedSchedulerAcceptanceResult(t, "cancellation", completed.result, gate.ResultStatusCancelled)
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func schedulerAcceptanceRequest(
	t *testing.T,
	configuration freshContainerSmokeConfiguration,
	profile gate.Profile,
	job string,
) FreshContainerRequest {
	t.Helper()
	plan, err := gate.BuildGatePlan(profile, gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: configuration.SourceTreeSHA}, SourceTreeSHA: configuration.SourceTreeSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(configuration.TrustedSourceRoot, job)
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	return FreshContainerRequest{
		Image: configuration.Image, ImageTruth: configuration.ImageTruth,
		SourceTreeSHA: configuration.SourceTreeSHA, SourceSnapshotDir: source,
		Profile: profile, Plan: plan, GateID: plan.Gates[0].ID,
		ContainerLabels: map[string]string{schedulerAcceptanceLabel: timeoutCancellationValue, schedulerJobLabel: job},
	}
}

func assertTerminatedSchedulerAcceptanceResult(
	t *testing.T,
	name string,
	result FreshContainerResult,
	want gate.ResultStatus,
) {
	t.Helper()
	if result.Status != want {
		t.Fatalf("%s status=%s want=%s", name, result.Status, want)
	}
	if !result.Killed || result.KillProofDigest == "" {
		t.Fatalf("%s kill evidence missing: %#v", name, result)
	}
	if !result.Container.Removed || result.RemovalProofDigest == "" {
		t.Fatalf("%s removal evidence missing: %#v", name, result)
	}
}

func waitSchedulerAcceptanceSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for real Docker started signal")
	}
}

func buildSchedulerDockerAcceptanceFixture(t *testing.T) freshContainerSmokeConfiguration {
	t.Helper()
	fixtureDirectory := canonicalSmokePath(t, "testdata/scheduler-docker-acceptance")
	request := BuildKitBuildRequest{
		SourceTreeSHA: strings.Repeat("c", 40), PolicyDigest: digest("8"), ImageSchemaVersion: imageInputSchemaVersion,
		ContextDigest: digest("1"), InputDigest: digest("3"), ToolchainDigest: digest("4"),
		DockerfileDigest: digest("2"), Platform: "linux/arm64",
	}
	args := []string{"buildx", "build", "--load", "--provenance=false", "--network=none", "--platform=" + request.Platform, "--file=" + filepath.Join(fixtureDirectory, "Dockerfile"), "--tag=" + schedulerDockerAcceptanceTag}
	for _, label := range sortedBuildxBindingLabels(request) {
		args = append(args, "--label="+label)
	}
	args = append(args, fixtureDirectory)
	if _, err := (execDockerRunner{}).Run(context.Background(), args...); err != nil {
		t.Fatalf("build scheduler Docker acceptance fixture: %v", err)
	}
	t.Cleanup(func() {
		_, _ = (execDockerRunner{}).Run(context.Background(), "image", "rm", "--force", schedulerDockerAcceptanceTag)
	})
	output, err := (execDockerRunner{}).Run(context.Background(), "image", "inspect", schedulerDockerAcceptanceTag)
	if err != nil {
		t.Fatal(err)
	}
	var document imageInspectDocument
	if err := decodeSingleInspect(output, &document); err != nil {
		t.Fatal(err)
	}
	root := canonicalSmokePath(t, t.TempDir())
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return freshContainerSmokeConfiguration{
		Image: smokeImageIdentity(t, document),
		ImageTruth: FreshContainerImageTruth{
			PolicyDigest: request.PolicyDigest, BuildSourceTreeSHA: request.SourceTreeSHA,
			InputDigest: request.InputDigest, ToolchainDigest: request.ToolchainDigest, SchemaVersion: request.ImageSchemaVersion,
		},
		SourceTreeSHA: strings.Repeat("d", 40),
		SeccompPath:   canonicalSmokePath(t, "testdata/fresh-container-smoke/seccomp.json"), TrustedSourceRoot: root,
	}
}

func startSchedulerAcceptanceContainer(
	t *testing.T,
	workers *errgroup.Group,
	runner *FreshContainerRunner,
	configuration freshContainerSmokeConfiguration,
	plan gate.GatePlan,
	id workloadID,
	started chan<- FreshContainerLifecycleEvent,
	results chan<- schedulerDockerAcceptanceResult,
) {
	t.Helper()
	source := filepath.Join(configuration.TrustedSourceRoot, string(id))
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	request := FreshContainerRequest{
		Image: configuration.Image, ImageTruth: configuration.ImageTruth,
		SourceTreeSHA: configuration.SourceTreeSHA, SourceSnapshotDir: source,
		Profile: gate.ProfileLocalFast, Plan: plan, GateID: gate.GateIDWhitespaceCheck,
		ContainerLabels: map[string]string{schedulerAcceptanceLabel: schedulerAcceptanceValue, schedulerJobLabel: string(id)},
		LifecycleHook: func(_ context.Context, event FreshContainerLifecycleEvent) error {
			if event.Phase == FreshContainerPhaseStarted {
				started <- event
			}
			return nil
		},
	}
	workers.Go(func() error {
		result, err := runner.RunFreshContainer(context.Background(), request)
		results <- schedulerDockerAcceptanceResult{workloadID: string(id), result: result, err: err}
		return nil
	})
}

func waitSchedulerAcceptanceStarts(t *testing.T, started <-chan FreshContainerLifecycleEvent, count int) []FreshContainerLifecycleEvent {
	t.Helper()
	events := make([]FreshContainerLifecycleEvent, 0, count)
	for len(events) < count {
		select {
		case event := <-started:
			events = append(events, event)
		case <-time.After(30 * time.Second):
			t.Fatalf("timed out waiting for %d real Docker starts; got %d", count, len(events))
		}
	}
	return events
}

func waitSchedulerAcceptanceResult(t *testing.T, results <-chan schedulerDockerAcceptanceResult) schedulerDockerAcceptanceResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for real Docker completion")
		return schedulerDockerAcceptanceResult{}
	}
}

func assertSchedulerAcceptanceContainers(t *testing.T, events []FreshContainerLifecycleEvent) {
	t.Helper()
	if len(events) != 3 {
		t.Fatalf("running containers=%d want=3", len(events))
	}
	for _, event := range events {
		document := inspectSchedulerAcceptanceContainer(t, event.ContainerID)
		assertSchedulerAcceptanceHostConfig(t, event.ContainerID, document)
		assertSchedulerAcceptanceSourceMount(t, event.ContainerID, document)
	}
}

func inspectSchedulerAcceptanceContainer(t *testing.T, containerID string) schedulerDockerInspect {
	t.Helper()
	output, err := (execDockerRunner{}).Run(context.Background(), "inspect", containerID)
	if err != nil {
		t.Fatal(err)
	}
	var documents []schedulerDockerInspect
	if err := json.Unmarshal([]byte(output), &documents); err != nil {
		t.Fatalf("decode Docker inspect: %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("Docker inspect documents=%d want=1", len(documents))
	}
	return documents[0]
}

func assertSchedulerAcceptanceHostConfig(t *testing.T, containerID string, document schedulerDockerInspect) {
	t.Helper()
	if document.HostConfig.NanoCPUs != 4_000_000_000 {
		t.Fatalf("container %s NanoCPUs=%d", containerID, document.HostConfig.NanoCPUs)
	}
	if document.HostConfig.Memory != 8*bytesPerGiB {
		t.Fatalf("container %s memory=%d", containerID, document.HostConfig.Memory)
	}
	if document.HostConfig.Init == nil || !*document.HostConfig.Init {
		t.Fatalf("container %s init=%v", containerID, document.HostConfig.Init)
	}
	if document.HostConfig.NetworkMode != "none" {
		t.Fatalf("container %s network=%s", containerID, document.HostConfig.NetworkMode)
	}
	if !document.HostConfig.ReadonlyRootfs {
		t.Fatalf("container %s rootfs is writable", containerID)
	}
}

func assertSchedulerAcceptanceSourceMount(t *testing.T, containerID string, document schedulerDockerInspect) {
	t.Helper()
	for _, mount := range document.Mounts {
		if mount.Destination == "/workspace/source" && !mount.RW {
			return
		}
	}
	t.Fatalf("container %s source mount is not read-only: %#v", containerID, document.Mounts)
}

func assertNoSchedulerAcceptanceContainer(t *testing.T, workloadID string) {
	t.Helper()
	output, err := (execDockerRunner{}).Run(context.Background(), "ps", "--all", "--filter=label="+schedulerAcceptanceLabel+"="+schedulerAcceptanceValue, "--filter=label="+schedulerJobLabel+"="+workloadID, "--format={{.ID}}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "" {
		t.Fatalf("queued workload %s already has Docker container %s", workloadID, strings.TrimSpace(output))
	}
}

func assertNoSchedulerAcceptanceContainers(t *testing.T) {
	t.Helper()
	assertNoDockerContainersForAcceptanceValue(t, schedulerAcceptanceValue)
}

func assertNoDockerContainersForAcceptanceValue(t *testing.T, value string) {
	t.Helper()
	output, err := (execDockerRunner{}).Run(context.Background(), "ps", "--all", "--filter=label="+schedulerAcceptanceLabel+"="+value, "--format={{.ID}}")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != "" {
		t.Fatalf("acceptance containers were not removed: %s", strings.TrimSpace(output))
	}
}
