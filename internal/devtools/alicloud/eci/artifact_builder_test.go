package eci

import (
	"context"
	"errors"
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
}

func assertArtifactBuilderCommand(t *testing.T, request ArtifactBuilderRequest) {
	t.Helper()
	command, err := request.CommandLine()
	if err != nil {
		t.Fatalf("CommandLine() error = %v", err)
	}
	if len(command) == 0 || command[0] != ArtifactBuilderCommand {
		t.Fatalf("builder command = %#v", command)
	}
	if slices.Contains(command, "go") || slices.Contains(command, "test") || slices.Contains(command, "workload") {
		t.Fatalf("builder command unexpectedly contains workload execution: %#v", command)
	}
	for _, value := range []string{testArtifactBuilderConfig().Input.Key, testArtifactBuilderConfig().Input.SHA256, testArtifactBuilderConfig().Input.SourceCommitSHA, testArtifactBuilderConfig().Input.SourceTreeSHA, testArtifactBuilderConfig().Input.BuilderDigest, testArtifactBuilderConfig().Input.ToolchainDigest} {
		if !slices.Contains(command, value) {
			t.Errorf("builder command does not bind input value %q: %#v", value, command)
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
		{name: "credential in input key", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "objects/token=password" }},
		{name: "signed URL", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "https://oss.example/bundle?signature=secret" }},
		{name: "absolute input path", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "/tmp/source.bundle" }},
		{name: "path traversal", mutate: func(config *ArtifactBuilderConfig) { config.Input.Key = "objects/../source.bundle" }},
		{name: "secret role value", mutate: func(config *ArtifactBuilderConfig) { config.RAMRoleName = "role/password=secret" }},
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
	group := testArtifactBuilderGroup(request, ArtifactBuilderGroupPending, "", nil)
	if err := lifecycle.ObserveCreated(group); err != nil {
		t.Fatalf("ObserveCreated() error = %v", err)
	}
	assertArtifactBuilderState(t, lifecycle, ArtifactBuilderStateCreated)
	group = testArtifactBuilderGroup(request, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
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
	group := testArtifactBuilderGroup(request, ArtifactBuilderGroupSucceeded, ArtifactBuilderContainerTerminated, new(int64(0)))
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
	wrongImage := testArtifactBuilderGroup(request, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
	wrongImage.Image = "registry.example.com/other@sha256:" + strings.Repeat("f", 64)
	if err := lifecycle.ObserveCreated(wrongImage); err == nil {
		t.Fatal("ObserveCreated() accepted a live image identity drift")
	}
	incomplete := testArtifactBuilderGroup(request, ArtifactBuilderGroupSucceeded, ArtifactBuilderContainerTerminated, nil)
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
		Image:               "registry.example.com/remote-ci@sha256:" + strings.Repeat("a", 64),
		ImageSnapshotID:     "snap-builder-1",
		EphemeralStorageGiB: ArtifactBuilderEphemeralStorageGiB,
		CPU:                 ArtifactBuilderCPU,
		MemoryGiB:           ArtifactBuilderMemoryGiB,
		VSwitches: []cicontract.ECIVSwitch{
			{ID: "vsw-zone-a", ZoneID: "cn-test-a"},
			{ID: "vsw-zone-b", ZoneID: "cn-test-b"},
		},
		SecurityGroupID: "sg-builder-1",
		RAMRoleName:     "AliyunECIArtifactBuilderRole",
		Input: ArtifactBuilderInput{
			Key:             "objects/source-baseline.bundle",
			SHA256:          "sha256:" + strings.Repeat("b", 64),
			SizeBytes:       4096,
			RepositoryID:    "repository-1",
			ObjectFormat:    "sha1",
			SourceCommitSHA: strings.Repeat("c", 40),
			SourceTreeSHA:   strings.Repeat("d", 40),
			BuilderDigest:   "sha256:" + strings.Repeat("e", 64),
			ToolchainDigest: "sha256:" + strings.Repeat("f", 64),
		},
	}
}

func testArtifactBuilderGroup(request ArtifactBuilderRequest, status, containerState string, exitCode *int64) ArtifactBuilderGroup {
	group := ArtifactBuilderGroup{
		ID:              "eci-builder-1",
		Name:            request.ContainerGroupName,
		Provider:        request.Provider,
		RegionID:        request.RegionID,
		Image:           request.Image,
		ImageSnapshotID: request.ImageSnapshotID,
		Status:          status,
		Container: ArtifactBuilderContainer{
			Name:     request.ContainerName,
			Image:    request.Image,
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
