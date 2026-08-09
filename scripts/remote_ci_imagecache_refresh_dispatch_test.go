package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRemoteCIImageCacheRefreshDispatcherForwardsExactCommitAndDeduplicates 验证 dispatcher 只转发精确提交且维护锁可去重。
func TestRemoteCIImageCacheRefreshDispatcherForwardsExactCommitAndDeduplicates(t *testing.T) {
	repository, commit := newRefreshDispatchRepository(t)
	binDir := t.TempDir()
	writeRefreshDispatchFile(t, filepath.Join(binDir, "shlock"), []byte("#!/usr/bin/env bash\nset -eu\nwhile (($#)); do case \"$1\" in -p) pid=$2; shift 2 ;; -f) lock=$2; shift 2 ;; *) exit 92 ;; esac; done\n[[ ! -e \"$lock\" ]] || exit 1\nprintf '%s\\n' \"$pid\" >\"$lock\"\n"), 0o700)
	config := filepath.Join(t.TempDir(), "remote.json")
	writeRefreshDispatchFile(t, config, []byte("{}\n"), 0o600)
	capture := filepath.Join(t.TempDir(), "arguments")
	refresh := filepath.Join(t.TempDir(), "refresh.sh")
	writeRefreshDispatchFile(t, refresh, []byte("#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" >\"$REFRESH_CAPTURE\"\n"), 0o700)
	dispatcher, err := filepath.Abs("dispatch_remote_ci_imagecache_refresh.sh")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(dispatcher, "--repository", repository, "--source-ref", commit, "--config", config, "--refresh-script", refresh, "--if-older-than-hours", "24")
	command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "REFRESH_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("dispatcher failed: %v: %s", err, output)
	}
	content, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{"--config", config, "--source-ref", commit, "--if-older-than-hours", "24", ""}, "\n")
	if string(content) != want {
		t.Fatalf("forwarded arguments = %q, want %q", content, want)
	}
	os.Remove(capture)
	commonDir := refreshGitOutput(t, repository, "rev-parse", "--path-format=absolute", "--git-common-dir")
	lock := filepath.Join(commonDir, "super-dolphin", "imagecache-refresh", "refresh.lock")
	writeRefreshDispatchFile(t, lock, []byte("fixture\n"), 0o600)
	t.Cleanup(func() { _ = os.Remove(lock) })
	command = exec.Command(dispatcher, "--repository", repository, "--source-ref", commit, "--config", config, "--refresh-script", refresh)
	command.Env = append(os.Environ(), "PATH="+binDir+":"+os.Getenv("PATH"), "REFRESH_CAPTURE="+capture)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("deduplicated dispatcher failed: %v: %s", err, output)
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatalf("refresh ran while maintenance lock was held: %v", err)
	}
}

func newRefreshDispatchRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := t.TempDir()
	refreshGit(t, repository, "init", "-q")
	refreshGit(t, repository, "config", "user.name", "Refresh Test")
	refreshGit(t, repository, "config", "user.email", "refresh@example.invalid")
	writeRefreshDispatchFile(t, filepath.Join(repository, "tracked.txt"), []byte("fixture\n"), 0o600)
	refreshGit(t, repository, "add", "tracked.txt")
	refreshGit(t, repository, "commit", "-qm", "fixture")
	return repository, refreshGitOutput(t, repository, "rev-parse", "HEAD")
}

func refreshGit(t *testing.T, repository string, arguments ...string) {
	t.Helper()
	if output, err := exec.Command("git", append([]string{"-C", repository}, arguments...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func refreshGitOutput(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", repository}, arguments...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func writeRefreshDispatchFile(t *testing.T, path string, content []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, content, mode); err != nil {
		t.Fatal(err)
	}
}

// TestRemoteCIImageCacheRefreshSchedulingReceiptIsStrictAndFresh 验证 24h 内只读回执跳过且未知字段 fail-fast。
func TestRemoteCIImageCacheRefreshSchedulingReceiptIsStrictAndFresh(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("刷新调度控制面只在 macOS Git hook 主机运行")
	}
	receipt := refreshSchedulingReceipt(time.Now().Unix())
	output, err := runRefreshSchedulingFixture(t, receipt)
	if err != nil {
		t.Fatalf("fresh scheduling receipt failed: %v: %s", err, output)
	}
	if !strings.Contains(string(output), `"action": "skip_fresh_imagecache"`) {
		t.Fatalf("fresh scheduling output = %s", output)
	}
	receipt["unexpected_field"] = "must fail"
	output, err = runRefreshSchedulingFixture(t, receipt)
	if err == nil || !strings.Contains(string(output), "refresh receipt is invalid") {
		t.Fatalf("unknown scheduling field did not fail strictly: %v: %s", err, output)
	}
}

func runRefreshSchedulingFixture(t *testing.T, receipt map[string]any) ([]byte, error) {
	t.Helper()
	root := t.TempDir()
	receiptPath := filepath.Join(root, "receipt.json")
	configPath := filepath.Join(root, "config.json")
	aliyunPath := filepath.Join(root, "aliyun")
	writeRefreshJSON(t, receiptPath, receipt)
	writeRefreshJSON(t, configPath, map[string]any{
		"credential_profile": "fixture", "region_id": "cn-test", "security_group_id": "sg-fixture", "worker_role_name": "fixture-role",
		"oss": map[string]any{"bucket": "fixture", "endpoint": "https://oss.example.invalid", "internal_endpoint": "https://oss-internal.example.invalid", "source_prefix": "source-bundles/"},
	})
	writeRefreshDispatchFile(t, aliyunPath, []byte("#!/usr/bin/env bash\nset -eu\n[[ \"$1 $2\" == 'oss stat' ]] && exit 0\nif [[ \"$1 $2\" == 'oss cp' ]]; then cp \"$REFRESH_FIXTURE_RECEIPT\" \"$4\"; exit 0; fi\nexit 93\n"), 0o700)
	writeRefreshDispatchFile(t, filepath.Join(root, "shasum"), []byte("#!/usr/bin/env bash\nexit 94\n"), 0o700)
	script, err := filepath.Abs("refresh_remote_ci_imagecache.sh")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(script, "--config", configPath, "--if-older-than-hours", "24")
	command.Env = append(os.Environ(), "PATH="+root+":"+os.Getenv("PATH"), "REFRESH_FIXTURE_RECEIPT="+receiptPath)
	return command.CombinedOutput()
}

func refreshSchedulingReceipt(refreshedAt int64) map[string]any {
	digest := strings.Repeat("a", 64)
	return map[string]any{
		"schema_version": "remote-ci-imagecache-refresh-receipt/v2", "authoritative": false, "action": "candidate_created_not_accepted", "execution_provider": "aliyun-eci/v1", "region_id": "cn-shenzhen",
		"source_commit": strings.Repeat("b", 40), "source_tree": strings.Repeat("c", 40), "base_image": "registry.invalid/base@sha256:" + digest, "base_snapshot_id": "s-base",
		"oci_base_image": "registry.invalid/base@sha256:" + digest, "image": "registry.invalid/new@sha256:" + digest, "image_digest": "sha256:" + digest,
		"image_cache_id": "imc-new", "image_cache_name": "sdci-refresh", "image_cache_snapshot_id": "s-new", "image_cache_status": "Ready", "gate_binary_sha256": "sha256:" + digest,
		"builder_compile_seconds": 2, "verification_compile_seconds": 1, "retention_days": 7, "refreshed_at_unix_sec": refreshedAt,
		"refreshed_at_utc": time.Unix(refreshedAt, 0).UTC().Format("2006-01-02T15:04:05Z"), "mutates_sqlite": false,
	}
}

func writeRefreshJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeRefreshDispatchFile(t, path, append(content, '\n'), 0o600)
}
