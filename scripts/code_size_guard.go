//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

func main() {
	repoRoot, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌  代码守卫: %v\n", err)
		os.Exit(1)
	}
	violations := archtest.CheckAll(archtest.CheckOptions{
		RepoRoot:  repoRoot,
		ScanRoots: []string{"internal", "cmd", "scripts"},
		SkipDirs:  archtest.DefaultSkipDirs(),
	})
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
	if len(violations) > 0 {
		fmt.Fprintf(os.Stderr, "\n❌  代码守卫: 发现 %d 项违规\n\n", len(violations))
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, "  •", violation.String())
		}
		fmt.Fprintln(os.Stderr)
		os.Exit(1)
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
