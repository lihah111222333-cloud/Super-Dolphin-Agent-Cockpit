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
)

// sharedfilegitignore 管理 sharedfile 私有状态的 `.gitignore` 规则。
//
// `<cwd>/.agnet/shared/_internal/...` 保存系统私有状态，不应进入 git index；
// handoff、dag、inbox 和 reports 等协作产物默认仍可提交，便于审计回溯。
//
// Ensure 每次检查 `.gitignore`；已有 `_internal/` 或父目录覆盖规则时不改文件。

const (
	// gitignoreEntry 是要写入 .gitignore 的相对规则。git 解读 `<dir>/`
	// 形式时把它当成目录通配，整树忽略。
	gitignoreEntry = ".agnet/shared/_internal/"
	// gitignoreHeader 标记自动追加的来源；纯注释，不影响 git 行为。
	gitignoreHeader = "# auto-managed by mcp-orch (Phase 3.8 sharedfile _internal)"
)

// Ensure 确保 `<cwd>/.gitignore` 忽略 `.agnet/shared/_internal/`。
// 空 cwd 视为无平台配置的测试或 fx 场景。
func Ensure(cwd string, logger *slog.Logger) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	return ensureRule(cwd, logger)
}

// ensureRule 执行 `.gitignore` 检查和追加写入，已有覆盖规则时不改文件。
func ensureRule(cwd string, logger *slog.Logger) error {
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
