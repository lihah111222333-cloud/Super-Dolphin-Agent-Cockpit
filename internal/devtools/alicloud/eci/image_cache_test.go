package eci

import "testing"

func TestClient_CreateContainerGroupPinsCacheAndRejectsAutomaticMatching(t *testing.T) {
	runner := &fakeCommandRunner{responses: [][]byte{[]byte(`{"ContainerGroupId":"eci-created"}`)}}
	client := newTestClient(t, runner)
	request := validCreateRequest()
	if _, err := client.CreateContainerGroup(t.Context(), request); err != nil {
		t.Fatalf("CreateContainerGroup() error = %v", err)
	}
	call := runner.calls[0]
	if !containsArgumentPair(call, "--ImageSnapshotId", request.ImageCacheSnapshotID) ||
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
	request.ImageCacheSnapshotID = ""
	if _, err := client.CreateContainerGroup(t.Context(), request); err == nil || len(runner.calls) != 1 {
		t.Fatalf("CreateContainerGroup without cache error = %v, calls = %#v", err, runner.calls)
	}
}

// TestValidateReadyImageCacheRejectsUnboundIdentity 覆盖首代 live cache 的 Ready、snapshot 和 immutable image 门禁。
func TestValidateReadyImageCacheRejectsUnboundIdentity(t *testing.T) {
	valid := ImageCache{ID: "imc-ready", SnapshotID: "snapshot-ready", Name: "cache-1", Status: "Ready", Images: []string{testMainImageDigest}}
	tests := []struct {
		name   string
		mutate func(*ImageCache, *string)
	}{
		{"not ready", func(cache *ImageCache, _ *string) { cache.Status = "Preparing" }},
		{"missing snapshot", func(cache *ImageCache, _ *string) { cache.SnapshotID = "" }},
		{"mutable image", func(cache *ImageCache, expected *string) {
			cache.Images = []string{"registry.example/runtime:latest"}
			*expected = cache.Images[0]
		}},
		{"duplicate expected image", func(cache *ImageCache, _ *string) { cache.Images = []string{testMainImageDigest, testMainImageDigest} }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cache, expected := valid, testMainImageDigest
			testCase.mutate(&cache, &expected)
			if err := ValidateReadyImageCache(cache, valid.ID, valid.Name, expected); err == nil {
				t.Fatal("invalid Ready cache was accepted")
			}
		})
	}
	if err := ValidateReadyImageCache(valid, valid.ID, valid.Name, valid.Images[0]); err != nil {
		t.Fatalf("valid Ready cache rejected: %v", err)
	}
}
