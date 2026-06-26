package reportgc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// MaxAge 是 stopped/archived agent report 文件的默认保留时间。
const MaxAge = 30 * 24 * time.Hour

// Logger 是 report GC 需要的最小日志接口，便于在测试中注入轻量 stub。
type Logger interface {
	Info(msg string, args ...any)
}

// Collect 删除同 cwd 下已 stopped/archived 且超过保留期的 agent report 文件。
// 仍在运行的 agent id 会被 protected 集合保护，避免误删活跃 report。
func Collect[T any](cwd string, threads []T, fields func(T) (agentID, threadCwd, status string), now time.Time, logger Logger) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	eligible := eligibleIDs(cwd, threads, fields)
	if len(eligible) == 0 {
		return nil
	}
	dir := filepath.Join(cwd, ".agnet", "report")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("agent report gc read dir: %w", err)
	}
	cutoff := now.Add(-MaxAge)
	for _, entry := range entries {
		remove, err := shouldRemove(entry, eligible, cutoff)
		if err != nil {
			return err
		}
		if !remove {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("agent report gc remove %s: %w", path, err)
		}
		if logger != nil {
			idPart, _, _ := strings.Cut(entry.Name(), "+")
			logger.Info("orchestration: removed expired agent report", "path", path, "agent_id", idPart)
		}
	}
	return nil
}

// eligibleIDs 计算允许清理的 agent id；同一 id 只要存在活跃线程就不会被清理。
func eligibleIDs[T any](cwd string, threads []T, fields func(T) (agentID, threadCwd, status string)) map[string]struct{} {
	eligible, protected := map[string]struct{}{}, map[string]struct{}{}
	for _, thread := range threads {
		agentID, threadCwd, status := fields(thread)
		idPart := Sanitize(agentID)
		if idPart == "" || !sameCwd(threadCwd, cwd) {
			continue
		}
		if isStopped(status) {
			eligible[idPart] = struct{}{}
		} else {
			protected[idPart] = struct{}{}
		}
	}
	for idPart := range protected {
		delete(eligible, idPart)
	}
	return eligible
}

// shouldRemove 判断单个 report 文件是否属于可清理 agent 且早于 cutoff。
func shouldRemove(entry os.DirEntry, eligible map[string]struct{}, cutoff time.Time) (bool, error) {
	if entry.IsDir() {
		return false, nil
	}
	idPart, _, ok := strings.Cut(entry.Name(), "+")
	if !ok || strings.TrimSpace(idPart) == "" {
		return false, nil
	}
	if _, ok := eligible[idPart]; !ok {
		return false, nil
	}
	info, err := entry.Info()
	if err != nil {
		return false, fmt.Errorf("agent report gc stat %s: %w", entry.Name(), err)
	}
	return info.ModTime().Before(cutoff), nil
}

// isStopped 判断持久化 thread 状态是否允许清理 report。
func isStopped(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "stopped", "archived":
		return true
	default:
		return false
	}
}

// sameCwd 以 filepath.Clean 后的结果比较 cwd，避免路径文本差异影响清理范围。
func sameCwd(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	return left != "" && right != "" && filepath.Clean(left) == filepath.Clean(right)
}

// Sanitize 将 agent id/name 清理为可用于 report 文件名的片段。
func Sanitize(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			if !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		builder.WriteRune(r)
		lastUnderscore = false
	}
	return strings.TrimSpace(builder.String())
}
