package localci

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type dockerRunnerStub struct {
	calls        [][]string
	wait         error
	create       error
	start        error
	waitDeadline time.Duration
	createOutput string
	kill         error
	remove       error
}

func (stub *dockerRunnerStub) Run(ctx context.Context, args ...string) (string, error) {
	stub.calls = append(stub.calls, append([]string(nil), args...))
	switch args[0] {
	case "create":
		if stub.createOutput != "" {
			return stub.createOutput, stub.create
		}
		return testContainerID + "\n", stub.create
	case "start":
		return "", stub.start
	case "wait":
		if deadline, exists := ctx.Deadline(); exists {
			stub.waitDeadline = time.Until(deadline)
		}
		return "0\n", stub.wait
	case "kill":
		return "", stub.kill
	case "rm":
		return "", stub.remove
	default:
		return "", nil
	}
}

const testContainerID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

var testGateCommand = []string{"/usr/local/bin/super-dolphin-gate-executor", "run"}

func TestDockerExecutorAppliesIsolationAndResourceContract(t *testing.T) {
	runner := &dockerRunnerStub{}
	seccomp, trustedRoot, sourceDir := dockerFixture(t)
	executor, err := newDockerExecutor(runner, seccomp, trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := executor.Run(context.Background(), containerRequest{
		Image:     "registry.local/gate@" + digest("1"),
		SourceDir: sourceDir,
		Command:   []string{"/usr/local/bin/super-dolphin-gate-executor", "run"},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if evidence.ContainerID != testContainerID || !evidence.Removed || evidence.SeccompDigest == "" {
		t.Fatalf("lifecycle evidence = %#v", evidence)
	}
	wantCreate := []string{
		"create", "--cpus=4", "--memory=8g", "--pids-limit=512", "--storage-opt=size=10G", "--read-only", "--user=65532:65532",
		"--cap-drop=ALL", "--security-opt=no-new-privileges", "--security-opt=seccomp=" + seccomp,
		"--network=none", "--mount=type=bind,src=" + sourceDir + ",dst=/workspace/source,readonly",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=2147483648",
		"--tmpfs=/workspace/work:rw,exec,nosuid,nodev,size=5368709120,uid=65532,gid=65532,mode=0700",
		"--workdir=/workspace/work", "--env=HOME=/workspace/work/home", "--env=TMPDIR=/workspace/work/tmp",
		"--env=GOCACHE=/workspace/work/go-cache", "--env=GOMODCACHE=/workspace/work/go-mod-cache",
		"--env=npm_config_cache=/workspace/work/npm-cache", "--env=XDG_CACHE_HOME=/workspace/work/xdg-cache",
		"--log-driver=local", "--log-opt=max-size=10m", "--log-opt=max-file=3",
		"--entrypoint=/usr/local/bin/super-dolphin-gate-executor", "registry.local/gate@" + digest("1"), "run",
	}
	if !reflect.DeepEqual(runner.calls[0], wantCreate) {
		t.Fatalf("docker create args = %#v\nwant %#v", runner.calls[0], wantCreate)
	}
	wantLifecycle := [][]string{{"start", testContainerID}, {"wait", testContainerID}, {"rm", "--force", testContainerID}}
	if !reflect.DeepEqual(runner.calls[1:], wantLifecycle) {
		t.Fatalf("lifecycle calls = %#v, want %#v", runner.calls[1:], wantLifecycle)
	}
}

func TestDockerExecutorKillsAndRemovesOnWaitFailure(t *testing.T) {
	runner := &dockerRunnerStub{wait: context.DeadlineExceeded}
	seccomp, trustedRoot, sourceDir := dockerFixture(t)
	executor, err := newDockerExecutor(runner, seccomp, trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := executor.Run(context.Background(), containerRequest{Image: "repo@" + digest("1"), SourceDir: sourceDir, Command: testGateCommand})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
	if !evidence.Killed || !evidence.Removed {
		t.Fatalf("timeout evidence = %#v", evidence)
	}
	wantTail := [][]string{{"kill", testContainerID}, {"rm", "--force", testContainerID}}
	if !reflect.DeepEqual(runner.calls[len(runner.calls)-2:], wantTail) {
		t.Fatalf("cleanup calls = %#v, want %#v", runner.calls, wantTail)
	}
}

func TestDockerExecutorTreatsNonzeroExitAsFailure(t *testing.T) {
	seccomp, trustedRoot, sourceDir := dockerFixture(t)
	runnerWithExit := &dockerExitRunnerStub{dockerRunnerStub: dockerRunnerStub{}}
	executor, err := newDockerExecutor(runnerWithExit, seccomp, trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := executor.Run(context.Background(), containerRequest{Image: "repo@" + digest("1"), SourceDir: sourceDir, Command: testGateCommand})
	if err == nil {
		t.Fatal("Run() accepted nonzero container exit code")
	}
	if evidence.ExitCode != 17 || !evidence.Removed {
		t.Fatalf("nonzero evidence = %#v", evidence)
	}
}

type dockerExitRunnerStub struct{ dockerRunnerStub }

func (stub *dockerExitRunnerStub) Run(ctx context.Context, args ...string) (string, error) {
	output, err := stub.dockerRunnerStub.Run(ctx, args...)
	if args[0] == "wait" {
		return "17\n", err
	}
	return output, err
}

func TestDockerExecutorCleanupBoundaries(t *testing.T) {
	for _, test := range []struct {
		name     string
		runner   *dockerRunnerStub
		wantTail [][]string
	}{
		{name: "create failure", runner: &dockerRunnerStub{create: errors.New("create failed")}, wantTail: [][]string{{"create"}}},
		{name: "start failure", runner: &dockerRunnerStub{start: errors.New("start failed")}, wantTail: [][]string{{"start", testContainerID}, {"rm", "--force", testContainerID}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			seccomp, trustedRoot, sourceDir := dockerFixture(t)
			executor, err := newDockerExecutor(test.runner, seccomp, trustedRoot)
			if err != nil {
				t.Fatal(err)
			}
			evidence, err := executor.Run(context.Background(), containerRequest{Image: "repo@" + digest("1"), SourceDir: sourceDir, Command: testGateCommand})
			if err == nil {
				t.Fatal("Run() accepted lifecycle failure")
			}
			if test.name == "create failure" {
				if len(test.runner.calls) != 1 || test.runner.calls[0][0] != "create" {
					t.Fatalf("create failure calls = %#v", test.runner.calls)
				}
				return
			}
			if !evidence.Removed {
				t.Fatalf("start failure evidence = %#v", evidence)
			}
			if !reflect.DeepEqual(test.runner.calls[len(test.runner.calls)-2:], test.wantTail) {
				t.Fatalf("cleanup calls = %#v, want tail %#v", test.runner.calls, test.wantTail)
			}
		})
	}
}

func TestExecutionTimeoutSeparatesNormalAndRelease(t *testing.T) {
	if got := executionTimeout(false); got != 10*time.Minute {
		t.Fatalf("normal timeout = %v", got)
	}
	if got := executionTimeout(true); got != 30*time.Minute {
		t.Fatalf("release timeout = %v", got)
	}
}

func TestDockerExecutorPassesProfileDeadlineToWait(t *testing.T) {
	for _, release := range []bool{false, true} {
		runner := &dockerRunnerStub{}
		seccomp, trustedRoot, sourceDir := dockerFixture(t)
		executor, err := newDockerExecutor(runner, seccomp, trustedRoot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Run(context.Background(), containerRequest{Image: "repo@" + digest("1"), SourceDir: sourceDir, Command: testGateCommand, Release: release}); err != nil {
			t.Fatal(err)
		}
		want := executionTimeout(release)
		if runner.waitDeadline < want-time.Second || runner.waitDeadline > want {
			t.Fatalf("release=%v wait deadline = %v, want near %v", release, runner.waitDeadline, want)
		}
	}
}

func TestDockerExecutorRejectsMountInjectionAndSourceEscape(t *testing.T) {
	runner := &dockerRunnerStub{}
	seccomp, trustedRoot, sourceDir := dockerFixture(t)
	executor, err := newDockerExecutor(runner, seccomp, trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	for _, source := range []string{sourceDir + ",dst=/host", outside} {
		if _, err := executor.Run(context.Background(), containerRequest{Image: "repo@" + digest("1"), SourceDir: source, Command: testGateCommand}); err == nil {
			t.Fatalf("Run() accepted source %q", source)
		}
	}
}

func TestDockerExecutorCleansVerifiableIDFromMalformedCreateOutput(t *testing.T) {
	runner := &dockerRunnerStub{createOutput: testContainerID + " unexpected\n"}
	seccomp, trustedRoot, sourceDir := dockerFixture(t)
	executor, err := newDockerExecutor(runner, seccomp, trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := executor.Run(context.Background(), containerRequest{Image: "repo@" + digest("1"), SourceDir: sourceDir, Command: testGateCommand})
	if err == nil || !evidence.Removed || evidence.ContainerID != testContainerID {
		t.Fatalf("malformed create evidence = %#v, err = %v", evidence, err)
	}
	want := []string{"rm", "--force", testContainerID}
	if !reflect.DeepEqual(runner.calls[len(runner.calls)-1], want) {
		t.Fatalf("cleanup call = %#v, want %#v", runner.calls, want)
	}
}

func TestDockerExecutorReportsKillAndRemovalFailures(t *testing.T) {
	runner := &dockerRunnerStub{
		wait:   context.DeadlineExceeded,
		kill:   errors.New("kill failed"),
		remove: errors.New("remove failed"),
	}
	seccomp, trustedRoot, sourceDir := dockerFixture(t)
	executor, err := newDockerExecutor(runner, seccomp, trustedRoot)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := executor.Run(context.Background(), containerRequest{Image: "repo@" + digest("1"), SourceDir: sourceDir, Command: testGateCommand})
	if err == nil || !strings.Contains(err.Error(), "wait for gate container") || !strings.Contains(err.Error(), "kill gate container") || !strings.Contains(err.Error(), "remove gate container") {
		t.Fatalf("joined lifecycle error = %v", err)
	}
	if evidence.Killed || evidence.Removed {
		t.Fatalf("failed cleanup evidence = %#v", evidence)
	}
}

func dockerFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	seccomp := filepath.Join(root, "seccomp.json")
	if err := os.WriteFile(seccomp, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	return seccomp, root, source
}
