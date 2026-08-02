package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRemoteMaterializeConfigAcceptsNestedRequestKey(t *testing.T) {
	values := map[string]string{
		remoteWorkerRoleEnv:    "worker-role",
		remoteOSSEndpointEnv:   "oss-cn-shenzhen-internal.aliyuncs.com",
		remoteOSSBucketEnv:     "ci-bucket",
		remoteRequestKeyEnv:    "baseline-artifacts/source-deltas/job-1234/shard-00.request.json",
		remoteRequestSHA256Env: strings.Repeat("a", sha256.Size*2),
	}
	config, err := loadRemoteMaterializeConfig(func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if err != nil {
		t.Fatalf("loadRemoteMaterializeConfig() error = %v", err)
	}
	if config.RequestKey != values[remoteRequestKeyEnv] {
		t.Fatalf("remote request key = %q", config.RequestKey)
	}
}

func TestHandoffRemoteWorkRoot(t *testing.T) {
	root := t.TempDir()
	var mode os.FileMode
	var uid, gid int
	err := handoffRemoteWorkRoot(root, func(path string, value os.FileMode) error {
		if path != root {
			t.Fatalf("chmod path = %q, want %q", path, root)
		}
		mode = value
		return nil
	}, func(path string, gotUID int, gotGID int) error {
		if path != root {
			t.Fatalf("chown path = %q, want %q", path, root)
		}
		uid, gid = gotUID, gotGID
		return nil
	})
	if err != nil {
		t.Fatalf("handoffRemoteWorkRoot() error = %v", err)
	}
	if mode != 0o700 || uid != remoteExecutorUID || gid != remoteExecutorGID {
		t.Fatalf("handoff = mode %o uid %d gid %d", mode, uid, gid)
	}
}

func TestHandoffRemoteWorkRootRejectsNonEmptyDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stale"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := handoffRemoteWorkRoot(root, os.Chmod, os.Chown); err == nil {
		t.Fatal("handoffRemoteWorkRoot() unexpectedly passed")
	}
}

func TestDownloadVerifiedFileCleansFailedStagingFile(t *testing.T) {
	root := t.TempDir()
	objectPath := filepath.Join(root, "source.patch")
	expected := digestBytes([]byte("expected"))
	err := downloadVerifiedFile(context.Background(), func(context.Context, string, int64, io.Writer) (int64, error) {
		return 0, errors.New("temporary OSS failure")
	}, "source.patch", expected, 1024, objectPath)
	if err == nil || !strings.Contains(err.Error(), "temporary OSS failure") {
		t.Fatalf("downloadVerifiedFile() error = %v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed download left staging files: %v", entries)
	}
}
