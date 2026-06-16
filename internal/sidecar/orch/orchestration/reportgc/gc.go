package reportgc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// MaxAge is part of the reportgc package API.
const MaxAge = 30 * 24 * time.Hour

// Logger describes a reportgc API type.
type Logger interface {
	Info(msg string, args ...any)
}

// Collect 收集编排。
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

// eligibleIDs 处理eligibleids。
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

// shouldRemove 判断remove是否可用。
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

func isStopped(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "stopped", "archived":
		return true
	default:
		return false
	}
}

func sameCwd(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	return left != "" && right != "" && filepath.Clean(left) == filepath.Clean(right)
}

// Sanitize 清理编排。
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
