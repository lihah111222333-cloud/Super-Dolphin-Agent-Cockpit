//go:build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	modulePkgRoot = "github.com/multi-agent/go-agent-v2/pkg/"
	oldSDKDir     = "codexsdk"
	newSDKDir     = "agentsdk"

	oldImportRoot = modulePkgRoot + oldSDKDir
	newImportRoot = modulePkgRoot + newSDKDir
)

// replacement 是 JSON 报告中的单个 import 改写记录。
type replacement struct {
	Old  string `json:"old"`
	New  string `json:"new"`
	Line int    `json:"line"`
}

// fileReport 是 JSON 报告中单个文件的改写汇总。
type fileReport struct {
	File         string        `json:"file"`
	Count        int           `json:"count"`
	Replacements []replacement `json:"replacements"`
}

// report 是 dry-run/apply 两种模式共用的 JSON 报告顶层结构。
type report struct {
	Mode              string       `json:"mode"`
	Root              string       `json:"root"`
	OldImportRoot     string       `json:"oldImportRoot"`
	NewImportRoot     string       `json:"newImportRoot"`
	TotalFiles        int          `json:"totalFiles"`
	TotalReplacements int          `json:"totalReplacements"`
	Files             []fileReport `json:"files"`
	GeneratedAt       string       `json:"generatedAt"`
}

// edit 描述一个 import 字面量在源文件字节切片中的替换范围。
type edit struct {
	Start  int
	End    int
	OldLit string
	NewLit string
	Line   int
}

// renamePlan 保存单文件改写所需的原始内容、权限和报告数据，用于 apply 失败时回滚。
type renamePlan struct {
	Path         string
	Rel          string
	Src          []byte
	Edits        []edit
	Replacements []replacement
	FileMode     fs.FileMode
}

// editCollector 收集单文件 import 替换及报告项，便于测试注入。
type editCollector func(path string, src []byte) ([]edit, []replacement, error)

// editApplier 应用已排序 edit，便于测试验证写入前的纯函数行为。
type editApplier func(src []byte, edits []edit) []byte

type renameOptions struct {
	DryRun    bool
	Apply     bool
	ReportOut string
	Root      string
}

// main 扫描 codexsdk import，并按 dry-run 或 apply 模式输出报告。
func main() {
	opts := parseRenameOptions()
	rep, reportOut, err := runRename(opts, collectEdits, applyEdits)
	if err != nil {
		fatalf("%v", err)
	}
	printReportSummary(rep, reportOut)
}

func parseRenameOptions() renameOptions {
	dryRun := flag.Bool("dry-run", false, "scan and report replacements without writing files")
	apply := flag.Bool("apply", false, "apply replacements to files")
	reportOut := flag.String("report", "", "write JSON report to this path")
	root := flag.String("root", ".", "repository root")
	flag.Parse()

	if *dryRun && *apply {
		fatalf("--dry-run and --apply are mutually exclusive")
	}
	if !*dryRun && !*apply {
		*dryRun = true
	}
	return renameOptions{DryRun: *dryRun, Apply: *apply, ReportOut: *reportOut, Root: *root}
}

// runRename 先完整收集改写计划，再按阶段执行 apply 和报告写入，确保失败可回滚。
func runRename(opts renameOptions, collect editCollector, applyEditSet editApplier) (report, string, error) {
	rootAbs, err := filepath.Abs(strings.TrimSpace(opts.Root))
	if err != nil {
		return report{}, "", fmt.Errorf("resolve root failed: %w", err)
	}

	plans, err := collectRenamePlans(rootAbs, collect)
	if err != nil {
		return report{}, "", fmt.Errorf("scan failed: %w", err)
	}

	fileReports, totalReplacements := buildRenameFileReports(plans)

	if opts.Apply {
		if err := applyRenamePlans(plans, applyEditSet); err != nil {
			return report{}, "", fmt.Errorf("apply failed: %w", err)
		}
	}

	rep := report{
		Mode:              renameMode(opts.Apply),
		Root:              rootAbs,
		OldImportRoot:     oldImportRoot,
		NewImportRoot:     newImportRoot,
		TotalFiles:        len(fileReports),
		TotalReplacements: totalReplacements,
		Files:             fileReports,
		GeneratedAt:       time.Now().Format(time.RFC3339),
	}

	trimmedReportOut := strings.TrimSpace(opts.ReportOut)
	if trimmedReportOut != "" {
		if err := writeReportWithRollback(trimmedReportOut, rep, opts.Apply, plans); err != nil {
			return report{}, "", err
		}
	}
	return rep, trimmedReportOut, nil
}

func renameMode(apply bool) string {
	if apply {
		return "apply"
	}
	return "dry-run"
}

func collectRenamePlans(rootAbs string, collect editCollector) ([]renamePlan, error) {
	plans := make([]renamePlan, 0)
	err := filepath.WalkDir(rootAbs, planRenameWalkDir(rootAbs, collect, &plans))
	if err != nil {
		return nil, err
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Rel < plans[j].Rel })
	return plans, nil
}

func buildRenameFileReports(plans []renamePlan) ([]fileReport, int) {
	fileReports := make([]fileReport, 0, len(plans))
	totalReplacements := 0
	for _, plan := range plans {
		recordRenamePlan(plan, &fileReports, &totalReplacements)
	}
	sort.Slice(fileReports, func(i, j int) bool { return fileReports[i].File < fileReports[j].File })
	return fileReports, totalReplacements
}

// planRenameWalkDir 构造 WalkDir 回调，过滤目录后只收集改写计划，不做文件写入。
func planRenameWalkDir(rootAbs string, collect editCollector, plans *[]renamePlan) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return skipRenameDir(d.Name())
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		plan, ok, err := buildRenamePlan(rootAbs, path, collect)
		if err != nil {
			return err
		}
		if ok {
			*plans = append(*plans, plan)
		}
		return nil
	}
}

// renameWalkDir 保留单文件处理回调，供回归测试注入 collector/applier。
func renameWalkDir(rootAbs string, apply bool, collect editCollector, applyEditSet editApplier, fileReports *[]fileReport, totalReplacements *int) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return skipRenameDir(d.Name())
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		return processRenameFile(rootAbs, path, apply, collect, applyEditSet, fileReports, totalReplacements)
	}
}

// skipRenameDir 跳过仓库元数据、工作区和生成产物目录，避免批量改写越界。
func skipRenameDir(name string) error {
	switch name {
	case ".git", ".worktrees", ".agent", "node_modules", ".idea", ".vscode", "dist", "build", "vendor":
		return filepath.SkipDir
	}
	return nil
}

// buildRenamePlan 读取单个 Go 文件并生成可回滚的改写计划；无改写时返回 ok=false。
func buildRenamePlan(rootAbs, path string, collect editCollector) (renamePlan, bool, error) {
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil {
		return renamePlan{}, false, err
	}
	rel = filepath.ToSlash(rel)

	src, err := os.ReadFile(path)
	if err != nil {
		return renamePlan{}, false, fmt.Errorf("read %s: %w", rel, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return renamePlan{}, false, fmt.Errorf("stat %s: %w", rel, err)
	}

	edits, replacements, err := collect(path, src)
	if err != nil {
		return renamePlan{}, false, fmt.Errorf("collect edits for %s: %w", rel, err)
	}
	if len(edits) == 0 {
		return renamePlan{}, false, nil
	}
	return renamePlan{Path: path, Rel: rel, Src: src, Edits: edits, Replacements: replacements, FileMode: info.Mode().Perm()}, true, nil
}

// processRenameFile 收集单个 Go 文件的 import 改写，并在 apply 模式下写回文件。
func processRenameFile(rootAbs, path string, apply bool, collect editCollector, applyEditSet editApplier, fileReports *[]fileReport, totalReplacements *int) error {
	plan, ok, err := buildRenamePlan(rootAbs, path, collect)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	if apply {
		updated := applyEditSet(plan.Src, plan.Edits)
		if err := writeFilePreservingMode(plan.Path, updated, plan.FileMode); err != nil {
			return fmt.Errorf("write %s: %w", plan.Rel, err)
		}
	}

	recordRenamePlan(plan, fileReports, totalReplacements)
	return nil
}

// recordRenamePlan 将单个改写计划转换为报告项。
func recordRenamePlan(plan renamePlan, fileReports *[]fileReport, totalReplacements *int) {
	*fileReports = append(*fileReports, fileReport{
		File:         plan.Rel,
		Count:        len(plan.Replacements),
		Replacements: plan.Replacements,
	})
	*totalReplacements += len(plan.Replacements)
}

// applyRenamePlans 写入全部计划；任一写入失败时回滚已经写入的文件。
func applyRenamePlans(plans []renamePlan, applyEditSet editApplier) error {
	applied := make([]renamePlan, 0, len(plans))
	for _, plan := range plans {
		updated := applyEditSet(plan.Src, plan.Edits)
		if err := writeFilePreservingMode(plan.Path, updated, plan.FileMode); err != nil {
			if rollbackErr := rollbackRenamePlans(applied); rollbackErr != nil {
				return fmt.Errorf("write %s: %w; rollback failed: %v", plan.Rel, err, rollbackErr)
			}
			return fmt.Errorf("write %s: %w", plan.Rel, err)
		}
		applied = append(applied, plan)
	}
	return nil
}

// rollbackRenamePlans 按反向顺序恢复已写入文件的原始内容和权限。
func rollbackRenamePlans(plans []renamePlan) error {
	for i := len(plans) - 1; i >= 0; i-- {
		plan := plans[i]
		if err := writeFilePreservingMode(plan.Path, plan.Src, plan.FileMode); err != nil {
			return fmt.Errorf("%s: %w", plan.Rel, err)
		}
	}
	return nil
}

// writeReportWithRollback 在 report 写失败时回滚已 apply 的改写。
func writeReportWithRollback(path string, rep report, applied bool, plans []renamePlan) error {
	if err := writeReport(path, rep); err != nil {
		if applied {
			if rollbackErr := rollbackRenamePlans(plans); rollbackErr != nil {
				return fmt.Errorf("write report failed: %w; rollback failed: %v", err, rollbackErr)
			}
		}
		return fmt.Errorf("write report failed: %w", err)
	}
	return nil
}

// writeFilePreservingMode 写文件后显式恢复原权限，避免 truncate 改写改变 fixture/源码权限。
func writeFilePreservingMode(path string, data []byte, mode fs.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// printReportSummary 输出人类可读摘要，完整明细可通过 --report 写 JSON。
func printReportSummary(rep report, reportOut string) {
	fmt.Printf("mode=%s files=%d replacements=%d\n", rep.Mode, rep.TotalFiles, rep.TotalReplacements)
	if reportOut != "" {
		fmt.Printf("report=%s\n", reportOut)
	}
	for _, fr := range rep.Files {
		fmt.Printf("%s (%d)\n", fr.File, fr.Count)
	}
}

// collectEdits 只解析 import 声明并收集 codexsdk 到 agentsdk 的替换范围。
func collectEdits(path string, src []byte) ([]edit, []replacement, error) {
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, path, src, parser.ImportsOnly)
	if err != nil {
		return nil, nil, err
	}
	edits := make([]edit, 0)
	reports := make([]replacement, 0)
	for _, imp := range fileAST.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		oldPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		newPath, ok := rewriteImportPath(oldPath)
		if !ok {
			continue
		}
		start := fset.Position(imp.Path.Pos()).Offset
		end := fset.Position(imp.Path.End()).Offset
		line := fset.Position(imp.Path.Pos()).Line
		edits = append(edits, edit{
			Start:  start,
			End:    end,
			OldLit: imp.Path.Value,
			NewLit: strconv.Quote(newPath),
			Line:   line,
		})
		reports = append(reports, replacement{Old: oldPath, New: newPath, Line: line})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
	return edits, reports, nil
}

// applyEdits 按倒序范围替换 import 字面量，避免前一次替换影响后续偏移。
func applyEdits(src []byte, edits []edit) []byte {
	out := append([]byte(nil), src...)
	for _, e := range edits {
		if e.Start < 0 || e.End < e.Start || e.End > len(out) {
			continue
		}
		out = append(out[:e.Start], append([]byte(e.NewLit), out[e.End:]...)...)
	}
	return out
}

// rewriteImportPath 将旧 SDK import 根替换为新 SDK 根。
func rewriteImportPath(path string) (string, bool) {
	if path == oldImportRoot {
		return newImportRoot, true
	}
	prefix := oldImportRoot + "/"
	if strings.HasPrefix(path, prefix) {
		suffix := strings.TrimPrefix(path, oldImportRoot)
		return newImportRoot + suffix, true
	}
	return "", false
}

// writeReport 将 JSON 报告写到指定路径，并负责创建父目录。
func writeReport(path string, rep report) error {
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// fatalf 输出错误并以 1 退出，保持脚本 fail-fast。
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
