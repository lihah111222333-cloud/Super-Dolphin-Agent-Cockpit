package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestVerifyRemoteOCIProjectCache(t *testing.T) {
	if err := verifyRemoteOCIProjectCache(remoteci.ShardRequest{}); err == nil {
		t.Fatal("verifyRemoteOCIProjectCache() accepted a missing OCI project cache")
	}
	cachePath := filepath.Join(t.TempDir(), "go-build")
	if err := os.Mkdir(cachePath, 0o555); err != nil {
		t.Fatal(err)
	}
	request := validRemoteOCIProjectCacheRequest()
	if err := verifyRemoteOCIProjectCacheAtPath(request, cachePath); err != nil {
		t.Fatalf("verifyRemoteOCIProjectCacheAtPath() error = %v", err)
	}
	for name, prepare := range map[string]func(string, *remoteci.ShardRequest){
		"missing":  func(path string, _ *remoteci.ShardRequest) { _ = os.Remove(path) },
		"writable": func(path string, _ *remoteci.ShardRequest) { _ = os.Chmod(path, 0o755) },
		"symlink": func(path string, _ *remoteci.ShardRequest) {
			_ = os.Remove(path)
			_ = os.Symlink(filepath.Dir(path), path)
		},
		"identity drift": func(_ string, request *remoteci.ShardRequest) {
			request.BaselineToolchainDigest = "sha256:" + strings.Repeat("f", 64)
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "go-build")
			if err := os.Mkdir(path, 0o555); err != nil {
				t.Fatal(err)
			}
			invalid := validRemoteOCIProjectCacheRequest()
			prepare(path, &invalid)
			if err := verifyRemoteOCIProjectCacheAtPath(invalid, path); err == nil {
				t.Fatal("verifyRemoteOCIProjectCacheAtPath() error = nil")
			}
		})
	}
}

func validRemoteOCIProjectCacheRequest() remoteci.ShardRequest {
	tree := strings.Repeat("a", 40)
	toolchain := "sha256:" + strings.Repeat("b", 64)
	image := "registry.example/runtime@sha256:" + strings.Repeat("c", 64)
	return remoteci.ShardRequest{
		RunnerBaseTree: tree, BaselineToolchainDigest: toolchain, BaselineRuntimeImage: image,
		OCIProjectCache: &remoteci.BaselineOCIProjectCache{Image: image, ContentManifestSHA256: "sha256:" + strings.Repeat("d", 64), MainTree: tree, ToolchainDigest: toolchain, Platform: "linux/amd64", CachePath: remoteci.OCIProjectGoBuildCachePath},
	}
}
