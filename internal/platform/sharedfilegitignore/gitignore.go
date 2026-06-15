package sharedfilegitignore

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Phase 3.8 / 3C · sharedfile `.gitignore` 默认策略
//
// 目标：sharedfile 落盘后产出的 `<cwd>/.agnet/shared/_internal/...` 不应进
// git index（系统私有 state，例如 1.7f / 1.8a 累计计数、3.10 progress 文件）。
// 其余 prefix（handoff/ dag/ inbox/ reports/）默认可 commit，让 agent 协作产
// 出能审计回溯。
//
// Ensure(cwd) 幂等：若 .gitignore 已存在并已包含 `_internal/` 规则（含父目
// 录通配 `.agnet/shared/`），不动；否则追加一行。每个进程内对同一 cwd 只
// 跑一次磁盘 IO（cwdOnce），不破坏调用方在 hot path 反复触发的语义。

const (
	// gitignoreEntry 是要写入 .gitignore 的相对规则。git 解读 `<dir>/`
	// 形式时把它当成目录通配，整树忽略。
	gitignoreEntry = ".agnet/shared/_internal/"
	// gitignoreHeader 标记自动追加的来源；纯注释，不影响 git 行为。
	gitignoreHeader = "# auto-managed by mcp-orch (Phase 3.8 sharedfile _internal)"
)

// Ensure makes sure `<cwd>/.gitignore` ignores `.agnet/shared/_internal/`.
// Empty cwd is a no-op (test environments / fx graphs without
// platformconfig). Failures are returned, not logged here — callers decide
// whether to log-warn or surface. Logger may be nil; pass a real *slog.Logger
// from the caller to record a single-line "appended" event the first time
// per cwd.
// Ensure 确保平台sharedfilegitignore。
func Ensure(cwd string, logger *slog.Logger) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	state := loadOrCreateState(cwd)
	state.once.Do(func() {
		state.err = ensureOnce(cwd, logger)
	})
	// state.err is published by sync.Once: any caller returning from
	// state.once.Do() observes the write that happened inside the first
	// invocation's f. No package-level error variable, so a failure for
	// cwd1 cannot bleed into cwd2's return value.
	return state.err
}

// ResetForTests clears the per-cwd memoization so unit tests can drive the
// helper repeatedly inside a single process. Production callers must not use
// this.
// ResetForTests 为tests重置平台sharedfilegitignore。
func ResetForTests() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.byCWD = make(map[string]*ensureState)
}

// ensureState bundles the per-cwd sync.Once gate with the error captured
// inside its f. Storing them together keeps every cwd's success/failure
// signal isolated; a previous design used a package-level `ensureErr`
// variable shared across all cwds, which both raced (no synchronization
// for reads outside Once.Do) and corrupted return values when distinct
// cwds completed in interleaved order. See archtest
// TestSharedfileGitignoreNoPackageLevelErrorVar for the lock-in.
type ensureState struct {
	once sync.Once
	err  error
}

type gitignoreState struct {
	mu    sync.Mutex
	byCWD map[string]*ensureState
}

var state = &gitignoreState{byCWD: make(map[string]*ensureState)}

func loadOrCreateState(cwd string) *ensureState {
	state.mu.Lock()
	defer state.mu.Unlock()
	if existing, ok := state.byCWD[cwd]; ok {
		return existing
	}
	s := &ensureState{}
	state.byCWD[cwd] = s
	return s
}

// ensureOnce 确保once。
func ensureOnce(cwd string, logger *slog.Logger) error {
	gitignorePath := filepath.Join(cwd, ".gitignore")
	existing, readErr := os.ReadFile(gitignorePath)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return fmt.Errorf("sharedfilegitignore: read %s: %w", gitignorePath, readErr)
	}
	if readErr == nil && hasMatchingRule(existing) {
		return nil
	}
	var buf strings.Builder
	if readErr == nil && len(existing) > 0 {
		buf.Write(existing)
		if !strings.HasSuffix(string(existing), "\n") {
			buf.WriteByte('\n')
		}
	}
	buf.WriteString(gitignoreHeader)
	buf.WriteByte('\n')
	buf.WriteString(gitignoreEntry)
	buf.WriteByte('\n')
	if writeErr := writeFileAtomic(gitignorePath, []byte(buf.String())); writeErr != nil {
		return writeErr
	}
	if logger != nil {
		logger.Info("sharedfile: appended .agnet/shared/_internal/ to .gitignore",
			"cwd", cwd,
			"path", gitignorePath)
	}
	return nil
}

// hasMatchingRule scans existing .gitignore lines and returns true when an
// entry already covers .agnet/shared/_internal/. We accept a few common
// shapes: the literal entry, the leading-slash form (`/.agnet/shared/_internal/`),
// the no-trailing-slash form, and any parent-directory wildcard like
// `.agnet/shared/` or `.agnet/`.
func hasMatchingRule(content []byte) bool {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// strip leading `/` (gitignore root anchor); both `/x` and `x` mean
		// the same when at repo root.
		canonical := strings.TrimPrefix(line, "/")
		canonical = strings.TrimSuffix(canonical, "/")
		switch canonical {
		case ".agnet/shared/_internal",
			".agnet/shared",
			".agnet":
			return true
		}
	}
	return false
}

// writeFileAtomic mirrors sharedfilefs.WriteAtomic's tmp + rename pattern
// but stays in this package to avoid depending on sharedfilefs (which would
// pull DB / SQL deps into a leaf gitignore helper). Crash-safe enough for a
// single config file: tmp + fsync + rename.
// writeFileAtomic 写入文件atomic。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".gitignore.tmp-")
	if err != nil {
		return fmt.Errorf("sharedfilegitignore: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	cleaned := false
	defer func() {
		if !cleaned {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("sharedfilegitignore: write tmp: %w", writeErr)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		_ = tmp.Close()
		return fmt.Errorf("sharedfilegitignore: fsync: %w", syncErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("sharedfilegitignore: close tmp: %w", closeErr)
	}
	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		return fmt.Errorf("sharedfilegitignore: rename %s → %s: %w", tmpPath, path, renameErr)
	}
	cleaned = true
	return nil
}
