//go:build darwin || linux

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
	unixSupervisorHelperEnv      = "SUPER_DOLPHIN_TEST_UNIX_LSP_SUPERVISOR"
	unixSupervisorInvalidPipeEnv = "SUPER_DOLPHIN_TEST_UNIX_LSP_SUPERVISOR_INVALID_PIPE"
)

// TestMain 让 hiddenexec 测试二进制复用生产监管入口，覆盖真实 re-exec 路径。
func TestMain(m *testing.M) {
	if handled, exitCode := RunProcessSupervisorIfRequested(os.Args); handled {
		os.Exit(exitCode)
	}
	os.Exit(m.Run())
}

func TestUnixSupervisedProcessTreeTerminatesWithoutRawPIDSignal(t *testing.T) {
	supervised, err := NewUnixSupervisedCommand("/bin/sh", "-c", "sleep 30 & wait")
	if err != nil {
		t.Fatalf("NewUnixSupervisedCommand() error = %v", err)
	}
	t.Cleanup(func() { _ = supervised.Close() })

	tree, err := supervised.StartProcessTree()
	if err != nil {
		t.Fatalf("StartProcessTree() error = %v", err)
	}
	cmd := supervised.Command()
	waitDone := startUnixSupervisorWait(cmd)

	root, err := tree.Identity()
	if err != nil {
		t.Fatalf("Identity() error = %v", err)
	}
	waitForUnixSupervisorDescendant(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tree.Terminate(); err != nil {
		t.Fatalf("Terminate() error = %v, want stable supervisor cleanup", err)
	}
	requireUnixSupervisorWait(t, cmd, waitDone, "supervised root")
	if err := tree.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := tree.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	assertUnixSupervisorGroupGone(t, root)
}

func TestUnixProcessSupervisorControlEOFReapsOwnedGroup(t *testing.T) {
	if os.Getenv(unixSupervisorHelperEnv) == "1" {
		os.Exit(runUnixProcessSupervisor([]string{
			os.Args[0],
			processSupervisorModeArgument,
			string(os.PathSeparator),
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

	cmd := exec.Command(os.Args[0], "-test.run=^TestUnixProcessSupervisorControlEOFReapsOwnedGroup$")
	cmd.Env = append(os.Environ(), unixSupervisorHelperEnv+"=1")
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
	waitForUnixSupervisorDescendant(t, root)
	if err := controlWrite.Close(); err != nil {
		t.Fatalf("close supervisor control writer: %v", err)
	}

	waitDone := startUnixSupervisorWait(cmd)
	requireUnixSupervisorWait(t, cmd, waitDone, "supervisor helper after parent control EOF")
	assertUnixSupervisorGroupGone(t, root)
}

func TestUnixProcessSupervisorRejectsNonPipeControl(t *testing.T) {
	if os.Getenv(unixSupervisorInvalidPipeEnv) == "1" {
		os.Exit(runUnixProcessSupervisor([]string{
			os.Args[0],
			processSupervisorModeArgument,
			string(os.PathSeparator),
			"/usr/bin/true",
		}))
	}

	control, err := os.CreateTemp(t.TempDir(), "not-a-control-pipe-*")
	if err != nil {
		t.Fatalf("create non-pipe control fixture: %v", err)
	}
	defer control.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestUnixProcessSupervisorRejectsNonPipeControl$")
	cmd.Env = append(os.Environ(), unixSupervisorInvalidPipeEnv+"=1")
	cmd.ExtraFiles = []*os.File{control}
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("non-pipe supervisor control result = %v, want exit code 1", err)
	}
}

func TestUnixProcessSupervisorOrphanPredicate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		ppid    int
		cwd     string
		statErr error
		want    bool
	}{
		{name: "live parent", ppid: 42, cwd: "/workspace", statErr: os.ErrNotExist, want: false},
		{name: "orphan valid cwd", ppid: 1, cwd: "/workspace", want: false},
		{name: "orphan relative cwd", ppid: 1, cwd: "workspace", statErr: os.ErrNotExist, want: false},
		{name: "orphan deleted cwd", ppid: 1, cwd: "/workspace", statErr: os.ErrNotExist, want: true},
		{name: "orphan uncertain stat", ppid: 1, cwd: "/workspace", statErr: os.ErrPermission, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := unixProcessSupervisorOrphaned(
				func() int { return tc.ppid },
				tc.cwd,
				func(string) (os.FileInfo, error) { return nil, tc.statErr },
			)
			if got != tc.want {
				t.Fatalf("unixProcessSupervisorOrphaned() = %v, want %v", got, tc.want)
			}
		})
	}
}

func startUnixSupervisorWait(cmd *exec.Cmd) <-chan error {
	waitDone := make(chan error, 1)
	safego.Go(context.Background(), nil, "mcp-lsp.hiddenexec.test.unix-supervisor-wait", func(context.Context) {
		waitDone <- cmd.Wait()
	})
	return waitDone
}

func requireUnixSupervisorWait(t *testing.T, cmd *exec.Cmd, waitDone <-chan error, label string) {
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

func waitForUnixSupervisorDescendant(t *testing.T, root ProcessIdentity) {
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

func assertUnixSupervisorGroupGone(t *testing.T, root ProcessIdentity) {
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
