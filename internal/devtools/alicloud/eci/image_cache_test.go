package eci

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestClient_ImageCacheLifecycleUsesExplicitImmutableCache(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{
		[]byte(`{"ImageCacheId":"imc-created","RequestId":"request-created"}`),
		[]byte(`{"ImageCaches":[{"ImageCacheId":"imc-created","SnapshotId":"s-created","ImageCacheName":"cache-1","Status":"Creating","Images":["registry.example/remote-builder@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"]}]}`),
		[]byte(`{"ImageCaches":[{"ImageCacheId":"imc-created","SnapshotId":"s-created","ImageCacheName":"cache-1","Status":"Ready","Progress":"100%","Images":["registry.example/remote-builder@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"],"Events":[{"Type":"Normal","Reason":"Ready","Message":"cache ready"}]}]}`),
		[]byte(`{"RequestId":"request-deleted"}`),
	}}
	client := newTestClient(t, runner)
	request := validImageCacheCreateRequest()
	cache, err := client.CreateImageCache(context.Background(), request)
	if err != nil || cache.ID != "imc-created" {
		t.Fatalf("CreateImageCache() = %#v, %v", cache, err)
	}
	ready, err := client.WaitImageCacheReady(context.Background(), cache.ID)
	if err != nil || ready.SnapshotID != "s-created" || ready.Status != "Ready" || !reflect.DeepEqual(ready.Images, request.Images) {
		t.Fatalf("WaitImageCacheReady() = %#v, %v", ready, err)
	}
	if err := client.DeleteImageCache(context.Background(), cache.ID); err != nil {
		t.Fatalf("DeleteImageCache() error = %v", err)
	}
	createCall := runner.calls[0]
	for _, pair := range [][]string{
		{"--AutoMatchImageCache", "true"}, {"--Flash", "true"}, {"--Image.1", request.Images[0]},
		{"--ClientToken", testClientToken},
	} {
		if !containsArgumentPair(createCall, pair[0], pair[1]) {
			t.Fatalf("CreateImageCache call missing %v: %#v", pair, createCall)
		}
	}
	if got := runner.calls[1]; !containsArgumentPair(got, "--ImageCacheId", "imc-created") {
		t.Fatalf("DescribeImageCache call = %#v", got)
	}
}

func TestClient_CreateContainerGroupPinsCacheAndRejectsAutomaticMatching(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	if _, err := client.CreateContainerGroup(context.Background(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	call := runner.calls[0]
	if !containsArgumentPair(call, "--ImageSnapshotId", "imc-test-cache") ||
		containsArgumentPair(call, "--AutoMatchImageCache", "true") {
		t.Fatalf("CreateContainerGroup cache binding = %#v", call)
	}
	for _, pair := range [][]string{
		{"--Container.1.Image", request.MainImage},
		{"--InitContainer.1.Image", request.InitImage},
		{"--Container.1.ImagePullPolicy", "IfNotPresent"},
		{"--InitContainer.1.ImagePullPolicy", "IfNotPresent"},
	} {
		if !containsArgumentPair(call, pair[0], pair[1]) {
			t.Fatalf("CreateContainerGroup call missing %v: %#v", pair, call)
		}
	}
	request = validCreateRequest()
	request.ImageCacheID = ""
	if _, err := client.CreateContainerGroup(context.Background(), request); err == nil || len(runner.calls) != 1 {
		t.Fatalf("CreateContainerGroup without cache error = %v, calls = %#v", err, runner.calls)
	}
}

func TestClient_WaitImageCacheReadyReturnsFailureEvents(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ImageCaches":[{"ImageCacheId":"imc-failed","ImageCacheName":"cache-1","Status":"Failed","Events":[{"Type":"Warning","Reason":"ImagePullBackOff","Message":"digest pull failed"}]}]}`)}}
	cache, err := newTestClient(t, runner).WaitImageCacheReady(context.Background(), "imc-failed")
	if err == nil || cache.Status != "Failed" || !strings.Contains(err.Error(), "ImagePullBackOff") {
		t.Fatalf("WaitImageCacheReady() = %#v, %v", cache, err)
	}
}

func TestClient_CreateImageCacheRejectsInvalidInputBeforeCLI(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ImageCacheCreateRequest)
	}{
		{"mutable image", func(request *ImageCacheCreateRequest) { request.Images = []string{"registry.example/repo:latest"} }},
		{"empty images", func(request *ImageCacheCreateRequest) { request.Images = nil }},
		{"invalid cache size", func(request *ImageCacheCreateRequest) { request.ImageCacheSize = 19 }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &fakeCommandRunner{}
			request := validImageCacheCreateRequest()
			testCase.mutate(&request)
			if _, err := newTestClient(t, runner).CreateImageCache(context.Background(), request); err == nil || len(runner.calls) != 0 {
				t.Fatalf("CreateImageCache() error = %v, calls = %#v", err, runner.calls)
			}
		})
	}
}

func TestClient_CreateImageCacheRetriesWithOneClientToken(t *testing.T) {
	runner := &fakeCommandRunner{
		responses: [][]byte{[]byte(`{"ImageCacheId":"imc-created"}`)},
		runErrors: repeatCommandErrors(errors.New("net/http: TLS handshake timeout"), maxCLIAttempts-1),
	}
	client := newTestClient(t, runner)
	client.wait = func(context.Context, time.Duration) error { return nil }
	if _, err := client.CreateImageCache(context.Background(), validImageCacheCreateRequest()); err != nil {
		t.Fatalf("CreateImageCache() error = %v", err)
	}
	for _, call := range runner.calls {
		if !containsArgumentPair(call, "--ClientToken", testClientToken) || !reflect.DeepEqual(call, runner.calls[0]) {
			t.Fatalf("CreateImageCache retries = %#v", runner.calls)
		}
	}
}

func validImageCacheCreateRequest() ImageCacheCreateRequest {
	return ImageCacheCreateRequest{
		ImageCacheName: "cache-1",
		Images:         []string{testMainImageDigest},
		ImageCacheSize: 20,
		RetentionDays:  7,
		Tags:           map[string]string{"workload": "test"},
	}
}
