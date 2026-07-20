package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type freshDockerRunnerStub struct {
	t             *testing.T
	request       FreshContainerRequest
	calls         [][]string
	imageMutation string
	waitForCancel bool
	waitErr       error
	waitCalls     int
	waitExitCode  int
	oomKilled     bool
	logOutput     string
	removeErr     error
	psOutput      string
	createErr     error
	startErr      error
	containerID   string
	finishedAt    string
}

func (stub *freshDockerRunnerStub) Run(ctx context.Context, args ...string) (string, error) {
	stub.calls = append(stub.calls, append([]string(nil), args...))
	switch args[0] {
	case "image", "create", "wait", "inspect":
		return stub.runPrimary(ctx, args[0])
	default:
		return stub.runLifecycle(args[0])
	}
}

func (stub *freshDockerRunnerStub) runPrimary(ctx context.Context, command string) (string, error) {
	switch command {
	case "image":
		return stub.imageInspectJSON(), nil
	case "create":
		return stub.runCreate()
	case "wait":
		return stub.runWait(ctx)
	case "inspect":
		return stub.containerInspectJSON(stub.waitCalls > 0), nil
	default:
		return "", errors.New("unexpected primary Docker command")
	}
}

func (stub *freshDockerRunnerStub) runLifecycle(command string) (string, error) {
	if command == "start" {
		return "", stub.startErr
	}
	if command == "kill" {
		return "", nil
	}
	switch command {
	case "logs":
		if stub.logOutput != "" {
			return stub.logOutput, nil
		}
		return "2026-07-16T00:00:00Z gate output\n", nil
	case "rm":
		return "", stub.removeErr
	case "ps":
		return stub.psOutput, nil
	default:
		return "", errors.New("unexpected lifecycle Docker command")
	}
}

func (stub *freshDockerRunnerStub) runCreate() (string, error) {
	if stub.containerID == "" {
		stub.containerID = testContainerID
	}
	return stub.containerID + "\n", stub.createErr
}

func (stub *freshDockerRunnerStub) runWait(ctx context.Context) (string, error) {
	stub.waitCalls++
	if stub.waitForCancel && stub.waitCalls == 1 {
		<-ctx.Done()
		return "", ctx.Err()
	}
	if stub.waitErr != nil && stub.waitCalls == 1 {
		return "", stub.waitErr
	}
	if stub.waitForCancel || stub.waitErr != nil {
		return "137\n", nil
	}
	if stub.waitExitCode != 0 {
		return fmt.Sprintf("%d\n", stub.waitExitCode), nil
	}
	return "0\n", nil
}

func (stub *freshDockerRunnerStub) imageInspectJSON() string {
	identity := stub.request.Image
	labels := map[string]string{
		labelPolicySHA: stub.request.ImageTruth.PolicyDigest, labelSourceTreeSHA: stub.request.ImageTruth.BuildSourceTreeSHA,
		labelInputDigest: stub.request.ImageTruth.InputDigest, labelToolchainDigest: stub.request.ImageTruth.ToolchainDigest,
		labelSchemaVersion: stub.request.ImageTruth.SchemaVersion,
	}
	reference := identity.Registry + "@" + identity.PlatformManifestDigest
	descriptor := map[string]any{
		"digest": identity.PlatformManifestDigest, "mediaType": "application/vnd.docker.distribution.manifest.v2+json", "size": 512,
		"annotations": map[string]string{"config.digest": identity.ConfigDigest},
	}
	document := map[string]any{
		"Id": identity.PlatformManifestDigest, "RepoDigests": []string{reference}, "Os": identity.OS,
		"Architecture": identity.Architecture, "Variant": identity.Variant,
		"Descriptor": descriptor, "Config": map[string]any{"Labels": labels},
		"RootFS": map[string]any{"Type": "layers", "Layers": identity.RootFSDiffIDs},
	}
	switch stub.imageMutation {
	case "manifest":
		descriptor["digest"] = digest("9")
		document["RepoDigests"] = []string{identity.Registry + "@" + digest("9")}
	case "config":
		descriptor["annotations"] = map[string]string{"config.digest": digest("9")}
	case "missing descriptor config":
		descriptor["annotations"] = map[string]string{}
	case "rootfs":
		document["RootFS"] = map[string]any{"Type": "layers", "Layers": []string{digest("9")}}
	case "platform":
		document["Architecture"] = "amd64"
	case "label":
		labels[labelSourceTreeSHA] = strings.Repeat("c", 40)
	}
	return marshalInspect(stub.t, stub.mustMarshal(document))
}

func (stub *freshDockerRunnerStub) containerInspectJSON(finished bool) string {
	command, err := stub.containerCommand()
	if err != nil {
		stub.t.Fatal(err)
	}
	exitCode := 0
	status := "created"
	finishedAt := ""
	if finished {
		status = "exited"
		finishedAt = "2026-07-16T00:00:01Z"
		if stub.finishedAt != "" {
			finishedAt = stub.finishedAt
		}
		if stub.waitForCancel || stub.waitErr != nil {
			exitCode = 137
		} else if stub.waitExitCode != 0 {
			exitCode = stub.waitExitCode
		}
	}
	document := map[string]any{
		"Id": stub.containerID, "Image": stub.request.Image.PlatformManifestDigest, "Path": command[0], "Args": command[1:],
		"Config": map[string]any{
			"Image": stub.request.Image.Registry + "@" + stub.request.Image.PlatformManifestDigest, "User": "65532:65532",
			"WorkingDir": "/workspace/work", "Labels": stub.request.ContainerLabels,
			"Env": []string{
				"HOME=/workspace/work/home", "TMPDIR=/workspace/work/tmp", "GOCACHE=/workspace/work/go-cache",
				"GOMODCACHE=/workspace/work/go-mod-cache", "npm_config_cache=/workspace/work/npm-cache",
				"XDG_CACHE_HOME=/workspace/work/xdg-cache",
				"PLAYWRIGHT_BROWSERS_PATH=/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright",
			},
		},
		"HostConfig": map[string]any{
			"Init":     true,
			"NanoCpus": int64(4_000_000_000), "Memory": int64(8 * 1024 * 1024 * 1024), "PidsLimit": int64(512),
			"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges", "seccomp=/fixture/seccomp.json"},
			"NetworkMode": "none", "StorageOpt": map[string]string{"size": "10G"},
			"Tmpfs": map[string]string{
				"/tmp":            "rw,noexec,nosuid,nodev,size=2147483648",
				"/workspace/work": "rw,exec,nosuid,nodev,size=5368709120,uid=65532,gid=65532,mode=0700",
			},
			"LogConfig": map[string]any{"Type": "local", "Config": map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		"Mounts": []map[string]any{{"Type": "bind", "Source": stub.request.SourceSnapshotDir, "Destination": "/workspace/source", "RW": false}},
		"State":  map[string]any{"Status": status, "Running": false, "ExitCode": exitCode, "OOMKilled": stub.oomKilled, "Error": "", "FinishedAt": finishedAt},
	}
	return marshalInspect(stub.t, stub.mustMarshal(document))
}

func (stub *freshDockerRunnerStub) containerCommand() ([]string, error) {
	return freshContainerCommand(stub.request)
}

func (stub *freshDockerRunnerStub) mustMarshal(value any) json.RawMessage {
	stub.t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		stub.t.Fatal(err)
	}
	return data
}

func marshalInspect(t *testing.T, document json.RawMessage) string {
	t.Helper()
	data, err := json.Marshal([]json.RawMessage{document})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunFreshContainerAcceptsDocker29DescriptorConfigAndReturnsEvidence(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("RunFreshContainer() error = %v", err)
	}
	assertFreshContainerEvidence(t, result)
	if !calledDockerCommand(stub.calls, "image", "inspect") {
		t.Fatalf("Docker image inspect missing: %#v", stub.calls)
	}
	if !calledDockerCommand(stub.calls, "create") {
		t.Fatalf("Docker create missing: %#v", stub.calls)
	}
	if !calledDockerCommand(stub.calls, "ps") {
		t.Fatalf("Docker removal proof missing: %#v", stub.calls)
	}
}

func assertFreshContainerEvidence(t *testing.T, result FreshContainerResult) {
	t.Helper()
	if result.Status != gate.ResultStatusPassed || result.GateResult == nil || result.Container.ContainerID != testContainerID {
		t.Fatalf("result = %#v", result)
	}
	if err := result.Container.Validate(); err != nil {
		t.Fatalf("container evidence: %v", err)
	}
	assertContainerResourceWitness(t, result.Container)
	for _, evidence := range result.Evidence {
		if err := evidence.Validate(); err != nil {
			t.Fatalf("evidence %#v: %v", evidence, err)
		}
	}
	if err := result.GateResult.Validate(); err != nil {
		t.Fatalf("gate result: %v", err)
	}
	wantExitedAt := time.Date(2026, 7, 16, 0, 0, 1, 0, time.UTC)
	if !result.ExitedAt.Equal(wantExitedAt) {
		t.Fatalf("container exited at %s, want %s", result.ExitedAt, wantExitedAt)
	}
}

func assertContainerResourceWitness(t *testing.T, container gate.ContainerEvidence) {
	t.Helper()
	want := ExpectedFreshContainerResourceWitness()
	digest, err := want.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if container.ResourceWitness != want || container.ResourceWitnessDigest != digest {
		t.Fatalf("resource witness=%+v digest=%q", container.ResourceWitness, container.ResourceWitnessDigest)
	}
}

func TestRunFreshContainerOOMKeepsVerifiedResourceWitnessAfterRemoval(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	stub.waitExitCode = 137
	stub.oomKilled = true
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "OOM-killed") {
		t.Fatalf("RunFreshContainer() error = %v", err)
	}
	if result.Status != gate.ResultStatusInfraFailed || !result.Container.Removed {
		t.Fatalf("result status=%q removed=%v", result.Status, result.Container.Removed)
	}
	if result.ExitedAt.IsZero() || result.RemovalProofDigest == "" {
		t.Fatalf("OOM terminal evidence exited_at=%s removal_proof=%q", result.ExitedAt, result.RemovalProofDigest)
	}
	assertContainerResourceWitness(t, result.Container)
}

func TestRunFreshContainerRejectsImageInspectDriftBeforeCreate(t *testing.T) {
	for _, mutation := range []string{"manifest", "config", "missing descriptor config", "rootfs", "platform", "label"} {
		t.Run(mutation, func(t *testing.T) {
			runner, stub, request := freshContainerFixture(t)
			stub.imageMutation = mutation
			result, err := runner.RunFreshContainer(context.Background(), request)
			if err == nil || result.Status == gate.ResultStatusPassed || calledDockerCommand(stub.calls, "create") {
				t.Fatalf("result = %#v, err = %v, calls = %#v", result, err, stub.calls)
			}
		})
	}
}

func TestRunFreshContainerAcceptsDifferentBuildAndJobSourceTrees(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	if request.ImageTruth.BuildSourceTreeSHA == request.SourceTreeSHA {
		t.Fatal("fixture did not separate accepted image build tree from submitted job tree")
	}
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err != nil || result.Status != gate.ResultStatusPassed || !calledDockerCommand(stub.calls, "create") {
		t.Fatalf("result = %#v, err = %v, calls = %#v", result, err, stub.calls)
	}
}

func TestRunFreshContainerRejectsBuildSourceTreeLabelDrift(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	stub.imageMutation = "label"
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil || result.Status == gate.ResultStatusPassed || calledDockerCommand(stub.calls, "create") {
		t.Fatalf("result = %#v, err = %v, calls = %#v", result, err, stub.calls)
	}
}

func TestRunFreshContainerRequiresCanonicalJobAndBuildSourceTrees(t *testing.T) {
	for _, mutate := range []func(*FreshContainerRequest){
		func(request *FreshContainerRequest) { request.SourceTreeSHA = "not-a-git-oid" },
		func(request *FreshContainerRequest) { request.ImageTruth.BuildSourceTreeSHA = "" },
	} {
		runner, stub, request := freshContainerFixture(t)
		mutate(&request)
		result, err := runner.RunFreshContainer(context.Background(), request)
		if err == nil || result.Status == gate.ResultStatusPassed || len(stub.calls) != 0 {
			t.Fatalf("result = %#v, err = %v, calls = %#v", result, err, stub.calls)
		}
	}
}

func TestRunFreshContainerRejectsTagInjectionAndSourceTreeMismatch(t *testing.T) {
	for _, mutate := range []func(*FreshContainerRequest){
		func(request *FreshContainerRequest) { request.Image.Registry += ":latest" },
		func(request *FreshContainerRequest) { request.SourceTreeSHA = strings.Repeat("d", 40) },
	} {
		runner, stub, request := freshContainerFixture(t)
		mutate(&request)
		result, err := runner.RunFreshContainer(context.Background(), request)
		if err == nil || result.Status == gate.ResultStatusPassed || len(stub.calls) != 0 {
			t.Fatalf("result = %#v, err = %v, calls = %#v", result, err, stub.calls)
		}
	}
}

func TestRunFreshContainerWaitTransportErrorIsInfrastructureFailure(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	stub.waitErr = errors.New("Docker daemon transport failed")
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "Docker daemon transport failed") {
		t.Fatalf("RunFreshContainer() error = %v", err)
	}
	if result.Status != gate.ResultStatusInfraFailed || result.GateResult != nil {
		t.Fatalf("wait transport result = %#v", result)
	}
	if !result.Killed || !result.Container.Removed {
		t.Fatalf("wait transport cleanup result = %#v", result)
	}
	if result.Status == gate.ResultStatusTimeout {
		t.Fatal("ordinary Docker wait error was misclassified as timeout")
	}
}

func TestRunFreshContainerRemovalFailureCannotPass(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	stub.removeErr = errors.New("remove failed")
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil || result.Status != gate.ResultStatusInfraFailed || result.Container.Removed || result.GateResult != nil {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}

func TestRunFreshContainerCreateIDErrorPersistsRemovedWithoutFabricatedExit(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	stub.createErr = errors.New("create response interrupted")
	events := make([]FreshContainerLifecycleEvent, 0, 3)
	request.LifecycleHook = func(_ context.Context, event FreshContainerLifecycleEvent) error {
		events = append(events, event)
		return nil
	}

	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil || result.Container.ContainerID != testContainerID || !result.Container.Removed || result.RemovalProofDigest == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertCleanupWithoutObservedExit(t, events, FreshContainerPhaseCreated)
	if !result.ExitedAt.IsZero() {
		t.Fatalf("create cleanup fabricated exited_at %s", result.ExitedAt)
	}
}

func TestRunFreshContainerStartErrorPersistsRemovedWithoutFabricatedExit(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	stub.startErr = errors.New("start failed before process execution")
	events := make([]FreshContainerLifecycleEvent, 0, 6)
	request.LifecycleHook = func(_ context.Context, event FreshContainerLifecycleEvent) error {
		events = append(events, event)
		return nil
	}

	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil || result.Status != gate.ResultStatusInfraFailed || !result.Container.Removed || result.RemovalProofDigest == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	assertCleanupWithoutObservedExit(t, events, FreshContainerPhaseStarting)
	if !result.ExitedAt.IsZero() {
		t.Fatalf("start cleanup fabricated exited_at %s", result.ExitedAt)
	}
}

type nilFreshDockerRunner struct{}

func (*nilFreshDockerRunner) Run(context.Context, ...string) (string, error) { return "", nil }

func TestFreshContainerRunnerRejectsTypedNilAndCancelledContext(t *testing.T) {
	seccomp, trustedRoot, _ := canonicalDockerFixture(t)
	var typedNil *nilFreshDockerRunner
	if _, err := newFreshContainerRunner(typedNil, seccomp, trustedRoot); err == nil {
		t.Fatal("newFreshContainerRunner() accepted a typed-nil Docker runner")
	}
	runner, stub, request := freshContainerFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := runner.RunFreshContainer(ctx, request)
	if !errors.Is(err, context.Canceled) || result.Status != gate.ResultStatusCancelled || len(stub.calls) != 0 {
		t.Fatalf("result = %#v, err = %v, calls = %#v", result, err, stub.calls)
	}
}

func TestFreshContainerRequestFieldRegistryIsComplete(t *testing.T) {
	assertRegisteredFields(t, reflect.TypeFor[FreshContainerRequest](), map[string]string{
		"Image": "identity and derived digest reference", "ImageTruth": "truth label verification",
		"SourceTreeSHA": "plan and image source binding", "SourceSnapshotDir": "private readonly mount",
		"Profile": "plan binding and timeout", "Plan": "canonical command closure", "GateID": "plan command selection",
		"PlanExecution":   "single-container canonical plan execution",
		"ShardGateIDs":    "exact canonical shard command and report coverage",
		"ShardIdentity":   "coordinator binding for one exact canonical shard",
		"ContainerLabels": "durable coordinator and Docker identity binding",
		"Deadline":        "original first-start deadline", "ClaimDeadline": "durable shared shard execution clock",
		"LifecycleHook": "durable crash-point transitions",
	})
	assertRegisteredFields(t, reflect.TypeFor[FreshContainerImageTruth](), map[string]string{
		"PolicyDigest":       "policy label",
		"BuildSourceTreeSHA": "accepted image build provenance label",
		"InputDigest":        "input label",
		"ToolchainDigest":    "toolchain label", "SchemaVersion": "schema label",
	})
}

func TestFreshContainerLifecycleEventFieldRegistryIsComplete(t *testing.T) {
	assertRegisteredFields(t, reflect.TypeFor[FreshContainerLifecycleEvent](), map[string]string{
		"Phase": "lifecycle transition", "ContainerID": "durable container identity",
		"ImageReference": "immutable image reference", "ConfigDigest": "image config identity",
		"HostConfigDigest": "host isolation evidence", "ResourceWitness": "resource limits evidence",
		"ResourceWitnessDigest": "resource evidence digest", "SourceSnapshotDir": "readonly source mount",
		"StartedAt": "execution clock", "Deadline": "execution deadline",
		"ExitedAt": "Docker terminal inspect time", "CompletedAt": "bounded evidence completion time",
		"ExitCode": "terminal process status", "RemovalProofDigest": "container removal proof",
	})
}

func TestPlanContainerCommandAndReportAreExactAndUnique(t *testing.T) {
	_, _, source := canonicalDockerFixture(t)
	request := validFreshContainerRequest(t, source)
	request.PlanExecution = true
	request.GateID = request.Plan.Gates[0].ID
	command, err := validateFreshContainerRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := gate.PlanExecutorArgv(request.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command, want) {
		t.Fatalf("plan container command = %v, want %v", command, want)
	}
	report := validPlanReport(request.Plan)
	for index := range report.Gates {
		report.Gates[index].Log = []byte(strings.Repeat("timestamped Docker chunk evidence\n", 200))
		report.Gates[index].LogDigest = digestBytes(report.Gates[index].Log)
	}
	logOutput := timestampedPlanReportLog(t, report)
	result := FreshContainerResult{ExitCode: 0, LogOutput: logOutput}
	if err := collectPlanGateResults(&result, request); err != nil {
		t.Fatalf("collect canonical plan report: %v", err)
	}
	if len(result.PlanGateResults) != len(request.Plan.Gates) {
		t.Fatalf("collected plan results = %d, want %d", len(result.PlanGateResults), len(request.Plan.Gates))
	}
	failedReport := validPlanReport(request.Plan)
	failedReport.Gates[0].Status = gate.ResultStatusFailed
	failedReport.Gates[0].ExitCode = 1
	failedLog := timestampedPlanReportLog(t, failedReport)
	failedResult := FreshContainerResult{ExitCode: 1, LogOutput: failedLog}
	if err := collectPlanGateResults(&failedResult, request); err != nil || len(failedResult.PlanGateResults) != len(request.Plan.Gates) {
		t.Fatalf("collect failed plan report: results=%d err=%v", len(failedResult.PlanGateResults), err)
	}
	result.LogOutput = append(append([]byte(nil), logOutput...), logOutput...)
	if err := collectPlanGateResults(&result, request); err == nil {
		t.Fatal("collector accepted duplicate plan report prefix")
	}
}

func TestShardContainerCommandAndReportAreExact(t *testing.T) {
	_, _, source := canonicalDockerFixture(t)
	request := canonicalShardRequest(t, validFreshContainerRequest(t, source))
	command, err := validateFreshContainerRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := gate.ContainerShardExecutorArgv(request.Plan, request.ShardGateIDs)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command, want) {
		t.Fatalf("shard container command = %v, want %v", command, want)
	}
	report := canonicalShardReport(request, []byte(strings.Repeat("timestamped shard Docker evidence\n", 200)))
	result := FreshContainerResult{ExitCode: 0, LogOutput: timestampedPlanReportLog(t, report)}
	if err := collectPlanGateResults(&result, request); err != nil {
		t.Fatalf("collect shard plan report: %v", err)
	}
	if len(result.PlanGateResults) != len(request.ShardGateIDs) {
		t.Fatalf("collected shard results = %d, want %d", len(result.PlanGateResults), len(request.ShardGateIDs))
	}
}

func TestRunFreshContainerShardCollectsCanonicalReport(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request = canonicalShardRequest(t, request)
	stub.request = request

	report := canonicalShardReport(request, []byte(strings.Repeat("canonical shard Docker evidence\n", MaxFreshContainerLogBytes/48)))
	stub.logOutput = string(timestampedPlanReportLog(t, report))
	if len(stub.logOutput) <= MaxFreshContainerLogBytes {
		t.Fatalf("shard report log = %d bytes, want more than single-gate limit %d", len(stub.logOutput), MaxFreshContainerLogBytes)
	}

	result, err := runner.RunFreshContainer(context.Background(), request)
	if err != nil {
		t.Fatalf("RunFreshContainer() shard error = %v", err)
	}
	if result.Status != gate.ResultStatusPassed || result.GateResult != nil {
		t.Fatalf("shard container result = %#v", result)
	}
	if len(result.PlanGateResults) != len(request.ShardGateIDs) {
		t.Fatalf("shard plan results = %d, want %d", len(result.PlanGateResults), len(request.ShardGateIDs))
	}
	for index, id := range request.ShardGateIDs {
		gateResult := result.PlanGateResults[index].GateResult
		if gateResult.GateID != string(id) {
			t.Fatalf("shard gate result %d = %q, want %q", index, gateResult.GateID, id)
		}
		if err := gateResult.Validate(); err != nil {
			t.Fatalf("shard gate result %q: %v", id, err)
		}
	}
}

func TestRunFreshContainerCancelledShardCollectsTerminalCoverage(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request = canonicalShardRequest(t, request)
	ctx, cancel := context.WithCancel(context.Background())
	observed := make([]terminalLifecycleObservation, 0, 2)
	request.LifecycleHook = observeCancelledTerminalLifecycle(cancel, &observed)
	stub.request = request
	stub.waitForCancel = true
	defer cancel()

	result, err := runner.RunFreshContainer(ctx, request)
	assertCancelledShardTerminalCoverage(t, result, err, request)
	assertTerminalLifecycle(t, observed, result)
}

func TestRunFreshContainerCancelledShardRejectsForgedReport(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request = canonicalShardRequest(t, request)
	ctx, cancel := context.WithCancel(context.Background())
	request.LifecycleHook = cancelOnStarted(cancel)
	stub.request = request
	stub.waitForCancel = true
	report := canonicalShardReport(request, []byte(strings.Repeat("forged shard report\n", MaxFreshContainerLogBytes/48)))
	report.PlanDigest = digest("f")
	stub.logOutput = string(timestampedPlanReportLog(t, report))
	defer cancel()

	result, err := runner.RunFreshContainer(ctx, request)
	assertCancelledShardForgedReportRejected(t, result, err)
}

func canonicalShardRequest(t *testing.T, request FreshContainerRequest) FreshContainerRequest {
	t.Helper()
	set, err := gate.BuildContainerShardSet(request.Plan, digest("a"), digest("b"))
	if err != nil {
		t.Fatal(err)
	}
	request.ShardGateIDs = append([]gate.GateID(nil), set.Shards[0].GateIDs...)
	request.ShardIdentity = set.Shards[0].IdentityDigest
	request.ClaimDeadline = func(_ context.Context, started time.Time) (time.Time, error) {
		return started.Add(time.Minute), nil
	}
	return request
}

func canonicalShardReport(request FreshContainerRequest, log []byte) gate.PlanExecutionReport {
	report := validPlanReport(request.Plan)
	byID := make(map[gate.GateID]gate.PlanGateExecution, len(report.Gates))
	for _, execution := range report.Gates {
		byID[execution.GateID] = execution
	}
	report.Gates = report.Gates[:0]
	for _, id := range request.ShardGateIDs {
		execution := byID[id]
		execution.Log = append([]byte(nil), log...)
		execution.LogDigest = digestBytes(execution.Log)
		report.Gates = append(report.Gates, execution)
	}
	return report
}

func cancelOnStarted(cancel context.CancelFunc) FreshContainerLifecycleHook {
	return func(_ context.Context, event FreshContainerLifecycleEvent) error {
		if event.Phase == FreshContainerPhaseStarted {
			cancel()
		}
		return nil
	}
}

func assertCancelledShardTerminalCoverage(t *testing.T, result FreshContainerResult, runErr error, request FreshContainerRequest) {
	t.Helper()
	if !errors.Is(runErr, context.Canceled) || result.Status != gate.ResultStatusCancelled || !result.Killed || !result.Container.Removed {
		t.Fatalf("cancelled shard result = %#v, err = %v", result, runErr)
	}
	if len(result.PlanGateResults) != len(request.ShardGateIDs) {
		t.Fatalf("cancelled shard results = %d, want %d", len(result.PlanGateResults), len(request.ShardGateIDs))
	}
	for index, id := range request.ShardGateIDs {
		assertCancelledShardGate(t, result.PlanGateResults[index], id, result.LogDigest)
	}
}

func assertCancelledShardGate(t *testing.T, observed FreshPlanGateResult, id gate.GateID, rawLogDigest string) {
	t.Helper()
	if observed.GateResult.GateID != string(id) || observed.Status != gate.ResultStatusCancelled || observed.GateResult.ExitCode != -1 {
		t.Fatalf("cancelled shard gate %q = %#v", id, observed)
	}
	if !strings.Contains(string(observed.LogOutput), rawLogDigest) || observed.GateResult.LogDigest != digestBytes(observed.LogOutput) {
		t.Fatalf("cancelled shard log evidence = %#v", observed)
	}
	if err := observed.GateResult.Validate(); err != nil {
		t.Fatalf("cancelled shard gate %q: %v", id, err)
	}
}

func assertCancelledShardForgedReportRejected(t *testing.T, result FreshContainerResult, runErr error) {
	t.Helper()
	if runErr == nil || result.Status != gate.ResultStatusInfraFailed || !result.Killed || !result.Container.Removed {
		t.Fatalf("forged cancelled shard result = %#v, err = %v", result, runErr)
	}
	if len(result.PlanGateResults) != 0 {
		t.Fatalf("forged cancelled shard gate results = %#v", result.PlanGateResults)
	}
}

func TestPlanReportCancelledGateRoundTrip(t *testing.T) {
	_, _, source := canonicalDockerFixture(t)
	request := validFreshContainerRequest(t, source)
	request.PlanExecution = true
	request.GateID = request.Plan.Gates[0].ID
	report := validPlanReport(request.Plan)
	report.Gates[0].Status = gate.ResultStatusFailed
	report.Gates[0].ExitCode = 2
	report.Gates[1].Status = gate.ResultStatusCancelled
	report.Gates[1].ExitCode = -1
	result := FreshContainerResult{ExitCode: 2, LogOutput: timestampedPlanReportLog(t, report)}
	if err := collectPlanGateResults(&result, request); err != nil {
		t.Fatalf("collect cancelled plan report: %v", err)
	}
	if got := result.PlanGateResults[1]; got.Status != gate.ResultStatusCancelled ||
		got.GateResult.Status != gate.GateStatusCancelled || got.GateResult.ExitCode != -1 {
		t.Fatalf("cancelled plan result roundtrip = %#v", got)
	}
}

func TestPlanContainerCodeFailureRemainsFailedWithValidTerminalEvidence(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	request.PlanExecution = true
	request.GateID = request.Plan.Gates[0].ID
	report := validPlanReport(request.Plan)
	report.Gates[0].Status = gate.ResultStatusFailed
	report.Gates[0].ExitCode = 2
	report.Gates[0].Log = []byte("project map drifted\n")
	report.Gates[0].LogDigest = digestBytes(report.Gates[0].Log)
	stub.request = request
	stub.waitExitCode = 2
	stub.logOutput = string(timestampedPlanReportLog(t, report))
	result, err := runner.RunFreshContainer(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "exited with code 2") {
		t.Fatalf("failed plan error = %v", err)
	}
	if result.Status != gate.ResultStatusFailed || !result.Container.Removed || len(result.PlanGateResults) != len(request.Plan.Gates) {
		t.Fatalf("failed plan result = %#v", result)
	}
	if result.PlanGateResults[0].Status != gate.ResultStatusFailed || result.PlanGateResults[0].GateResult.ExitCode != 2 {
		t.Fatalf("failed plan gate result = %#v", result.PlanGateResults[0])
	}
}

func validPlanReport(plan gate.GatePlan) gate.PlanExecutionReport {
	now := time.Now().UTC()
	report := gate.PlanExecutionReport{SchemaVersion: 1, Profile: plan.Profile, PlanDigest: plan.PlanDigest}
	for _, spec := range plan.Gates {
		log := []byte("passed " + string(spec.ID) + "\n")
		digest := digestBytes(log)
		report.Gates = append(report.Gates, gate.PlanGateExecution{
			GateID: spec.ID, Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: now, CompletedAt: now.Add(time.Millisecond),
			Log: log, LogDigest: digest,
		})
	}
	return report
}

func timestampedPlanReportLog(t *testing.T, report gate.PlanExecutionReport) []byte {
	t.Helper()
	chunks, err := gate.EncodePlanExecutionReportChunks(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 2 {
		t.Fatalf("plan report chunks = %d, want timestamped split", len(chunks))
	}
	var output []byte
	for _, chunk := range chunks {
		output = append(output, []byte("2026-07-18T00:00:00.000000000Z "+chunk+"\n")...)
	}
	return output
}

func assertRegisteredFields(t *testing.T, producer reflect.Type, consumerRegistry map[string]string) {
	t.Helper()
	producerFields := make([]string, 0, producer.NumField())
	for index := 0; index < producer.NumField(); index++ {
		producerFields = append(producerFields, producer.Field(index).Name)
	}
	for _, field := range producerFields {
		if strings.TrimSpace(consumerRegistry[field]) == "" {
			t.Fatalf("missing %s consumer registration for %s", producer.Name(), field)
		}
	}
	for field := range consumerRegistry {
		if !slices.Contains(producerFields, field) {
			t.Fatalf("stale %s consumer registration for %s", producer.Name(), field)
		}
	}
}

func freshContainerFixture(t *testing.T) (*FreshContainerRunner, *freshDockerRunnerStub, FreshContainerRequest) {
	t.Helper()
	seccomp, trustedRoot, source := canonicalDockerFixture(t)
	request := validFreshContainerRequest(t, source)
	stub := &freshDockerRunnerStub{t: t, request: request, containerID: testContainerID}
	runner, err := newFreshContainerRunner(stub, seccomp, trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	return runner, stub, request
}

func canonicalDockerFixture(t *testing.T) (string, string, string) {
	t.Helper()
	seccomp, root, source := dockerFixture(t)
	for _, path := range []string{seccomp, root, source} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		switch path {
		case seccomp:
			seccomp = resolved
		case root:
			root = resolved
		case source:
			source = resolved
		}
	}
	if err := os.Chmod(source, 0o700); err != nil {
		t.Fatal(err)
	}
	return seccomp, root, source
}

func validFreshContainerRequest(t *testing.T, source string) FreshContainerRequest {
	t.Helper()
	jobSourceTree := strings.Repeat("b", 40)
	buildSourceTree := strings.Repeat("a", 40)
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: jobSourceTree}, SourceTreeSHA: jobSourceTree,
	})
	if err != nil {
		t.Fatal(err)
	}
	return FreshContainerRequest{
		Image: gate.ImageIdentity{
			Registry: "registry.local/gate", OCIIndexDigest: digest("1"), PlatformManifestDigest: digest("2"),
			ConfigDigest: digest("3"), RootFSDiffIDs: []string{digest("4"), digest("5")}, OS: "linux", Architecture: "arm64",
		},
		ImageTruth: FreshContainerImageTruth{
			PolicyDigest: digest("8"), BuildSourceTreeSHA: buildSourceTree,
			InputDigest: digest("6"), ToolchainDigest: digest("7"), SchemaVersion: imageInputSchemaVersion,
		},
		SourceTreeSHA: jobSourceTree, SourceSnapshotDir: source, Profile: gate.ProfileLocalFast, Plan: plan, GateID: gate.GateIDWhitespaceCheck,
	}
}

func calledDockerCommand(calls [][]string, prefix ...string) bool {
	for _, call := range calls {
		if len(call) >= len(prefix) && slices.Equal(call[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}
