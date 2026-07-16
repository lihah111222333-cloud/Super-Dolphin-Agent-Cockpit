package localci

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const secondTestContainerID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type freshDockerRunnerStub struct {
	t             *testing.T
	request       FreshContainerRequest
	calls         [][]string
	imageMutation string
	waitForCancel bool
	waitErr       error
	waitCalls     int
	removeErr     error
	containerID   string
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
	if command == "start" || command == "kill" {
		return "", nil
	}
	switch command {
	case "logs":
		return "2026-07-16T00:00:00Z gate output\n", nil
	case "rm":
		return "", stub.removeErr
	case "ps":
		return "", nil
	default:
		return "", errors.New("unexpected lifecycle Docker command")
	}
}

func (stub *freshDockerRunnerStub) runCreate() (string, error) {
	if stub.containerID == "" {
		stub.containerID = testContainerID
	}
	return stub.containerID + "\n", nil
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
	return "0\n", nil
}

func (stub *freshDockerRunnerStub) imageInspectJSON() string {
	identity := stub.request.Image
	labels := map[string]string{
		labelPolicySHA: stub.request.ImageTruth.PolicySHA, labelSourceTreeSHA: stub.request.SourceTreeSHA,
		labelInputDigest: stub.request.ImageTruth.InputDigest, labelToolchainDigest: stub.request.ImageTruth.ToolchainDigest,
		labelSchemaVersion: stub.request.ImageTruth.SchemaVersion,
	}
	reference := identity.Registry + "@" + identity.PlatformManifestDigest
	document := map[string]any{
		"Id": identity.ConfigDigest, "RepoDigests": []string{reference}, "Os": identity.OS,
		"Architecture": identity.Architecture, "Variant": identity.Variant,
		"Config": map[string]any{"Labels": labels}, "RootFS": map[string]any{"Type": "layers", "Layers": identity.RootFSDiffIDs},
	}
	switch stub.imageMutation {
	case "manifest":
		document["RepoDigests"] = []string{identity.Registry + "@" + digest("9")}
	case "config":
		document["Id"] = digest("9")
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
	command, err := commandFromPlan(stub.request.Plan, stub.request.GateID)
	if err != nil {
		stub.t.Fatal(err)
	}
	exitCode := 0
	status := "created"
	finishedAt := ""
	if finished {
		status = "exited"
		finishedAt = "2026-07-16T00:00:01Z"
		if stub.waitForCancel || stub.waitErr != nil {
			exitCode = 137
		}
	}
	document := map[string]any{
		"Id": stub.containerID, "Image": stub.request.Image.ConfigDigest, "Path": command[0], "Args": command[1:],
		"Config": map[string]any{"Image": stub.request.Image.Registry + "@" + stub.request.Image.PlatformManifestDigest, "User": "65532:65532"},
		"HostConfig": map[string]any{
			"NanoCpus": int64(4_000_000_000), "Memory": int64(8 * 1024 * 1024 * 1024), "PidsLimit": int64(512),
			"ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges", "seccomp=/fixture/seccomp.json"},
			"NetworkMode": "none", "StorageOpt": map[string]string{"size": "10G"},
			"Tmpfs":     map[string]string{"/tmp": "rw,noexec,nosuid,nodev,size=2147483648"},
			"LogConfig": map[string]any{"Type": "local", "Config": map[string]string{"max-size": "10m", "max-file": "3"}},
		},
		"Mounts": []map[string]any{{"Type": "bind", "Source": stub.request.SourceSnapshotDir, "Destination": "/workspace/source", "RW": false}},
		"State":  map[string]any{"Status": status, "Running": false, "ExitCode": exitCode, "OOMKilled": false, "Error": "", "FinishedAt": finishedAt},
	}
	return marshalInspect(stub.t, stub.mustMarshal(document))
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

func TestRunFreshContainerReturnsMappableRemovalEvidence(t *testing.T) {
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
	for _, evidence := range result.Evidence {
		if err := evidence.Validate(); err != nil {
			t.Fatalf("evidence %#v: %v", evidence, err)
		}
	}
	if err := result.GateResult.Validate(); err != nil {
		t.Fatalf("gate result: %v", err)
	}
}

func TestRunFreshContainerRejectsImageInspectDriftBeforeCreate(t *testing.T) {
	for _, mutation := range []string{"manifest", "config", "rootfs", "platform", "label"} {
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

func TestRunFreshContainerTimeoutKillsAndRemoves(t *testing.T) {
	runner, stub, request := freshContainerFixture(t)
	stub.waitForCancel = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	result, err := runner.RunFreshContainer(ctx, request)
	if !errors.Is(err, context.DeadlineExceeded) || result.Status != gate.ResultStatusTimeout {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if !result.Killed || !result.Container.Removed || result.KillProofDigest == "" || result.GateResult != nil {
		t.Fatalf("timeout result = %#v", result)
	}
	if !calledDockerCommand(stub.calls, "kill", testContainerID) || !calledDockerCommand(stub.calls, "rm", "--force", testContainerID) {
		t.Fatalf("Docker calls = %#v", stub.calls)
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
	assertRegisteredFields(t, reflect.TypeOf(FreshContainerRequest{}), map[string]string{
		"Image": "identity and derived digest reference", "ImageTruth": "truth label verification",
		"SourceTreeSHA": "plan and image source binding", "SourceSnapshotDir": "private readonly mount",
		"Profile": "plan binding and timeout", "Plan": "canonical command closure", "GateID": "plan command selection",
	})
	assertRegisteredFields(t, reflect.TypeOf(FreshContainerImageTruth{}), map[string]string{
		"PolicySHA": "policy label", "InputDigest": "input label",
		"ToolchainDigest": "toolchain label", "SchemaVersion": "schema label",
	})
}

func assertRegisteredFields(t *testing.T, producer reflect.Type, consumerRegistry map[string]string) {
	t.Helper()
	producerFields := make([]string, 0, producer.NumField())
	for index := 0; index < producer.NumField(); index++ {
		producerFields = append(producerFields, producer.Field(index).Name)
	}
	for _, field := range producerFields {
		if strings.TrimSpace(consumerRegistry[field]) == "" {
			t.Fatalf("missing FreshContainerRequest consumer registration for %s", field)
		}
	}
	for field := range consumerRegistry {
		if !slices.Contains(producerFields, field) {
			t.Fatalf("stale FreshContainerRequest consumer registration for %s", field)
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
	sourceTree := strings.Repeat("b", 40)
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: gate.GitObjectFormatSHA1,
		Tree: &gate.TreeSource{SHA: sourceTree}, SourceTreeSHA: sourceTree,
	})
	if err != nil {
		t.Fatal(err)
	}
	return FreshContainerRequest{
		Image: gate.ImageIdentity{
			Registry: "registry.local/gate", OCIIndexDigest: digest("1"), PlatformManifestDigest: digest("2"),
			ConfigDigest: digest("3"), RootFSDiffIDs: []string{digest("4"), digest("5")}, OS: "linux", Architecture: "arm64",
		},
		ImageTruth:    FreshContainerImageTruth{PolicySHA: strings.Repeat("a", 40), InputDigest: digest("6"), ToolchainDigest: digest("7"), SchemaVersion: "1"},
		SourceTreeSHA: sourceTree, SourceSnapshotDir: source, Profile: gate.ProfileLocalFast, Plan: plan, GateID: gate.GateIDWhitespaceCheck,
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
