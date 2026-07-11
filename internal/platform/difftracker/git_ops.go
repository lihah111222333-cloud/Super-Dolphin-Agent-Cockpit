package difftracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

var ErrNotGitRepository = errors.New("difftracker: not a git repository")

// git 命令执行上限；index.lock 竞争只做短重试，避免工具调用长时间卡住。
const (
	gitCommandTimeout = 4 * time.Second
	gitRetryAttempts  = 2
)

func findGitRoot(ctx context.Context, dir string) (string, error) {
	base, err := gitBaseDir(dir)
	if err != nil {
		return "", err
	}
	output, err := execGitCommand(ctx, base, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := filepath.Clean(strings.TrimSpace(string(output)))
	if root == "" {
		return "", fmt.Errorf("difftracker: git root is empty")
	}
	return root, nil
}

func listDirtyFiles(ctx context.Context, root string) ([]string, error) {
	repoRoot, err := findGitRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	modified, err := execGitCommand(ctx, repoRoot, "diff", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := execGitCommand(ctx, repoRoot, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	files := append(splitNonEmptyLines(string(modified)), splitNonEmptyLines(string(untracked))...)
	return uniqueSorted(files), nil
}

func readHEADContent(ctx context.Context, root, relPath string) ([]byte, error) {
	repoRoot, err := findGitRoot(ctx, root)
	if err != nil {
		return nil, err
	}
	path := normalizeDiffPath(relPath)
	if path == "" {
		return nil, os.ErrNotExist
	}
	content, err := execGitCommand(ctx, repoRoot, "show", "HEAD:"+filepath.ToSlash(path))
	if err == nil {
		return content, nil
	}
	if isMissingHEADPath(err) {
		return nil, os.ErrNotExist
	}
	return nil, err
}

// execGitCommand 执行 git 子命令并只对 index.lock 竞争做有界重试。
// 其他错误立即返回，避免在非 git 目录或权限错误上静默等待。
func execGitCommand(ctx context.Context, dir string, args ...string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= gitRetryAttempts; attempt++ {
		output, err := execGitCommandOnce(ctx, dir, args...)
		if err == nil {
			return output, nil
		}
		lastErr = err
		errMsg := err.Error()
		// archguard:ignore priority_ssa_error_string -- Git reports index.lock contention only through stderr text.
		if !strings.Contains(errMsg, "index.lock") || attempt == gitRetryAttempts {
			return nil, err
		}
		if waitErr := sleepWithContext(ctx, time.Duration(attempt+1)*150*time.Millisecond); waitErr != nil {
			return nil, waitErr
		}
	}
	return nil, lastErr
}

// execGitCommandOnce 在固定超时内执行一次 git 子命令。
// stderr 会进入错误消息，not-a-repo 错误会映射为 ErrNotGitRepository 供调用方判定。
func execGitCommandOnce(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	commandCtx, cancel := platformconfig.WithTimeout(ctx, gitCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", args...)
	cmd.Dir = dir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("difftracker: git %s timeout", strings.Join(args, " "))
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		if isNotGitRepository(message) {
			return nil, fmt.Errorf("%w: %s", ErrNotGitRepository, message)
		}
		return nil, fmt.Errorf("difftracker: git %s: %s", strings.Join(args, " "), message)
	}
	return stdout.Bytes(), nil
}

func gitBaseDir(dir string) (string, error) {
	base := strings.TrimSpace(dir)
	if base == "" {
		base = "."
	}
	info, err := os.Stat(base)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Clean(base), nil
	}
	return filepath.Dir(filepath.Clean(base)), nil
}

func splitNonEmptyLines(output string) []string {
	lines := strings.Split(normalizeNewlines(output), "\n")
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		path := normalizeDiffPath(line)
		if path != "" {
			items = append(items, path)
		}
	}
	return items
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	items := make([]string, 0, len(values))
	for _, value := range values {
		path := normalizeDiffPath(value)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		items = append(items, path)
	}
	slicesSort(items)
	return items
}

func slicesSort(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func isMissingHEADPath(err error) bool {
	message := err.Error()
	return strings.Contains(message, "does not exist in 'HEAD'") || // archguard:ignore priority_ssa_error_string -- Git show exposes a path absent from HEAD only through stderr text.
		strings.Contains(message, "exists on disk, but not in 'HEAD'")
}

func isNotGitRepository(message string) bool {
	return strings.Contains(message, "not a git repository") || strings.Contains(message, "outside repository")
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
