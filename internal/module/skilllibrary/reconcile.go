package skilllibrary

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
)

// Reconciler 把 library 的当前状态投影到 cache。
type Reconciler struct {
	store    *Store
	cacheDir string
}

func NewReconciler(s *Store, cacheDir string) *Reconciler {
	return &Reconciler{store: s, cacheDir: cacheDir}
}

// ReconcileReport 是 ReconcileAll 的统计结果。
type ReconcileReport struct {
	Built   int
	Removed int
	Errors  []error
}

// ReconcileOne 重建/删除单个 skill 的 cache 条目。
//   - library 没有该 skill → 从 cache 删除
//   - library 有但 Disabled → 从 cache 删除
//   - library 有且未 Disabled → forge 重建
func (r *Reconciler) ReconcileOne(name string) error {
	entry, err := r.store.Get(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return os.RemoveAll(filepath.Join(r.cacheDir, name))
		}
		return fmt.Errorf("skilllibrary: reconcileOne get %s: %w", name, err)
	}
	if entry.Meta.Disabled {
		return os.RemoveAll(filepath.Join(r.cacheDir, name))
	}
	libDir := filepath.Dir(entry.Dir)
	return skillforge.Forge(libDir, r.cacheDir, name, entry.Meta.SectionSummaries)
}

// ReconcileAll 全量对账：构建所有 enabled skill 到 cache，并清理孤儿条目。
// 启动时先跑 spec §4.4 startup recovery，把上一次 publish 中断遗留的
// .tmp-*/.bak-* 残骸处理掉，避免后续 removeOrphans 把唯一可工作版本（仅存在于
// .bak-* 备份中）当孤儿删除。
func (r *Reconciler) ReconcileAll() (*ReconcileReport, error) {
	if err := os.MkdirAll(r.cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("skilllibrary: mkdir cache: %w", err)
	}
	report := &ReconcileReport{}
	r.recoverStaging(report)
	libEntries, err := r.store.List()
	if err != nil {
		return nil, fmt.Errorf("skilllibrary: list library: %w", err)
	}
	libNames := r.buildLibrary(libEntries, report)
	r.removeOrphans(libNames, report)
	return report, nil
}

// recoverStaging 调用 skillforge.RecoverStaging 处理 publish 中断残骸；
// 恢复期错误并入 report.Errors，不阻断后续 reconcile 流程。
// skillforge 自身的 fatal 错误（如 ReadDir 权限失败）会冒到 report.Errors，
// 由调用方决定是否当致命处理。
func (r *Reconciler) recoverStaging(report *ReconcileReport) {
	rec, err := skillforge.RecoverStaging(r.cacheDir)
	if err != nil {
		report.Errors = append(report.Errors, fmt.Errorf("skilllibrary: recover staging: %w", err))
		return
	}
	for _, e := range rec.Errors {
		report.Errors = append(report.Errors, e)
	}
}

// buildLibrary forges all enabled skills and removes disabled ones from cache.
// Returns the set of all library skill names (enabled + disabled).
func (r *Reconciler) buildLibrary(entries []SkillEntry, report *ReconcileReport) map[string]struct{} {
	libNames := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		libNames[e.Meta.Name] = struct{}{}
		if e.Meta.Disabled {
			_ = os.RemoveAll(filepath.Join(r.cacheDir, e.Meta.Name))
			continue
		}
		if err := skillforge.Forge(filepath.Dir(e.Dir), r.cacheDir, e.Meta.Name, e.Meta.SectionSummaries); err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("forge %s: %w", e.Meta.Name, err))
			continue
		}
		report.Built++
	}
	return libNames
}

// removeOrphans deletes cache directories that have no corresponding library entry.
// ReadDir errors (e.g. permission denied) are surfaced to report.Errors instead
// of being silently dropped; fs.ErrNotExist is treated as "no orphans to clean"
// and returns quietly.
func (r *Reconciler) removeOrphans(libNames map[string]struct{}, report *ReconcileReport) {
	cacheEntries, err := os.ReadDir(r.cacheDir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			report.Errors = append(report.Errors, fmt.Errorf("skilllibrary: read cache dir: %w", err))
		}
		return
	}
	for _, e := range cacheEntries {
		if !e.IsDir() {
			continue
		}
		if _, ok := libNames[e.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(r.cacheDir, e.Name())); err != nil {
			report.Errors = append(report.Errors, fmt.Errorf("remove orphan %s: %w", e.Name(), err))
			continue
		}
		report.Removed++
	}
}
