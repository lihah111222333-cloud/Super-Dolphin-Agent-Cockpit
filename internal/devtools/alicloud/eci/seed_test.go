package eci

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClientCreateSeedContainerGroup(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-seed"}`)}}
	client := newTestClient(t, runner)
	request := validSeedRequest()
	group, err := client.CreateSeedContainerGroup(context.Background(), request)
	if err != nil || group.ID != "eci-seed" {
		t.Fatalf("CreateSeedContainerGroup() = %#v, %v", group, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v", runner.calls)
	}
	call := runner.calls[0]
	for _, pair := range [][]string{
		{"--ClientToken", "baseline-token-2-spot"},
		{"--Cpu", "4"},
		{"--Memory", "8"},
		{"--SpotStrategy", "SpotAsPriceGo"},
		{"--SpotDuration", "1"},
		{"--Container.1.Cpu", "4"},
		{"--Container.1.Memory", "8"},
		{"--DataCacheBucket", "super-dolphin-ci"},
		{"--AutoCreateEip", "true"},
		{"--EipBandwidth", "5"},
		{"--Container.1.SecurityContext.ReadOnlyRootFilesystem", "false"},
		{"--Volume.1.Name", "input-data"},
		{"--Volume.1.FlexVolume.Driver", "alicloud/oss"},
		{"--Volume.2.Name", "output-data"},
		{"--Volume.2.FlexVolume.Driver", "alicloud/oss"},
		{"--Volume.5.HostPathVolume.Path", "/super-dolphin/ci/baselines/1"},
		{"--Container.1.VolumeMount.1.MountPath", "/input"},
		{"--Container.1.VolumeMount.1.ReadOnly", "true"},
		{"--Container.1.VolumeMount.2.MountPath", "/output"},
		{"--Container.1.VolumeMount.5.ReadOnly", "true"},
	} {
		if !containsArgumentPair(call, pair[0], pair[1]) {
			t.Fatalf("call missing %v: %#v", pair, call)
		}
	}
}

func TestClientCreateSeedUsesStableTokenAcrossClientInstances(t *testing.T) {
	var calls [][]string
	for _, generatedToken := range []string{"random-token-one", "random-token-two"} {
		runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-seed"}`)}}
		client := newTestClientWithTokens(t, runner, generatedToken)
		if _, err := client.CreateSeedContainerGroup(context.Background(), validSeedRequest()); err != nil {
			t.Fatalf("CreateSeedContainerGroup() error = %v", err)
		}
		calls = append(calls, runner.calls[0])
	}
	if !reflect.DeepEqual(calls[0], calls[1]) ||
		!containsArgumentPair(calls[0], "--ClientToken", "baseline-token-2-spot") {
		t.Fatalf("seed Create calls must keep one cross-process token: %#v", calls)
	}
}

func TestClientCreateSeedAddsBaselineLayersBeforeAnchorDataCache(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-seed"}`)}}
	client := newTestClient(t, runner)
	request := validSeedRequest()
	request.BaselineLayers = OSSVolume{
		Bucket: "source-bucket", Endpoint: "oss-cn-shenzhen-internal.aliyuncs.com",
		Path: "/baseline-layers", RoleName: "worker-role",
	}
	if _, err := client.CreateSeedContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateSeedContainerGroup() error = %v", err)
	}
	call := runner.calls[0]
	for _, pair := range [][]string{
		{"--Volume.5.Name", "baseline-layers"},
		{"--Volume.5.Type", "FlexVolume"},
		{"--Volume.5.FlexVolume.Driver", "alicloud/oss"},
		{"--Volume.6.Name", "previous-data"},
		{"--Container.1.VolumeMount.5.MountPath", "/layers"},
		{"--Container.1.VolumeMount.6.MountPath", "/previous"},
		{"--Container.1.VolumeMount.5.ReadOnly", "true"},
		{"--Container.1.VolumeMount.6.ReadOnly", "true"},
	} {
		if !containsArgumentPair(call, pair[0], pair[1]) {
			t.Fatalf("call missing %v: %#v", pair, call)
		}
	}
}

func TestClientCreateSeedReconcilesUncertainCreate(t *testing.T) {
	transient := errors.New("net/http: TLS handshake timeout")
	runner := &fakeCommandRunner{
		responses: [][]byte{[]byte(`{"ContainerGroups":[{"ContainerGroupId":"eci-seed","ContainerGroupName":"sdci-seed-2","Status":"Running"}]}`)},
		runErrors: repeatCommandErrors(transient, maxCLIAttempts),
	}
	client := newTestClient(t, runner)
	client.wait = func(context.Context, time.Duration) error { return nil }
	group, err := client.CreateSeedContainerGroup(context.Background(), validSeedRequest())
	if err != nil || group.ID != "eci-seed" || len(runner.calls) != maxCLIAttempts+1 {
		t.Fatalf("CreateSeedContainerGroup() = %#v, %v, calls = %d", group, err, len(runner.calls))
	}
	for _, call := range runner.calls[:maxCLIAttempts] {
		if !reflect.DeepEqual(call, runner.calls[0]) || !containsArgumentPair(call, "--ClientToken", "baseline-token-2-spot") {
			t.Fatalf("seed Create retries must reuse one ClientToken: %#v", runner.calls)
		}
	}
}

func TestClientCreateSeedFallsBackOnlyForSpotCapacity(t *testing.T) {
	runner := &fakeCommandRunner{
		responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-seed"}`)},
		runErrors: []error{errors.New("ErrorCode: Spot.NotMatched")},
	}
	client := newTestClient(t, runner)
	group, err := client.CreateSeedContainerGroup(context.Background(), validSeedRequest())
	if err != nil || group.ID != "eci-seed" || len(runner.calls) != 2 {
		t.Fatalf("CreateSeedContainerGroup() = %#v, %v, calls=%d", group, err, len(runner.calls))
	}
	if !containsArgumentPair(runner.calls[0], "--SpotStrategy", "SpotAsPriceGo") ||
		!containsArgumentPair(runner.calls[1], "--SpotStrategy", "NoSpot") ||
		!containsArgumentPair(runner.calls[0], "--ClientToken", "baseline-token-2-spot") ||
		!containsArgumentPair(runner.calls[1], "--ClientToken", "baseline-token-2-regular") ||
		containsArgumentPair(runner.calls[1], "--SpotDuration", "1") {
		t.Fatalf("seed calls = %#v", runner.calls)
	}
}

func TestSeedRequestFieldRegistry(t *testing.T) {
	assertStructFields(t, reflect.TypeFor[SeedRequest](), []string{
		"ContainerGroupName", "ContainerName", "ClientToken", "Resources", "Command", "Args", "AutoCreateEIP",
		"EIPBandwidth", "Environment", "Tags",
		"Input", "Output", "BaselineLayers", "Script", "DataCacheBucket", "PreviousDataCachePath",
	})
	assertStructFields(t, reflect.TypeFor[OSSVolume](), []string{
		"Bucket", "Endpoint", "Path", "RoleName",
	})
}

func TestClientCreateSeedRejectsInvalidRequest(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*SeedRequest)
	}{
		{"name", func(request *SeedRequest) { request.ContainerName = "x" }},
		{"missing client token", func(request *SeedRequest) { request.ClientToken = "" }},
		{"long client token", func(request *SeedRequest) { request.ClientToken = strings.Repeat("x", 57) }},
		{"resources", func(request *SeedRequest) { request.Resources = Resources{CPU: 8, MemoryGiB: 20} }},
		{"command", func(request *SeedRequest) { request.Command = nil }},
		{"script", func(request *SeedRequest) { request.Script = nil }},
		{"disabled EIP bandwidth", func(request *SeedRequest) { request.AutoCreateEIP = false }},
		{"EIP bandwidth", func(request *SeedRequest) { request.EIPBandwidth = 0 }},
		{"input OSS bucket", func(request *SeedRequest) { request.Input.Bucket = "" }},
		{"input OSS endpoint", func(request *SeedRequest) {
			request.Input.Endpoint = "oss-cn-shenzhen.aliyuncs.com"
		}},
		{"input OSS path", func(request *SeedRequest) { request.Input.Path = "/" }},
		{"input OSS role", func(request *SeedRequest) { request.Input.RoleName = "" }},
		{"output OSS bucket", func(request *SeedRequest) { request.Output.Bucket = "" }},
		{"output OSS endpoint", func(request *SeedRequest) {
			request.Output.Endpoint = "oss-cn-shenzhen.aliyuncs.com"
		}},
		{"output OSS path", func(request *SeedRequest) { request.Output.Path = "/" }},
		{"output OSS role", func(request *SeedRequest) { request.Output.RoleName = "" }},
		{"previous path without bucket", func(request *SeedRequest) { request.DataCacheBucket = "" }},
		{"previous bucket without path", func(request *SeedRequest) { request.PreviousDataCachePath = "" }},
		{"reserved previous bucket", func(request *SeedRequest) {
			request.DataCacheBucket = "eci-system"
		}},
		{"previous path", func(request *SeedRequest) { request.PreviousDataCachePath = "relative" }},
		{"baseline layers bucket only", func(request *SeedRequest) { request.BaselineLayers.Bucket = "source-bucket" }},
		{"baseline layers endpoint only", func(request *SeedRequest) { request.BaselineLayers.Endpoint = "oss-cn-shenzhen-internal.aliyuncs.com" }},
		{"baseline layers path only", func(request *SeedRequest) { request.BaselineLayers.Path = "/baseline-layers" }},
		{"baseline layers role only", func(request *SeedRequest) { request.BaselineLayers.RoleName = "worker-role" }},
		{"baseline layers bucket differs", func(request *SeedRequest) {
			request.BaselineLayers = request.Input
			request.BaselineLayers.Bucket = "other-bucket"
		}},
		{"baseline layers endpoint differs", func(request *SeedRequest) {
			request.BaselineLayers = request.Input
			request.BaselineLayers.Endpoint = "oss-cn-shanghai-internal.aliyuncs.com"
		}},
		{"baseline layers role differs", func(request *SeedRequest) {
			request.BaselineLayers = request.Input
			request.BaselineLayers.RoleName = "other-role"
		}},
		{"baseline layers equals input", func(request *SeedRequest) { request.BaselineLayers = request.Input }},
		{"baseline layers equals output", func(request *SeedRequest) { request.BaselineLayers = request.Output }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			client := newTestClient(t, runner)
			request := validSeedRequest()
			testCase.mutate(&request)
			if _, err := client.CreateSeedContainerGroup(context.Background(), request); err == nil {
				t.Fatal("CreateSeedContainerGroup() error = nil")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("runner calls = %#v, want none", runner.calls)
			}
		})
	}
}

func TestClientCreateSeedAllowsDisabledEIPWithoutBandwidth(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-seed"}`)}}
	client := newTestClient(t, runner)
	request := validSeedRequest()
	request.AutoCreateEIP = false
	request.EIPBandwidth = 0
	if _, err := client.CreateSeedContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateSeedContainerGroup() error = %v", err)
	}
	for _, pair := range [][]string{
		{"--AutoCreateEip", "false"},
		{"--EipBandwidth", "0"},
	} {
		if !containsArgumentPair(runner.calls[0], pair[0], pair[1]) {
			t.Fatalf("call missing %v: %#v", pair, runner.calls[0])
		}
	}
}

func validSeedRequest() SeedRequest {
	return SeedRequest{
		ContainerGroupName: "sdci-seed-2", ContainerName: "baseline-seed",
		ClientToken: "baseline-token-2",
		Resources:   Resources{CPU: 4, MemoryGiB: 8},
		Command:     []string{"/bin/sh"}, Args: []string{"/bootstrap/seed.sh"},
		AutoCreateEIP: true, EIPBandwidth: 5,
		Environment: map[string]string{"BASELINE_GENERATION": "2"},
		Tags:        map[string]string{"owner": "super-dolphin-ci"},
		Input: OSSVolume{
			Bucket: "source-bucket", Endpoint: "oss-cn-shenzhen-internal.aliyuncs.com",
			Path: "/source-bundles/2", RoleName: "worker-role",
		},
		Output: OSSVolume{
			Bucket: "source-bucket", Endpoint: "oss-cn-shenzhen-internal.aliyuncs.com",
			Path: "/baseline-artifacts/2", RoleName: "worker-role",
		},
		Script:                []byte("#!/bin/sh\nset -eu\n"),
		DataCacheBucket:       "super-dolphin-ci",
		PreviousDataCachePath: "/super-dolphin/ci/baselines/1",
	}
}

func containsArgumentPair(values []string, key string, value string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == key && values[index+1] == value {
			return true
		}
	}
	return false
}
