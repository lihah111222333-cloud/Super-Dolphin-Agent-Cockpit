//go:build darwin

package hiddenexec

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

const (
	darwinSupervisorHelperEnv      = "SUPER_DOLPHIN_TEST_DARWIN_LSP_SUPERVISOR"
	darwinSupervisorInvalidPipeEnv = "SUPER_DOLPHIN_TEST_DARWIN_LSP_SUPERVISOR_INVALID_PIPE"
)

// TestMain 让 hiddenexec 测试二进制复用生产监管入口，覆盖真实 re-exec 路径。
func TestMain(m *testing.M) {
	if handled, exitCode := RunProcessSupervisorIfRequested(os.Args); handled {
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

func TestDarwinSupervisedProcessTreeTerminatesWithoutRawPIDSignal(t *testing.T) {
	supervised, err := NewDarwinSupervisedCommand("/bin/sh", "-c", "sleep 30 & wait")
	if err != nil {
		t.Fatalf("NewDarwinSupervisedCommand() error = %v", err)
	}
	t.Cleanup(func() { _ = supervised.Close() })

	tree, err := supervised.StartProcessTree()
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	cmd := supervised.Command()
	waitDone := startDarwinSupervisorWait(cmd)

	root, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	waitForDarwinSupervisorDescendant(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tree.Terminate(); err != nil {
		t.Fatalf("Terminate() error = %v, want stable supervisor cleanup", err)
	}
	requireDarwinSupervisorWait(t, cmd, waitDone, "supervised root")
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := tree.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	assertDarwinSupervisorGroupGone(t, root)
}

func TestDarwinProcessSupervisorControlEOFReapsOwnedGroup(t *testing.T) {
	if os.Getenv(darwinSupervisorHelperEnv) == "1" {
		os.Exit(runDarwinProcessSupervisor([]string{
			os.Args[0],
			processSupervisorModeArgument,
			"/bin/sh",
			"-c",
			"sleep 30 & wait",
		}))
	}

	controlRead, controlWrite, err := os.Pipe()
	if err != nil {
		t.Fatalf("create supervisor control pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = controlRead.Close()
		_ = controlWrite.Close()
	})

	cmd := exec.Command(os.Args[0], "-test.run=^TestDarwinProcessSupervisorControlEOFReapsOwnedGroup$")
	cmd.Env = append(os.Environ(), darwinSupervisorHelperEnv+"=1")
	cmd.ExtraFiles = []*os.File{controlRead}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start supervisor helper: %v", err)
	}
	if err := controlRead.Close(); err != nil {
		t.Fatalf("close parent copy of supervisor control reader: %v", err)
	}

	root, err := captureProcessIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("capture supervisor root identity: %v", err)
	}
	waitForDarwinSupervisorDescendant(t, root)
	if err := controlWrite.Close(); err != nil {
		t.Fatalf("close supervisor control writer: %v", err)
	}

	waitDone := startDarwinSupervisorWait(cmd)
	requireDarwinSupervisorWait(t, cmd, waitDone, "supervisor helper after parent control EOF")
	assertDarwinSupervisorGroupGone(t, root)
}

func TestDarwinProcessSupervisorRejectsNonPipeControl(t *testing.T) {
	if os.Getenv(darwinSupervisorInvalidPipeEnv) == "1" {
		os.Exit(runDarwinProcessSupervisor([]string{
			os.Args[0],
			processSupervisorModeArgument,
			"/usr/bin/true",
		}))
	}

	control, err := os.CreateTemp(t.TempDir(), "not-a-control-pipe-*")
	if err != nil {
		t.Fatalf("create non-pipe control fixture: %v", err)
	}
	defer control.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDarwinProcessSupervisorRejectsNonPipeControl$")
	cmd.Env = append(os.Environ(), darwinSupervisorInvalidPipeEnv+"=1")
	cmd.ExtraFiles = []*os.File{control}
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("non-pipe supervisor control result = %v, want exit code 1", err)
	}
}

func TestDarwinProcessSupervisorOrphanPredicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ppid    int
		cwdErr  error
		statErr error
		want    bool
	}{
		{name: "live parent", ppid: 42, cwdErr: os.ErrNotExist, want: false},
		{name: "orphan valid cwd", ppid: 1, want: false},
		{name: "orphan deleted cwd from getwd", ppid: 1, cwdErr: os.ErrNotExist, want: true},
		{name: "orphan deleted cwd from stat", ppid: 1, statErr: os.ErrNotExist, want: true},
		{name: "orphan uncertain stat", ppid: 1, statErr: os.ErrPermission, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := darwinProcessSupervisorOrphaned(
				func() int { return tc.ppid },
				func() (string, error) { return "/workspace", tc.cwdErr },
				func(string) (os.FileInfo, error) { return nil, tc.statErr },
			)
			if got != tc.want {
				t.Fatalf("darwinProcessSupervisorOrphaned() = %v, want %v", got, tc.want)
			}
		})
	}
}

func startDarwinSupervisorWait(cmd *exec.Cmd) <-chan error {
	waitDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.test.darwin-supervisor-wait", func(context.Context) {
		waitDone <- cmd.Wait()
	})
	return waitDone
}

func requireDarwinSupervisorWait(t *testing.T, cmd *exec.Cmd, waitDone <-chan error, label string) {
	t.Helper()
	select {
	case waitErr := <-waitDone:
		var exitErr *exec.ExitError
		if waitErr != nil && !errors.As(waitErr, &exitErr) {
			t.Fatalf("wait %s: %v", label, waitErr)
		}
	case <-time.After(5 * time.Second):
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		t.Fatalf("%s did not exit within 5s", label)
	}
}

func waitForDarwinSupervisorDescendant(t *testing.T, root ProcessIdentity) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		table, err := processTable()
		if err != nil {
			t.Fatalf("read process table: %v", err)
		}
		for _, member := range table {
			if member.PID != root.PID && sameProcessTreeGroup(member, root) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("supervisor did not start an owned descendant within 3s")
}

func assertDarwinSupervisorGroupGone(t *testing.T, root ProcessIdentity) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		table, err := processTable()
		if err != nil {
			t.Fatalf("read process table after supervisor exit: %v", err)
		}
		remaining := 0
		for _, member := range table {
			if sameProcessTreeGroup(member, root) {
				remaining++
			}
		}
		if remaining == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("supervisor-owned process group still has members after control EOF")
}
