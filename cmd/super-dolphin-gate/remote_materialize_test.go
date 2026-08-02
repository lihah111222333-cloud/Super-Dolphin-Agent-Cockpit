package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
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

func TestVerifyRemoteMaterializedGateCLICompileClosureRejectsTransitiveSourceDrift(t *testing.T) {
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runRemoteMaterializeGit(t, repository, "init")
	runRemoteMaterializeGit(t, repository, "config", "user.email", "test@example.invalid")
	runRemoteMaterializeGit(t, repository, "config", "user.name", "Remote Materialize Test")
	for name, source := range map[string]string{
		"go.mod":                         "module example.invalid/gate\n\ngo 1.24.0\n",
		"go.sum":                         "example.invalid/dependency v1.0.0 h1:abc\n",
		"build/gate/toolchain.lock":      "go=1.24.0\n",
		"cmd/super-dolphin-gate/main.go": "package main\n\nimport \"example.invalid/gate/internal/dep\"\n\nfunc main() { dep.Run() }\n",
		"internal/dep/dep.go":            "package dep\n\nfunc Run() { println(\"base\") }\n",
	} {
		filePath := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filePath, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runRemoteMaterializeGit(t, repository, "add", ".")
	runRemoteMaterializeGit(t, repository, "commit", "-m", "base")
	baseTree := strings.TrimSpace(runRemoteMaterializeGit(t, repository, "rev-parse", "HEAD^{tree}"))
	sourceDigest, toolchainDigest, _, err := remoteci.LoadGateCLICompileClosure(context.Background(), repository, baseTree)
	if err != nil {
		t.Fatalf("load base compile closure: %v", err)
	}
	request := remoteci.ShardRequest{SourceTreeSHA: baseTree, CandidateGateSourceSHA256: sourceDigest, CandidateGateToolchainSHA256: toolchainDigest}
	if err := verifyRemoteMaterializedGateCLICompileClosure(context.Background(), repository, request); err != nil {
		t.Fatalf("verify base compile closure: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "internal/dep/dep.go"), []byte("package dep\n\nfunc Run() { println(\"changed\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRemoteMaterializeGit(t, repository, "add", ".")
	runRemoteMaterializeGit(t, repository, "commit", "-m", "imported source drift")
	request.SourceTreeSHA = strings.TrimSpace(runRemoteMaterializeGit(t, repository, "rev-parse", "HEAD^{tree}"))
	if err := verifyRemoteMaterializedGateCLICompileClosure(context.Background(), repository, request); err == nil || !strings.Contains(err.Error(), "does not match shard request") {
		t.Fatalf("transitive source drift error = %v", err)
	}
}

func runRemoteMaterializeGit(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
