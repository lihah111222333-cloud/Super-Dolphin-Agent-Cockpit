//go:build !windows

package gate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestRunConfiguredCommandTerminatesWritingGrandchildBeforeReturn(t *testing.T) {
	root := durableCommandWitnessRoot(t)
	outputPath := filepath.Join(root, "cache", "writes")
	initialSize := seedCommandWitness(t, outputPath)
	script := strings.Join([]string{
		"set -eu",
		"mkdir -p " + shellQuote(filepath.Dir(outputPath)),
		"printf x >> " + shellQuote(outputPath),
		"sh -c 'trap \"\" TERM; while :; do printf x >> \"$1\"; done' sh " + shellQuote(outputPath) + " &",
		"wait $!",
	}, "\n")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", "-c", script)
	configureCommandCancellation(command)
	var commandRun errgroup.Group
	commandRun.Go(func() error { return runConfiguredCommand(command) })
	cancelAfterCommandWitness(t, cancel, outputPath, initialSize, &commandRun)
	err := commandRun.Wait()
	assertCancelledCommand(t, ctx, err)
	assertCommandWitnessStopped(t, root, outputPath)
}

// seedCommandWitness 预先创建可跨越进程启动延迟的 witness 文件。
func seedCommandWitness(t *testing.T, outputPath string) int64 {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		t.Fatalf("create cancelled command cache: %v", err)
	}
	if err := os.WriteFile(outputPath, []byte("seed"), 0o600); err != nil {
		t.Fatalf("seed cancelled command witness: %v", err)
	}
	return mustFileSize(t, outputPath)
}

// cancelAfterCommandWitness 仅在确认子进程已写入 witness 后取消命令。
func cancelAfterCommandWitness(t *testing.T, cancel context.CancelFunc, outputPath string, initialSize int64, commandRun *errgroup.Group) {
	t.Helper()
	if !awaitCommandWitness(outputPath, initialSize+1, 5*time.Second) {
		cancel()
		t.Fatalf("cancelled command did not establish a durable witness: %v", commandRun.Wait())
	}
	cancel()
}

// assertCancelledCommand 保留取消失败与进程组终止的双重断言。
func assertCancelledCommand(t *testing.T, ctx context.Context, err error) {
	t.Helper()
	if err == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("run configured command error = %v context = %v, want cancelled failure", err, ctx.Err())
	}
	if strings.Contains(err.Error(), "did not terminate") {
		t.Fatalf("run configured command retained an executable process group: %v", err)
	}
}

// assertCommandWitnessStopped 确认返回后孤儿进程不再写入并保留目录清理回归。
func assertCommandWitnessStopped(t *testing.T, root, outputPath string) {
	t.Helper()
	before := mustFileSize(t, outputPath)
	if before == 0 {
		t.Fatal("cancelled command did not establish a durable witness")
	}
	time.Sleep(50 * time.Millisecond)
	if after := mustFileSize(t, outputPath); after != before {
		t.Fatalf("grandchild continued writing after return: before=%d after=%d", before, after)
	}
	if err := os.RemoveAll(filepath.Join(root, "cache")); err != nil {
		t.Fatalf("remove cancelled command cache: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "cache"), 0o700); err != nil {
		t.Fatalf("reuse isolated cache path: %v", err)
	}
}

// durableCommandWitnessRoot 在生产短临时根之外创建可由测试生命周期管理的 witness 根。
func durableCommandWitnessRoot(t *testing.T) string {
	t.Helper()
	managed := discoverDurableCommandTempRoots()
	parent := durableCommandWitnessParent(t, managed)
	assertDurableCommandWitnessOutsideManagedRoots(t, parent, managed.roots, "parent")
	root := createDurableCommandWitnessRoot(t, parent)
	assertDurableCommandWitnessOutsideManagedRoots(t, root, managed.roots, "root")
	return root
}

// durableCommandWitnessParent 选择生产短临时根之外的父目录，避免清理删除 witness。
func durableCommandWitnessParent(t *testing.T, managed durableCommandTempRootSet) string {
	t.Helper()
	return absoluteDurableCommandWitnessParent(t, managed.parent)
}

type durableCommandTempRootSet struct {
	roots  []string
	parent string
}

func discoverDurableCommandTempRoots() durableCommandTempRootSet {
	managed := durableCommandTempRootSet{roots: make([]string, 0, 2)}
	for _, key := range []string{"TMPDIR", "GOTMPDIR"} {
		configured, parent, ok := durableCommandTempRoot(key)
		if !ok {
			continue
		}
		managed.roots = append(managed.roots, configured)
		if managed.parent == "" {
			managed.parent = parent
		}
	}
	return managed
}

func durableCommandTempRoot(key string) (string, string, bool) {
	configured := strings.TrimSpace(os.Getenv(key))
	if configured == "" {
		return "", "", false
	}
	configured = filepath.Clean(configured)
	if strings.HasPrefix(filepath.Base(configured), "sd-b-") {
		return configured, filepath.Dir(configured), true
	}
	configuredParent := filepath.Dir(configured)
	if !strings.HasPrefix(filepath.Base(configuredParent), "sd-b-") {
		return "", "", false
	}
	return configured, filepath.Dir(configuredParent), true
}

func absoluteDurableCommandWitnessParent(t *testing.T, parent string) string {
	t.Helper()
	if parent == "" {
		parent = os.TempDir()
	}
	absolute, err := filepath.Abs(filepath.Clean(parent))
	if err != nil || !filepath.IsAbs(absolute) {
		t.Fatalf("durable command witness parent must be absolute: %q", absolute)
	}
	return absolute
}

func assertDurableCommandWitnessOutsideManagedRoots(t *testing.T, path string, managedRoots []string, label string) {
	t.Helper()
	for _, managedRoot := range managedRoots {
		if path == managedRoot || pathContains(managedRoot, path) {
			t.Fatalf("durable command witness %s %q is inside managed command temp root %q", label, path, managedRoot)
		}
	}
}

func createDurableCommandWitnessRoot(t *testing.T, parent string) string {
	t.Helper()
	root, err := os.MkdirTemp(parent, "super-dolphin-command-witness-")
	if err != nil {
		t.Fatalf("create durable command witness root under %q: %v", parent, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove durable command witness root %q: %v", root, err)
		}
	})
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("secure durable command witness root %q: %v", root, err)
	}
	return root
}

// awaitCommandWitness 等待命令实际写入预先创建的 witness 文件。
func awaitCommandWitness(path string, minimumSize int64, timeout time.Duration) bool {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		info, err := os.Stat(path)
		if err == nil && info.Size() >= minimumSize {
			return true
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			return false
		}
	}
}

func TestRunConfiguredCommandPreservesExitErrorIdentity(t *testing.T) {
	command := exec.CommandContext(context.Background(), "/bin/sh", "-c", "exit 1")
	configureCommandCancellation(command)
	err := runConfiguredCommand(command)
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("run configured command error = %T %v, want direct exit code 1", err, err)
	}
	var matched *exec.ExitError
	if !errors.As(err, &matched) || matched.ExitCode() != 1 {
		t.Fatalf("errors.As exit error = %v, want exit code 1", err)
	}
}

func mustFileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
