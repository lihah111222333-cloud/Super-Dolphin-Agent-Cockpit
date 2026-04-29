package skillforge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RecoveryReport 是 RecoverStaging 的统计结果。
type RecoveryReport struct {
	Restored      []string // 把 .{name}.bak-* 恢复为 {name} 的 skill 名集
	RemovedBackup []string // .{name}.bak-* 被丢弃（target 已存在）的 skill 名集
	RemovedTmp    []string // 被清理的 .{name}.tmp-* / 老 {name}.tmp 全名集
	Errors        []error
}

// RecoverStaging 扫描 cacheDir，处理 publish 中断遗留的 staging 目录。
// 对齐 spec §4.4 startup recovery 语义，命名约定与 atomic.go 保持一致：
//
//   - .{name}.bak-{suffix}：
//   - target 不存在 → rename 回 target（恢复旧版本，避免数据丢失）
//   - target 存在    → 删除（publish 已成功，backup 是清理失败的残留）
//   - .{name}.tmp-{suffix}：删除（publish 中断的脏数据，无法信任）
//   - {name}.tmp（无前导点的老格式）：删除
//
// cacheDir 不存在视作"无残骸"，返回空 report、nil error。
// 单条恢复失败不中断扫描；错误进入 report.Errors，调用方按需上报。
//
// 必须在 reconcile 删孤儿前调用——否则 .{name}.bak-{suffix} 这类备份目录
// 会被 removeOrphans 当作孤儿删掉，丢失唯一的可工作版本。
func RecoverStaging(cacheDir string) (*RecoveryReport, error) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &RecoveryReport{}, nil
		}
		return nil, fmt.Errorf("skillforge: recover read cache dir: %w", err)
	}
	report := &RecoveryReport{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if skill, ok := parseBackupName(name); ok {
			recoverBackup(cacheDir, name, skill, report)
			continue
		}
		if _, ok := parseTmpName(name); ok {
			removeStaging(cacheDir, name, report)
			continue
		}
		if isLegacyTmp(name) {
			removeStaging(cacheDir, name, report)
			continue
		}
	}
	return report, nil
}

// parseBackupName 匹配 .{name}.bak-{suffix}；返回 skill 名。
func parseBackupName(full string) (string, bool) {
	if !strings.HasPrefix(full, ".") {
		return "", false
	}
	idx := strings.Index(full, ".bak-")
	if idx <= 1 {
		return "", false
	}
	return full[1:idx], true
}

// parseTmpName 匹配 .{name}.tmp-{suffix}；返回 skill 名。
func parseTmpName(full string) (string, bool) {
	if !strings.HasPrefix(full, ".") {
		return "", false
	}
	idx := strings.Index(full, ".tmp-")
	if idx <= 1 {
		return "", false
	}
	return full[1:idx], true
}

// isLegacyTmp 匹配 pre-Gap#1 的老格式 {name}.tmp（无前导点）。
func isLegacyTmp(full string) bool {
	return !strings.HasPrefix(full, ".") && strings.HasSuffix(full, ".tmp") && len(full) > len(".tmp")
}

func recoverBackup(cacheDir, full, skill string, report *RecoveryReport) {
	bakPath := filepath.Join(cacheDir, full)
	target := filepath.Join(cacheDir, skill)
	if dirExists(target) {
		if err := os.RemoveAll(bakPath); err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("skillforge: recover discard backup %s: %w", full, err))
			return
		}
		report.RemovedBackup = append(report.RemovedBackup, skill)
		return
	}
	if err := renamePath(bakPath, target); err != nil {
		report.Errors = append(report.Errors, fmt.Errorf("skillforge: recover restore %s: %w", full, err))
		return
	}
	report.Restored = append(report.Restored, skill)
}

func removeStaging(cacheDir, full string, report *RecoveryReport) {
	if err := os.RemoveAll(filepath.Join(cacheDir, full)); err != nil {
		report.Errors = append(report.Errors, fmt.Errorf("skillforge: recover remove tmp %s: %w", full, err))
		return
	}
	report.RemovedTmp = append(report.RemovedTmp, full)
}
