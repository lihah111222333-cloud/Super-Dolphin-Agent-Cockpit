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

// sharedfilegitignore 管理 sharedfile 私有状态的 `.gitignore` 规则。
//
// `<cwd>/.agnet/shared/_internal/...` 保存系统私有状态，不应进入 git index；
// handoff、dag、inbox 和 reports 等协作产物默认仍可提交，便于审计回溯。
//
// Ensure 对同一 cwd 在进程内只执行一次磁盘 IO；已有 `_internal/` 或父目录覆盖规则时不改文件。

const (
	// gitignoreEntry 是要写入 .gitignore 的相对规则。git 解读 `<dir>/`
	// 形式时把它当成目录通配，整树忽略。
	gitignoreEntry = ".agnet/shared/_internal/"
	// gitignoreHeader 标记自动追加的来源；纯注释，不影响 git 行为。
	gitignoreHeader = "# auto-managed by mcp-orch (Phase 3.8 sharedfile _internal)"
)

// Ensure 确保 `<cwd>/.gitignore` 忽略 `.agnet/shared/_internal/`。
// 空 cwd 视为无平台配置的测试或 fx 场景；每个 cwd 在进程内只执行一次实际 IO。
func Ensure(cwd string, logger *slog.Logger) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	state := loadOrCreateState(cwd)
	state.once.Do(func() {
		state.err = ensureOnce(cwd, logger)
	})
	// sync.Once 会发布本次执行错误，且错误挂在每个 cwd 的状态上，避免不同 cwd 的结果互相污染。
	return state.err
}

// ResetForTests 清空按 cwd 记录的执行状态，允许单进程测试重复覆盖 Ensure 路径。
func ResetForTests() {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.byCWD = make(map[string]*ensureState)
}

// ensureState 把每个 cwd 的 sync.Once 和执行错误放在一起，保证成功/失败状态隔离。
type ensureState struct {
	once sync.Once
	err  error
}

// gitignoreState 保存每个 cwd 独立的 once 状态，避免不同仓库的错误互相污染。
type gitignoreState struct {
	mu    sync.Mutex
	byCWD map[string]*ensureState
}

var state = &gitignoreState{byCWD: make(map[string]*ensureState)}

// loadOrCreateState 在全局锁下读取或创建 cwd 对应的 ensureState。
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

// ensureOnce 执行单次 `.gitignore` 检查和追加写入，已有覆盖规则时不改文件。
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

// hasMatchingRule 检查现有 `.gitignore` 是否已覆盖 `.agnet/shared/_internal/`。
// 允许字面规则、根锚定规则、无尾斜杠规则，以及 `.agnet/shared/` 或 `.agnet/` 父目录规则。
func hasMatchingRule(content []byte) bool {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 去掉 gitignore 根锚定前缀；仓库根下 `/x` 和 `x` 覆盖同一目标。
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

// writeFileAtomic 使用 tmp、fsync 和 rename 写入 `.gitignore`。
// 它留在本包内，避免 leaf helper 反向依赖 sharedfilefs 及其 store 相关依赖。
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
