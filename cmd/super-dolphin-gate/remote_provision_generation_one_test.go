package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestParseRemoteGenerationOneProvisionOptionsRequiresExplicitReceipt 覆盖首代命令的显式配置和 authority 约束。
func TestParseRemoteGenerationOneProvisionOptionsRequiresExplicitReceipt(t *testing.T) {
	if _, err := parseRemoteGenerationOneProvisionOptions([]string{"--config", "/tmp/remote-ci.json"}); err == nil {
		t.Fatal("generation-one provision without receipt unexpectedly passed")
	}
	options, err := parseRemoteGenerationOneProvisionOptions([]string{"--config", "relative/remote-ci.json", "--receipt", "/tmp/provision.json"})
	if err != nil {
		t.Fatalf("parse generation-one provision options: %v", err)
	}
	if !filepath.IsAbs(options.ConfigPath) || options.LedgerPath != strings.TrimSuffix(options.ConfigPath, filepath.Ext(options.ConfigPath))+".baseline-state.sqlite" {
		t.Fatalf("generation-one options did not normalize one authority: %#v", options)
	}
	if _, err := parseRemoteGenerationOneProvisionOptions([]string{"--config", "/tmp/remote-ci.json", "--receipt", "/tmp/provision.json", "unexpected"}); err == nil {
		t.Fatal("unexpected positional argument was accepted")
	}
}

// TestValidateGenerationOneLiveImageCacheBindsExactImages 覆盖实时镜像集合与回执的精确绑定。
func TestValidateGenerationOneLiveImageCacheBindsExactImages(t *testing.T) {
	image := "ghcr.io/example/runtime@sha256:" + strings.Repeat("a", 64)
	extra := "ghcr.io/example/helper@sha256:" + strings.Repeat("b", 64)
	receipt := cicontract.GenerationOneProvisionReceipt{
		ImageCacheID: "imc-generation-one", ImageCacheSnapshotID: "snapshot-generation-one",
		ImageCacheName: "generation-one", ImageCacheStatus: "Ready", Image: image,
		ImageCacheImages: []string{extra, image},
	}
	live := eci.ImageCache{
		ID: receipt.ImageCacheID, SnapshotID: receipt.ImageCacheSnapshotID, Name: receipt.ImageCacheName,
		Status: "Ready", Progress: "100%", Images: []string{image, extra},
	}
	if err := validateGenerationOneLiveImageCache(live, receipt); err != nil {
		t.Fatalf("validate exact live ImageCache images: %v", err)
	}
	for name, mutate := range map[string]func(*eci.ImageCache){
		"missing image": func(cache *eci.ImageCache) { cache.Images = []string{image} },
		"extra image": func(cache *eci.ImageCache) {
			cache.Images = append(cache.Images, "ghcr.io/example/other@sha256:"+strings.Repeat("c", 64))
		},
		"status": func(cache *eci.ImageCache) { cache.Status = "Creating" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := live
			changed.Images = append([]string(nil), live.Images...)
			mutate(&changed)
			if err := validateGenerationOneLiveImageCache(changed, receipt); err == nil {
				t.Fatalf("live ImageCache %s drift unexpectedly passed", name)
			}
		})
	}
}

// TestReadGenerationOneProvisionReceiptRejectsUnboundedInput 覆盖回执文件边界和普通文件要求。
func TestReadGenerationOneProvisionReceiptRejectsUnboundedInput(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readGenerationOneProvisionReceipt(directory); err == nil {
		t.Fatal("directory receipt path unexpectedly passed")
	}
	empty := filepath.Join(root, "empty.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGenerationOneProvisionReceipt(empty); err == nil {
		t.Fatal("empty receipt unexpectedly passed")
	}
}
