//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func main() {
	mode := parseMode()
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  代码守卫: %v\n", err)
		os.Exit(1)
	}
	opts := archtest.CheckOptions{
		RepoRoot:  repoRoot,
		ScanRoots: []string{"internal", "cmd", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	}
	baselinePath := filepath.Join(repoRoot, "internal/archtest/baseline.json")

	switch mode {
	case "freeze":
		runFreeze(opts, baselinePath)
	case "strict":
		runStrict(opts)
	default:
		runCheck(opts, baselinePath)
	}
}

func parseMode() string {
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--freeze":
			return "freeze"
		case "--strict":
			return "strict"
		}
	}
	return "check"
}

// runFreeze 全仓扫描建立/重建 baseline。
func runFreeze(opts archtest.CheckOptions, baselinePath string) {
	fmt.Println("🔒  代码守卫: freeze 模式 — 建立 baseline")
	bl := archtest.FreezeBaseline(opts)
	if err := archtest.SaveBaseline(baselinePath, bl); err != nil {
		fmt.Fprintf(os.Stderr, "❌  代码守卫: 保存 baseline 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅  代码守卫: baseline 已建立 — %d 个文件已冻结\n", len(bl))
}

// runStrict 无 baseline 全量检查（现有 CheckAll 逻辑）。
func runStrict(opts archtest.CheckOptions) {
	fmt.Println("🔍  代码守卫: strict 模式 — 无 baseline 全量检查")
	runFreezeRegistryAutoRepair(opts)
	violations := archtest.CheckAll(opts)
	printThresholds()
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "\n❌  代码守卫: 发现 %d 项违规\n\n", len(violations))
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, "  •", violation.String())
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}
	fmt.Printf("✅  代码守卫: strict 模式全量通过\n")
}

// runCheck 默认棘轮模式：现有 CheckAll + baseline 棘轮 + 自动收缩。
func runCheck(opts archtest.CheckOptions, baselinePath string) {
	// Phase 1: 现有 freeze registry auto-repair + CheckAll（保持向后兼容）
	runFreezeRegistryAutoRepair(opts)
	violations := archtest.CheckAll(opts)
	printThresholds()
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "\n❌  代码守卫: 发现 %d 项违规\n\n", len(violations))
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, "  •", violation.String())
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
	}

	// Phase 2: baseline 棘轮检查（如果 baseline 存在且非空）
	blInfo, err := archtest.LoadBaseline(baselinePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  代码守卫: 加载 baseline 失败: %v（跳过棘轮检查）\n", err)
	} else if len(blInfo.Data) > 0 {
		result := archtest.CheckWithBaseline(opts, blInfo.Data)
		if !result.OK() {
			fmt.Fprintf(os.Stderr, "\n❌  代码守卫: 棘轮检查发现 %d 项恶化\n\n", len(result.Violations))
			for _, v := range result.Violations {
				fmt.Fprintln(os.Stderr, "  •", v.String())
			}
			fmt.Fprintln(os.Stderr)
			os.Exit(1)
		}

		// Phase 3: 自动收缩
		fileSet := buildFileSet(opts)
		repoRoot := opts.RepoRoot
		if repoRoot == "" {
			repoRoot = "."
		}
		newBL, stats := archtest.ShrinkBaseline(blInfo.Data, fileSet, func(relPath string) archtest.FileMetrics {
			return archtest.MeasureFileMetrics(filepath.Join(repoRoot, filepath.FromSlash(relPath)))
		})
		if stats.Changed() {
			if err := archtest.SaveBaseline(baselinePath, newBL); err != nil {
				fmt.Fprintf(os.Stderr, "⚠️  代码守卫: 保存收缩 baseline 失败: %v\n", err)
			} else {
				fmt.Printf("🧹  代码守卫: baseline 自动收缩 — 收紧 %d, 毕业 %d, 清理 %d\n",
					stats.Shrunk, stats.Graduated, stats.Removed)
			}
		}
		fmt.Printf("📊  代码守卫: baseline 棘轮通过 — %d 个文件冻结中\n", len(newBL))
	}

	fmt.Printf("✅  代码守卫: 全部通过 — 文件 ≤ %d 行, 函数 ≤ %d 行, 嵌套 ≤ %d 层, 圈复杂度 ≤ %d, 命名下划线 ≤ %d 个, 包文件数 ≤ %d, 包行数 ≤ %d\n",
		archtest.MaxFileLines,
		archtest.MaxFuncLines,
		archtest.MaxNestingDepth,
		archtest.MaxCCComplexity,
		archtest.MaxUnderscores,
		archtest.MaxPackageFiles,
		archtest.MaxPackageLines,
	)
}

func runFreezeRegistryAutoRepair(opts archtest.CheckOptions) {
	fixes, err := archtest.AutoRepairFreezeRegistry(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  代码守卫: auto-fix freeze registry 失败: %v\n", err)
		os.Exit(1)
	}
	for _, fix := range fixes {
		fmt.Printf("🧹  代码守卫: %s\n", fix.String())
	}
}

func printThresholds() {
	fmt.Printf("📏  代码守卫: 默认 文件 ≤ %d 行, 函数 ≤ %d 行, 嵌套 ≤ %d 层, 圈复杂度 ≤ %d, 命名下划线 ≤ %d 个, 包文件数 ≤ %d, 包行数 ≤ %d\n",
		archtest.MaxFileLines,
		archtest.MaxFuncLines,
		archtest.MaxNestingDepth,
		archtest.MaxCCComplexity,
		archtest.MaxUnderscores,
		archtest.MaxPackageFiles,
		archtest.MaxPackageLines,
	)
	fmt.Printf("   核心包(memory/prompt/thread/turn): 文件 ≤ %d 行, 包文件数 ≤ %d, 包行数 ≤ %d\n",
		archtest.MaxCorePackageFileLines,
		archtest.MaxCorePackageFiles,
		archtest.MaxCorePackageLines,
	)
}

// buildFileSet 构建当前仓库中所有非测试 Go 文件的 relPath set。
func buildFileSet(opts archtest.CheckOptions) map[string]bool {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	scanRoots := opts.ScanRoots
	if len(scanRoots) == 0 {
		scanRoots = archtest.DefaultScanRoots()
	}
	skipDirs := opts.SkipDirs
	if len(skipDirs) == 0 {
		skipDirs = archtest.DefaultSkipDirs()
	}
	fileSet := make(map[string]bool)
	for _, root := range scanRoots {
		absRoot := filepath.Join(repoRoot, root)
		_ = filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				if skipDirs[info.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				return nil
			}
			fileSet[filepath.ToSlash(rel)] = true
			return nil
		})
	}
	return fileSet
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd, nil
		}
		next := filepath.Dir(wd)
		if next == wd {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		wd = next
	}
}
