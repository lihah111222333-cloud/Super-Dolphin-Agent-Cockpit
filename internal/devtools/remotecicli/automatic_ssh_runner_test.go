package remotecicli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociexec"
)

func TestAutomaticSSHRunnerExecutesLinuxAndWindowsBatchCommands(t *testing.T) {
	ssh, argvLog := automaticRunnerFakeSSH(t)
	for _, platform := range []heteroci.Platform{heteroci.PlatformLinuxAMD64, heteroci.PlatformWindowsAMD64} {
		t.Run(string(platform), func(t *testing.T) {
			t.Setenv("AUTOMATIC_RUNNER_ARGV_LOG", argvLog+strings.ReplaceAll(string(platform), "/", "-"))
			var logs bytes.Buffer
			config := automaticRunnerTestConfig(ssh, platform, &logs)
			runner, err := NewAutomaticSSHRunner(config)
			if err != nil {
				t.Fatal(err)
			}
			command, err := heterociexec.EncodeCommand(platform, []string{"/opt/super-dolphin/bin/super-dolphin-heteroci-node", "batch", "--request-base64", "token"})
			if err != nil {
				t.Fatal(err)
			}
			outcome, err := runner.Run(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.ExitCode != 0 || len(outcome.Outcomes) != 1 || outcome.Outcomes[0].State != heteroci.LeaseStateSucceeded {
				t.Fatalf("outcome = %#v", outcome)
			}
			if !strings.Contains(logs.String(), `"schema_version":"heteroci-node-batch-result/v1"`) {
				t.Fatalf("observable logs = %q", logs.String())
			}
		})
	}
}

func automaticRunnerFakeSSH(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "ssh")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" >"${AUTOMATIC_RUNNER_ARGV_LOG:?}"
printf '%s\n' 'worker log'
printf '%s\n' '{"schema_version":"heteroci-node-batch-result/v1","authority":"LOCAL_NON_AUTHORITATIVE","status":"NOT_VERIFIED","outcome":"success","outcomes":[{"unit_id":"unit-a","status":"success","exit_code":0}]}'
printf '%s\n' '{"status":"NOT_VERIFIED"}'
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path, filepath.Join(root, "argv-")
}

func automaticRunnerTestConfig(ssh string, platform heteroci.Platform, logs *bytes.Buffer) AutomaticSSHRunnerConfig {
	config := AutomaticSSHRunnerConfig{SSHExecutable: ssh, Host: "node", Platform: platform, Timeout: 5 * time.Second, WaitDelay: time.Second, LogWriter: logs}
	if platform == heteroci.PlatformLinuxAMD64 {
		config.Linux = &AutomaticLinuxRunnerConfig{
			RunAsUser: "ci-runner", DockerGroup: "docker", RunnerExecutable: "/runner", RepoPath: "/repo", SharedRoot: "/shared", CacheRoot: "/cache",
			RepositoryID: "repo", NodeID: "node", ObjectFormat: "sha1", SourceCommit: strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40),
			ToolchainIdentity: "sha256:" + strings.Repeat("c", 64), ModuleIdentity: "sha256:" + strings.Repeat("d", 64), WorkloadIdentity: "sha256:" + strings.Repeat("e", 64),
			DockerImage: "sha256:" + strings.Repeat("f", 64), WorkerBinary: "/worker", WorkerIdentity: "sha256:" + strings.Repeat("1", 64),
		}
	} else {
		config.Windows = &AutomaticWindowsRunnerConfig{
			RunnerExecutable: "C:/runner.ps1", RepoPath: "C:/repo", ObjectStoreRoot: "C:/repo/.git", WorktreeRoot: "C:/repo", CacheRoot: "C:/cache",
			RepositoryID: "repo", NodeID: "node", SourceCommit: strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40),
			ToolchainIdentity: "sha256:" + strings.Repeat("c", 64), ModuleIdentity: "sha256:" + strings.Repeat("d", 64), WorkloadIdentity: "sha256:" + strings.Repeat("e", 64),
		}
	}
	return config
}
