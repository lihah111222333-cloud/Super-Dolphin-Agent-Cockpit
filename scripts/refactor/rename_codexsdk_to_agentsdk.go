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

// editCollector 收集单文件 import 替换及报告项，便于测试注入。
type editCollector func(path string, src []byte) ([]edit, []replacement, error)

// editApplier 应用已排序 edit，便于测试验证写入前的纯函数行为。
type editApplier func(src []byte, edits []edit) []byte

// main 扫描 codexsdk import，并按 dry-run 或 apply 模式输出报告。
func main() {
	var (
		dryRun    = flag.Bool("dry-run", false, "scan and report replacements without writing files")
		apply     = flag.Bool("apply", false, "apply replacements to files")
		reportOut = flag.String("report", "", "write JSON report to this path")
		root      = flag.String("root", ".", "repository root")
	)
	flag.Parse()

	if *dryRun && *apply {
		fatalf("--dry-run and --apply are mutually exclusive")
	}
	if !*dryRun && !*apply {
		*dryRun = true
	}

	rootAbs, err := filepath.Abs(strings.TrimSpace(*root))
	if err != nil {
		fatalf("resolve root failed: %v", err)
	}

	mode := "dry-run"
	if *apply {
		mode = "apply"
	}

	fileReports := make([]fileReport, 0)
	totalReplacements := 0
	collect := func(path string, src []byte) ([]edit, []replacement, error) {
		return collectEdits(path, src)
	}
	applyEditSet := func(src []byte, edits []edit) []byte {
		return applyEdits(src, edits)
	}

	err = filepath.WalkDir(rootAbs, renameWalkDir(rootAbs, *apply, collect, applyEditSet, &fileReports, &totalReplacements))
	if err != nil {
		fatalf("scan failed: %v", err)
	}

	sort.Slice(fileReports, func(i, j int) bool { return fileReports[i].File < fileReports[j].File })

	rep := report{
		Mode:              mode,
		Root:              rootAbs,
		OldImportRoot:     oldImportRoot,
		NewImportRoot:     newImportRoot,
		TotalFiles:        len(fileReports),
		TotalReplacements: totalReplacements,
		Files:             fileReports,
		GeneratedAt:       time.Now().Format(time.RFC3339),
	}

	trimmedReportOut := strings.TrimSpace(*reportOut)
	if trimmedReportOut != "" {
		if err := writeReport(trimmedReportOut, rep); err != nil {
			fatalf("write report failed: %v", err)
		}
	}

	printReportSummary(rep, trimmedReportOut)
}

// renameWalkDir 构造 WalkDir 回调，过滤目录后处理 Go 文件。
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

// processRenameFile 收集单个 Go 文件的 import 改写，并在 apply 模式下写回文件。
func processRenameFile(rootAbs, path string, apply bool, collect editCollector, applyEditSet editApplier, fileReports *[]fileReport, totalReplacements *int) error {
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)

	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", rel, err)
	}

	edits, replacements, err := collect(path, src)
	if err != nil {
		return fmt.Errorf("collect edits for %s: %w", rel, err)
	}
	if len(edits) == 0 {
		return nil
	}

	if apply {
		updated := applyEditSet(src, edits)
		if err := os.WriteFile(path, updated, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}

	*fileReports = append(*fileReports, fileReport{
		File:         rel,
		Count:        len(replacements),
		Replacements: replacements,
	})
	*totalReplacements += len(replacements)
	return nil
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
