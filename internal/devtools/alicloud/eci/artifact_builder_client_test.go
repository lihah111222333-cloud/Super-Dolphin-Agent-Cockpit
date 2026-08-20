package eci

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestContainerGroup_FieldRegistry(t *testing.T) {
	assertStructFields(t, reflect.TypeFor[ContainerGroup](), []string{"ID", "Name", "RegionID", "VSwitchID", "SecurityGroupID", "RAMRoleName", "EphemeralStorageGiB", "ImageSnapshotID", "CPU", "MemoryGiB", "Status", "CreationTime", "SucceededTime", "FailedTime", "Containers", "InitContainers", "Tags", "Events"})
	assertStructFields(t, reflect.TypeFor[ContainerGroupTag](), []string{"Key", "Value"})
	assertStructFields(t, reflect.TypeFor[ContainerStatus](), []string{"Name", "Image", "Command", "Args", "CurrentState"})
	assertStructFields(t, reflect.TypeFor[ContainerState](), []string{"State", "StartTime", "FinishTime", "ExitCode", "Reason", "Message"})
	assertStructFields(t, reflect.TypeFor[ContainerGroupEvent](), []string{"Type", "Reason", "Message", "Count", "LastTimestamp"})
}

func TestArtifactBuilderClientCreateUsesDedicatedArgvAndLiveDescribe(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatal(err)
	}
	tags, err := artifactBuilderExpectedTags(request)
	if err != nil {
		t.Fatal(err)
	}
	live := artifactBuilderLiveResponse(t, request, tags, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-builder-1"}`), live}}
	client := newTestClient(t, runner)
	actor := NewArtifactBuilderActor(client)
	group, err := actor.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if group.ID != "eci-builder-1" || group.Image != request.Image || group.Input != request.Input || group.Container.Args[0] != ArtifactBuilderSubcommand {
		t.Fatalf("Create() group = %#v", group)
	}
	assertArtifactBuilderCreateArgv(t, runner.calls[0], request)
}

// TestArtifactBuilderClientAcceptsProviderOmittedSnapshotAndEphemeral 验证 Describe 未回显 create-only 字段时仍核对其余 live 身份。
func TestArtifactBuilderClientAcceptsProviderOmittedSnapshotAndEphemeral(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatal(err)
	}
	tags, err := artifactBuilderExpectedTags(request)
	if err != nil {
		t.Fatal(err)
	}
	live := artifactBuilderLiveGroup(t, request, tags, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
	live.EphemeralStorageGiB = 0
	live.ImageSnapshotID = ""
	live.Containers[0].Command = nil
	payload, err := json.Marshal(struct {
		ContainerGroups []ContainerGroup `json:"ContainerGroups"`
	}{[]ContainerGroup{live}})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-builder-1"}`), payload}}
	actor := NewArtifactBuilderActor(newTestClient(t, runner))
	if _, err := actor.Create(context.Background(), request); err != nil {
		t.Fatalf("Create() rejected provider-omitted create-only fields: %v", err)
	}
}

func assertArtifactBuilderCreateArgv(t *testing.T, createCall []string, request ArtifactBuilderRequest) {
	t.Helper()
	for _, pair := range [][]string{
		{"--VSwitchId", "vsw-zone-a,vsw-zone-b"},
		{"--ScheduleStrategy", ArtifactBuilderScheduleStrategy},
		{"--SecurityGroupId", request.SecurityGroupID},
		{"--RamRoleName", request.RAMRoleName},
		{"--Cpu", "4"},
		{"--Memory", "8"},
		{"--EphemeralStorage", "100"},
		{"--ImageSnapshotId", request.ImageSnapshotID},
		{"--Container.1.Command.1", ArtifactBuilderCommand},
		{"--Container.1.Arg.1", ArtifactBuilderSubcommand},
		{"--Container.1.Arg.3", request.CompilerPath},
		{"--Container.1.Arg.5", ArtifactBuilderProvider},
		{"--Container.1.Arg.39", request.RAMRoleName},
		{"--Container.1.Arg.11", request.Input.ArtifactPrefix},
		{"--Tag.1.Key", ArtifactBuilderInputDigestTag},
		{"--Tag.2.Key", ArtifactBuilderRequestDigestTag},
	} {
		if !containsArgumentPair(createCall, pair[0], pair[1]) {
			t.Fatalf("Create argv missing %v: %#v", pair, createCall)
		}
	}
	for _, flag := range []string{"--Container.1.Arg.2=--compiler-path", "--Container.1.Arg.4=--execution-provider", "--Container.1.Arg.6=--artifact-kind", "--Container.1.Arg.10=--artifact-prefix", "--Container.1.Arg.38=--ram-role-name"} {
		if !slices.Contains(createCall, flag) {
			t.Fatalf("Create argv missing flag %q: %#v", flag, createCall)
		}
	}
	for _, forbidden := range []string{"--InitContainer.1.Name", "--Volume.1.Name", "--Container.1.EnvironmentVar", "workload", "--observed-builder-digest", "--observed-toolchain-digest"} {
		if slices.Contains(createCall, forbidden) {
			t.Fatalf("builder argv contains normal runtime field %q: %#v", forbidden, createCall)
		}
	}
}

func TestArtifactBuilderClientRejectsLiveFieldCommandAndTagDrift(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ContainerGroup)
	}{
		{"vSwitch", func(group *ContainerGroup) { group.VSwitchID = "vsw-foreign" }},
		{"security group", func(group *ContainerGroup) { group.SecurityGroupID = "sg-foreign" }},
		{"RAM role", func(group *ContainerGroup) { group.RAMRoleName = "role-foreign" }},
		{"ephemeral storage", func(group *ContainerGroup) { group.EphemeralStorageGiB = 20 }},
		{"snapshot", func(group *ContainerGroup) { group.ImageSnapshotID = "snap-foreign" }},
		{"container command", func(group *ContainerGroup) { group.Containers[0].Command[0] = "/wrong" }},
		{"container args", func(group *ContainerGroup) { group.Containers[0].Args[1] = "_wrong" }},
		{"tag", func(group *ContainerGroup) { group.Tags[0].Value = "sha256:" + strings.Repeat("9", 64) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
			if err != nil {
				t.Fatal(err)
			}
			tags, err := artifactBuilderExpectedTags(request)
			if err != nil {
				t.Fatal(err)
			}
			live := artifactBuilderLiveGroup(t, request, tags, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil)
			testCase.mutate(&live)
			payload, err := json.Marshal(struct {
				ContainerGroups []ContainerGroup `json:"ContainerGroups"`
			}{[]ContainerGroup{live}})
			if err != nil {
				t.Fatal(err)
			}
			runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-builder-1"}`), payload}}
			actor := NewArtifactBuilderActor(newTestClient(t, runner))
			if _, err := actor.Create(context.Background(), request); err == nil || !errors.Is(err, ErrArtifactBuilderIdentityDrift) {
				t.Fatalf("Create() error = %v, want identity drift", err)
			}
		})
	}
}

func TestArtifactBuilderClientReconcilesUncertainCreateAndCleansUp(t *testing.T) {
	request, err := NewArtifactBuilderRequest(testArtifactBuilderConfig())
	if err != nil {
		t.Fatal(err)
	}
	tags, err := artifactBuilderExpectedTags(request)
	if err != nil {
		t.Fatal(err)
	}
	transient := errors.New("net/http: TLS handshake timeout")
	runner := &fakeCommandRunner{
		runErrors: repeatCommandErrors(transient, maxCLIAttempts),
		responses: [][]byte{
			artifactBuilderLiveResponse(t, request, tags, ArtifactBuilderGroupRunning, ArtifactBuilderContainerRunning, nil),
			[]byte(`{"Content":"builder output"}`),
			[]byte(`{"RequestId":"deleted"}`),
			[]byte(`{"ContainerGroups":[]}`),
		},
	}
	actor := NewArtifactBuilderActor(newTestClient(t, runner))
	group, err := actor.Create(context.Background(), request)
	if err != nil || group.ID != "eci-builder-1" {
		t.Fatalf("uncertain Create() = %#v, %v", group, err)
	}
	assertArtifactBuilderClientCleanup(t, actor, runner, group.ID, request)
}

func assertArtifactBuilderClientCleanup(t *testing.T, actor ArtifactBuilderActor, runner *fakeCommandRunner, groupID string, request ArtifactBuilderRequest) {
	t.Helper()
	if _, err := actor.Logs(context.Background(), groupID, request.ContainerName); err != nil {
		t.Fatalf("Logs() error = %v", err)
	}
	if err := actor.Delete(context.Background(), groupID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	absent, err := actor.ConfirmAbsent(context.Background(), groupID)
	if err != nil || !absent {
		t.Fatalf("ConfirmAbsent() = %v, %v", absent, err)
	}
	if len(runner.calls) != maxCLIAttempts+4 {
		t.Fatalf("runner calls = %d, want %d", len(runner.calls), maxCLIAttempts+4)
	}
	if !containsArgumentPair(runner.calls[maxCLIAttempts], "--ContainerGroupName", request.ContainerGroupName) {
		t.Fatalf("reconcile call = %#v", runner.calls[maxCLIAttempts])
	}
}

func artifactBuilderLiveResponse(t *testing.T, request ArtifactBuilderRequest, tags map[string]string, status, state string, exitCode *int64) []byte {
	t.Helper()
	group := artifactBuilderLiveGroup(t, request, tags, status, state, exitCode)
	payload, err := json.Marshal(struct {
		ContainerGroups []ContainerGroup `json:"ContainerGroups"`
	}{[]ContainerGroup{group}})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func artifactBuilderLiveGroup(t *testing.T, request ArtifactBuilderRequest, tags map[string]string, status, state string, exitCode *int64) ContainerGroup {
	t.Helper()
	commandLine, err := request.CommandLine()
	if err != nil {
		t.Fatal(err)
	}
	containerArgs := append([]string{request.Command[1]}, commandLine[2:]...)
	containerTags := make([]ContainerGroupTag, 0, len(tags))
	for _, key := range []string{ArtifactBuilderInputDigestTag, ArtifactBuilderRequestDigestTag} {
		containerTags = append(containerTags, ContainerGroupTag{Key: key, Value: tags[key]})
	}
	return ContainerGroup{
		ID: "eci-builder-1", Name: request.ContainerGroupName, RegionID: request.RegionID, VSwitchID: "vsw-zone-a",
		SecurityGroupID: request.SecurityGroupID, RAMRoleName: request.RAMRoleName, EphemeralStorageGiB: request.EphemeralStorageGiB,
		ImageSnapshotID: request.ImageSnapshotID, CPU: request.CPU, MemoryGiB: request.MemoryGiB, Status: status,
		Containers: []ContainerStatus{{Name: request.ContainerName, Image: request.Image, Command: []string{request.Command[0]}, Args: containerArgs, CurrentState: ContainerState{State: state, ExitCode: exitCode}}},
		Tags:       containerTags,
	}
}
