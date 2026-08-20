package eci

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestArtifactBuilderRequestBindsPortableSourceBaseline(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatalf("NewArtifactBuilderRequest() error = %v", err)
	}
	assertArtifactBuilderRequestIdentity(t, request)
	assertArtifactBuilderCommand(t, request)
}

func assertArtifactBuilderRequestIdentity(t *testing.T, request ArtifactBuilderRequest) {
	t.Helper()
	if request.Provider != ArtifactBuilderProvider || request.ArtifactKind != ArtifactBuilderArtifactKind || request.ArtifactRef != ArtifactBuilderArtifactRef {
		t.Fatalf("request protocol identity = %#v", request)
	}
	if request.EphemeralStorageGiB != ArtifactBuilderEphemeralStorageGiB || request.CPU != ArtifactBuilderCPU || request.MemoryGiB != ArtifactBuilderMemoryGiB {
		t.Fatalf("request resources = %#v", request)
	}
	if request.CompilerPath != testArtifactBuilderConfig().CompilerPath {
		t.Fatalf("request compiler path = %q", request.CompilerPath)
	}
}

func assertArtifactBuilderCommand(t *testing.T, request ArtifactBuilderRequest) {
	t.Helper()
	command, err := request.CommandLine()
	if err != nil {
		t.Fatalf("CommandLine() error = %v", err)
	}
	if len(command) < 2 || command[0] != ArtifactBuilderCommand || command[1] != ArtifactBuilderSubcommand {
		t.Fatalf("builder command = %#v", command)
	}
	if slices.Contains(command, "go") || slices.Contains(command, "test") || slices.Contains(command, "workload") {
		t.Fatalf("builder command unexpectedly contains workload execution: %#v", command)
	}
	for _, value := range []string{testArtifactBuilderConfig().Input.ArtifactPrefix, testArtifactBuilderConfig().Input.Key, testArtifactBuilderConfig().Input.SHA256, testArtifactBuilderConfig().Input.SourceCommitSHA, testArtifactBuilderConfig().Input.SourceTreeSHA, testArtifactBuilderConfig().Input.ExpectedBuilderDigest, testArtifactBuilderConfig().Input.ExpectedToolchainDigest, testArtifactBuilderConfig().Input.ProtocolABIDigest, testArtifactBuilderConfig().Input.WorkloadManifestABIDigest} {
		if !slices.Contains(command, value) {
			t.Errorf("builder command does not bind input value %q: %#v", value, command)
		}
	}
}

func TestArtifactBuilderCommandLineHasExactInputFieldSet(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatal(err)
	}
	command, err := request.CommandLine()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--compiler-path", "--execution-provider", "--artifact-kind", "--artifact-ref", "--artifact-prefix", "--input-key", "--input-sha256", "--input-size-bytes", "--repository-id", "--object-format", "--source-commit-sha", "--source-tree-sha", "--expected-builder-digest", "--expected-toolchain-digest", "--protocol-abi-digest", "--workload-manifest-abi-digest", "--oss-bucket", "--oss-endpoint", "--ram-role-name"}
	if len(command[2:]) != len(want)*2 {
		t.Fatalf("builder command field count = %d, want %d: %#v", len(command[2:])/2, len(want), command)
	}
	for index, field := range want {
		if command[2+index*2] != field {
			t.Fatalf("builder command field %d = %q, want %q: %#v", index, command[2+index*2], field, command)
		}
	}
}

func TestArtifactBuilderRequestRejectsIdentityResourceSecretAndPathDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ArtifactBuilderConfig)
	}{
		{name: "wrong provider", mutate: func(config *ArtifactBuilderConfig) { config.Provider = "github-actions/v1" }},
		{name: "mutable image", mutate: func(config *ArtifactBuilderConfig) { config.Image = "registry.example.com/runtime:latest" }},
		{name: "wrong snapshot", mutate: func(config *ArtifactBuilderConfig) { config.ImageSnapshotID = "" }},
		{name: "mutable snapshot", mutate: func(config *ArtifactBuilderConfig) { config.ImageSnapshotID = "latest" }},
		{name: "wrong CPU", mutate: func(config *ArtifactBuilderConfig) { config.CPU = 2 }},
		{name: "wrong memory", mutate: func(config *ArtifactBuilderConfig) { config.MemoryGiB = 4 }},
		{name: "wrong ephemeral storage", mutate: func(config *ArtifactBuilderConfig) { config.EphemeralStorageGiB = 20 }},
		{name: "one vSwitch", mutate: func(config *ArtifactBuilderConfig) { config.VSwitches = config.VSwitches[:1] }},
		{name: "missing compiler path", mutate: func(config *ArtifactBuilderConfig) { config.CompilerPath = "" }},
		{name: "unclean compiler path", mutate: func(config *ArtifactBuilderConfig) { config.CompilerPath = "/usr/local/go/../go/bin/go" }},
		{name: "credential in input key", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "objects/token=password" }},
		{name: "signed URL", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "https://oss.example/bundle?signature=secret" }},
		{name: "absolute input path", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "/tmp/source.bundle" }},
		{name: "path traversal", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "objects/../source.bundle" }},
		{name: "missing artifact prefix", mutate: func(config *ArtifactBuilderConfig) { config.Input.ArtifactPrefix = "" }},
		{name: "artifact prefix without slash", mutate: func(config *ArtifactBuilderConfig) { config.Input.ArtifactPrefix = "objects" }},
		{name: "artifact prefix traversal", mutate: func(config *ArtifactBuilderConfig) { config.Input.ArtifactPrefix = "objects/../" }},
		{name: "artifact prefix absolute", mutate: func(config *ArtifactBuilderConfig) { config.Input.ArtifactPrefix = "/objects/" }},
		{name: "input prefix drift", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "other/source.bundle" }},
		{name: "secret role value", mutate: func(config *ArtifactBuilderConfig) { config.RAMRoleName = "role/password=secret" }},
		{name: "zero source commit oid", mutate: func(config *ArtifactBuilderConfig) { config.Input.SourceCommitSHA = strings.Repeat("0", 40) }},
		{name: "zero source tree oid", mutate: func(config *ArtifactBuilderConfig) { config.Input.SourceTreeSHA = strings.Repeat("0", 40) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := testArtifactBuilderConfig()
			testCase.mutate(&config)
			if _, err := NewArtifactBuilderRequest(config); err == nil {
				t.Fatal("NewArtifactBuilderRequest() accepted invalid builder identity")
			}
		})
	}
}

func TestArtifactBuilderInputRejectsEveryZeroField(t *testing.T) {
	input := testArtifactBuilderConfig().Input
	value := reflect.ValueOf(&input).Elem()
	typeOfInput := value.Type()
	for index := 0; index < value.NumField(); index++ {
		name := typeOfInput.Field(index).Name
		t.Run(name, func(t *testing.T) {
			candidate := testArtifactBuilderConfig().Input
			field := reflect.ValueOf(&candidate).Elem().Field(index)
			field.Set(reflect.Zero(field.Type()))
			if err := candidate.Validate(); err == nil {
				t.Fatalf("ArtifactBuilderInput.Validate() accepted zero field %s", name)
			}
		})
	}
}

func TestArtifactBuilderLifecycleRejectsLiveCommandInputResourceScheduleAndTagDrift(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatalf("NewArtifactBuilderRequest() error = %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*ArtifactBuilderGroup)
	}{
		{name: "group command", mutate: func(group *ArtifactBuilderGroup) { group.Command[1] = "_wrong" }},
		{name: "container command", mutate: func(group *ArtifactBuilderGroup) { group.Container.Command[1] = "_wrong" }},
		{name: "input sha", mutate: func(group *ArtifactBuilderGroup) { group.Input.SHA256 = "sha256:" + strings.Repeat("9", 64) }},
		{name: "input size", mutate: func(group *ArtifactBuilderGroup) { group.Input.SizeBytes++ }},
		{name: "source commit", mutate: func(group *ArtifactBuilderGroup) { group.Input.SourceCommitSHA = strings.Repeat("9", 40) }},
		{name: "source tree", mutate: func(group *ArtifactBuilderGroup) { group.Input.SourceTreeSHA = strings.Repeat("9", 40) }},
		{name: "cpu", mutate: func(group *ArtifactBuilderGroup) { group.CPU = 2 }},
		{name: "memory", mutate: func(group *ArtifactBuilderGroup) { group.MemoryGiB = 4 }},
		{name: "ephemeral storage", mutate: func(group *ArtifactBuilderGroup) { group.EphemeralStorageGiB = 20 }},
		{name: "vSwitch", mutate: func(group *ArtifactBuilderGroup) { group.VSwitches[0].ZoneID = "cn-test-drift" }},
		{name: "schedule", mutate: func(group *ArtifactBuilderGroup) { group.ScheduleStrategy = "VSwitchRandomDrift" }},
		{name: "security group", mutate: func(group *ArtifactBuilderGroup) { group.SecurityGroupID = "sg-drift" }},
		{name: "RAM role", mutate: func(group *ArtifactBuilderGroup) { group.RAMRoleName = "role-drift" }},
		{name: "compiler path", mutate: func(group *ArtifactBuilderGroup) { group.CompilerPath = "/usr/local/go/bin/other-go" }},
		{name: "request digest tag", mutate: func(group *ArtifactBuilderGroup) {
			group.Tags[ArtifactBuilderRequestDigestTag] = "sha256:" + strings.Repeat("9", 64)
		}},
		{name: "input digest tag", mutate: func(group *ArtifactBuilderGroup) {
			group.Tags[ArtifactBuilderInputDigestTag] = "sha256:" + strings.Repeat("9", 64)
		}},
		{name: "secret tag", mutate: func(group *ArtifactBuilderGroup) { group.Tags["token"] = "sha256:" + strings.Repeat("9", 64) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			group := testArtifactBuilderGroup(t, request, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
			testCase.mutate(&group)
			lifecycle, err := NewArtifactBuilderLifecycle(request)
			if err != nil {
				t.Fatalf("NewArtifactBuilderLifecycle() error = %v", err)
			}
			if err := lifecycle.ObserveCreated(group); err == nil {
				t.Fatal("ObserveCreated() accepted live identity drift")
			}
			if lifecycle.State() != ArtifactBuilderStateIdentityDrift {
				t.Fatalf("identity drift state = %s", lifecycle.State())
			}
			if err := lifecycle.MarkDeleteRequested(); err != nil {
				t.Fatalf("MarkDeleteRequested() after identity drift error = %v", err)
			}
			if lifecycle.State() != ArtifactBuilderStateDeleteRequested {
				t.Fatalf("cleanup state = %s", lifecycle.State())
			}
		})
	}
}

func TestArtifactBuilderLifecycleCancellationAndErrorCannotBecomeSucceeded(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatalf("NewArtifactBuilderRequest() error = %v", err)
	}
	assertArtifactBuilderCancellationCleanup(t, request)
	assertArtifactBuilderErrorCleanup(t, request)
}

func assertArtifactBuilderCancellationCleanup(t *testing.T, request ArtifactBuilderRequest) {
	t.Helper()
	lifecycle, err := NewArtifactBuilderLifecycle(request)
	if err != nil {
		t.Fatalf("NewArtifactBuilderLifecycle() error = %v", err)
	}
	if err := lifecycle.ObserveCreated(testArtifactBuilderGroup(t, request, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)); err != nil {
		t.Fatalf("ObserveCreated(running) error = %v", err)
	}
	if err := lifecycle.MarkCancelled(); err != nil {
		t.Fatalf("MarkCancelled() error = %v", err)
	}
	if lifecycle.State() != ArtifactBuilderStateCancelled {
		t.Fatalf("cancelled state = %s", lifecycle.State())
	}
	if err := lifecycle.MarkDeleteRequested(); err != nil {
		t.Fatalf("MarkDeleteRequested() after cancellation error = %v", err)
	}
	if err := lifecycle.ConfirmAbsent(true); err != nil {
		t.Fatalf("ConfirmAbsent() after cancellation error = %v", err)
	}
	if lifecycle.State() != ArtifactBuilderStateAbsent {
		t.Fatalf("absent state = %s", lifecycle.State())
	}
	if err := lifecycle.Observe(testArtifactBuilderGroup(t, request, ArtifactBuilderGroupSucceeded, ArtifactBuilderContainerTerminated, nil)); err == nil {
		t.Fatal("cancelled builder became observable after cleanup")
	}
}

func assertArtifactBuilderErrorCleanup(t *testing.T, request ArtifactBuilderRequest) {
	t.Helper()
	errorLifecycle, err := NewArtifactBuilderLifecycle(request)
	if err != nil {
		t.Fatalf("NewArtifactBuilderLifecycle(error path) error = %v", err)
	}
	if err := errorLifecycle.ObserveCreated(testArtifactBuilderGroup(t, request, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)); err != nil {
		t.Fatalf("ObserveCreated(error path) error = %v", err)
	}
	if err := errorLifecycle.Observe(testArtifactBuilderGroup(t, request, ArtifactBuilderGroupError, ArtifactBuilderContainerRunning, nil)); err != nil {
		t.Fatalf("Observe(error status) error = %v", err)
	}
	if errorLifecycle.State() != ArtifactBuilderStateError {
		t.Fatalf("error state = %s", errorLifecycle.State())
	}
	if err := errorLifecycle.MarkDeleteRequested(); err != nil {
		t.Fatalf("MarkDeleteRequested() after error error = %v", err)
	}
	if err := errorLifecycle.ConfirmAbsent(true); err != nil {
		t.Fatalf("ConfirmAbsent() after error error = %v", err)
	}
}

func TestArtifactBuilderLifecycleValidatesLiveIdentityTerminalEvidenceAndCleanup(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatalf("NewArtifactBuilderRequest() error = %v", err)
	}
	lifecycle, err := NewArtifactBuilderLifecycle(request)
	if err != nil {
		t.Fatalf("NewArtifactBuilderLifecycle() error = %v", err)
	}
	assertArtifactBuilderState(t, lifecycle, ArtifactBuilderStateNew)
	advanceArtifactBuilderToRunning(t, lifecycle, request)
	advanceArtifactBuilderToSucceeded(t, lifecycle, request)
	assertArtifactBuilderCleanup(t, lifecycle)
}

func advanceArtifactBuilderToRunning(t *testing.T, lifecycle *ArtifactBuilderLifecycle, request ArtifactBuilderRequest) {
	t.Helper()
	group := testArtifactBuilderGroup(t, request, ArtifactBuilderGroupPending, "", nil)
	if err := lifecycle.ObserveCreated(group); err != nil {
		t.Fatalf("ObserveCreated() error = %v", err)
	}
	assertArtifactBuilderState(t, lifecycle, ArtifactBuilderStateCreated)
	group = testArtifactBuilderGroup(t, request, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
	if err := lifecycle.Observe(group); err != nil {
		t.Fatalf("Observe(running) error = %v", err)
	}
	assertArtifactBuilderState(t, lifecycle, ArtifactBuilderStateRunning)
	if err := lifecycle.ObserveLogs(strings.Repeat("x", ArtifactBuilderMaxLogBytes+1)); err == nil {
		t.Fatal("ObserveLogs() accepted unbounded logs")
	}
	if err := lifecycle.ObserveLogs("builder started"); err != nil {
		t.Fatalf("ObserveLogs() error = %v", err)
	}
}

func advanceArtifactBuilderToSucceeded(t *testing.T, lifecycle *ArtifactBuilderLifecycle, request ArtifactBuilderRequest) {
	t.Helper()
	group := testArtifactBuilderGroup(t, request, ArtifactBuilderGroupSucceeded, ArtifactBuilderContainerTerminated, new(int64(0)))
	if err := lifecycle.Observe(group); err != nil {
		t.Fatalf("Observe(succeeded) error = %v", err)
	}
	assertArtifactBuilderState(t, lifecycle, ArtifactBuilderStateSucceeded)
	secondGroup := group
	secondGroup.ID = "eci-builder-2"
	if err := lifecycle.Observe(secondGroup); err == nil {
		t.Fatal("Observe() accepted a second container group")
	}
}

func assertArtifactBuilderCleanup(t *testing.T, lifecycle *ArtifactBuilderLifecycle) {
	t.Helper()
	if err := lifecycle.MarkDeleteRequested(); err != nil {
		t.Fatalf("MarkDeleteRequested() error = %v", err)
	}
	if err := lifecycle.ConfirmAbsent(false); !errors.Is(err, ErrArtifactBuilderStillPresent) {
		t.Fatalf("ConfirmAbsent(false) error = %v", err)
	}
	if err := lifecycle.ConfirmAbsent(true); err != nil {
		t.Fatalf("ConfirmAbsent(true) error = %v", err)
	}
	assertArtifactBuilderState(t, lifecycle, ArtifactBuilderStateAbsent)
}

func assertArtifactBuilderState(t *testing.T, lifecycle *ArtifactBuilderLifecycle, want ArtifactBuilderState) {
	t.Helper()
	if lifecycle.State() != want {
		t.Fatalf("artifact builder state = %s, want %s", lifecycle.State(), want)
	}
}

func TestArtifactBuilderLifecycleRejectsWrongLiveIdentityAndIncompleteTerminal(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatalf("NewArtifactBuilderRequest() error = %v", err)
	}
	lifecycle, err := NewArtifactBuilderLifecycle(request)
	if err != nil {
		t.Fatalf("NewArtifactBuilderLifecycle() error = %v", err)
	}
	wrongImage := testArtifactBuilderGroup(t, request, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
	wrongImage.Image = "registry.example.com/other@sha256:" + strings.Repeat("f", 64)
	if err := lifecycle.ObserveCreated(wrongImage); err == nil {
		t.Fatal("ObserveCreated() accepted a live image identity drift")
	}
	incomplete := testArtifactBuilderGroup(t, request, ArtifactBuilderGroupSucceeded, ArtifactBuilderContainerTerminated, nil)
	if err := lifecycle.ObserveCreated(incomplete); err == nil {
		t.Fatal("ObserveCreated() accepted terminal group without exit evidence")
	}
}

func TestArtifactBuilderActorIsSeparateFromNormalRuntime(t *testing.T) {
	var actor ArtifactBuilderActor = artifactBuilderActorStub{}
	if _, err := actor.Describe(context.Background(), "eci-builder-1"); err != nil {
		t.Fatalf("artifact builder actor Describe() error = %v", err)
	}
}

type artifactBuilderActorStub struct{}

func (artifactBuilderActorStub) Create(context.Context, ArtifactBuilderRequest) (ArtifactBuilderGroup, error) {
	return ArtifactBuilderGroup{}, nil
}

func (artifactBuilderActorStub) ReconcileRequest(context.Context, ArtifactBuilderRequest) (ArtifactBuilderRequestMatch, error) {
	return ArtifactBuilderRequestMatch{}, nil
}

func (artifactBuilderActorStub) Describe(context.Context, string) (ArtifactBuilderGroup, error) {
	return ArtifactBuilderGroup{}, nil
}

func (artifactBuilderActorStub) Logs(context.Context, string, string) (string, error) {
	return "", nil
}

func (artifactBuilderActorStub) Delete(context.Context, string) error { return nil }

func (artifactBuilderActorStub) ConfirmAbsent(context.Context, string) (bool, error) {
	return true, nil
}

func testArtifactBuilderConfig() ArtifactBuilderConfig {
	return ArtifactBuilderConfig{
		Provider:            ArtifactBuilderProvider,
		RegionID:            "cn-shenzhen",
		ContainerGroupName:  "artifact-builder-1",
		ContainerName:       "portable-builder",
		CompilerPath:        "/usr/local/go/bin/go",
		Image:               "registry.example.com/remote-ci@sha256:" + strings.Repeat("a", 64),
		ImageSnapshotID:     "snap-builder-1",
		EphemeralStorageGiB: ArtifactBuilderEphemeralStorageGiB,
		CPU:                 ArtifactBuilderCPU,
		MemoryGiB:           ArtifactBuilderMemoryGiB,
		VSwitches: []cicontract.ECIVSwitch{
			{ID: "vsw-zone-a", ZoneID: "cn-test-a"},
			{ID: "vsw-zone-b", ZoneID: "cn-test-b"},
		},
		SecurityGroupID:     "sg-builder-1",
		RAMRoleName:         "AliyunECIArtifactBuilderRole",
		OSSBucket:           "builder-bucket",
		OSSInternalEndpoint: "https://oss-cn-shenzhen-internal.aliyuncs.com",
		Input: ArtifactBuilderInput{
			ArtifactPrefix:            "builder/",
			Key:                       "builder/inputs/source-baseline.bundle",
			SHA256:                    "sha256:" + strings.Repeat("b", 64),
			SizeBytes:                 4096,
			RepositoryID:              "repository-1",
			ObjectFormat:              "sha1",
			SourceCommitSHA:           strings.Repeat("c", 40),
			SourceTreeSHA:             strings.Repeat("d", 40),
			ExpectedBuilderDigest:     "sha256:" + strings.Repeat("e", 64),
			ExpectedToolchainDigest:   "sha256:" + strings.Repeat("f", 64),
			ProtocolABIDigest:         "sha256:" + strings.Repeat("1", 64),
			WorkloadManifestABIDigest: "sha256:" + strings.Repeat("2", 64),
		},
	}
}

func testArtifactBuilderGroup(t *testing.T, request ArtifactBuilderRequest, status, containerState string, exitCode *int64) ArtifactBuilderGroup {
	t.Helper()
	tags, err := artifactBuilderExpectedTags(request)
	if err != nil {
		t.Fatal(err)
	}
	group := ArtifactBuilderGroup{
		ID:                  "eci-builder-1",
		Name:                request.ContainerGroupName,
		Provider:            request.Provider,
		RegionID:            request.RegionID,
		Image:               request.Image,
		ImageSnapshotID:     request.ImageSnapshotID,
		CompilerPath:        request.CompilerPath,
		Command:             append([]string(nil), request.Command...),
		Input:               request.Input,
		EphemeralStorageGiB: request.EphemeralStorageGiB,
		CPU:                 request.CPU,
		MemoryGiB:           request.MemoryGiB,
		VSwitches:           append([]cicontract.ECIVSwitch(nil), request.VSwitches...),
		ScheduleStrategy:    request.ScheduleStrategy,
		SecurityGroupID:     request.SecurityGroupID,
		RAMRoleName:         request.RAMRoleName,
		Tags:                tags,
		Status:              status,
		Container: ArtifactBuilderContainer{
			Name:     request.ContainerName,
			Image:    request.Image,
			Command:  append([]string(nil), request.Command...),
			State:    containerState,
			ExitCode: exitCode,
		},
	}
	if exitCode != nil {
		group.Container.StartTime = time.Date(2026, time.August, 20, 1, 2, 3, 0, time.UTC)
		group.Container.FinishTime = group.Container.StartTime.Add(time.Second)
	}
	return group
}
