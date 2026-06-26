package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/devtools/sqlitereleasegate"
)

// defaultReportPath 是 SQLite 发布闸门默认写入的 Markdown 报告位置。
const defaultReportPath = "docs/cc/数据库切换/sqlite-release-gate-report.md"

// main 解析 SQLite 发布闸门 CLI 参数并执行检查。
// 即使 gate 运行失败也会先写报告，保证失败现场能被后续排查读取。
func main() {
	var (
		out          = flag.String("out", filepath.FromSlash(defaultReportPath), "markdown report path")
		logDir       = flag.String("logs", ".sqlite-release-gate-logs", "raw log artifact directory")
		only         = flag.String("only", "", "comma-separated gate IDs, for example G12")
		timeout      = flag.Duration("timeout", 10*time.Minute, "per-gate timeout")
		allowPartial = flag.Bool("allow-partial", false, "allow reports that intentionally run only a subset of gates")
	)
	flag.Parse()

	repoRoot, err := os.Getwd()
	if err != nil {
		fatalf("get repo root: %v", err)
	}
	reportPath, err := cleanRequiredFlagPath("out", *out)
	if err != nil {
		fatalf("%v", err)
	}
	logDirPath, err := cleanRequiredFlagPath("logs", *logDir)
	if err != nil {
		fatalf("%v", err)
	}
	selected := splitIDs(*only)
	report, runErr := sqlitereleasegate.Run(context.Background(), sqlitereleasegate.RunOptions{
		RepoRoot:     repoRoot,
		LogDir:       logDirPath,
		Only:         selected,
		Timeout:      *timeout,
		AllowPartial: *allowPartial,
	})
	if err := sqlitereleasegate.WriteReport(reportPath, report); err != nil {
		fatalf("%v", err)
	}
	if runErr != nil {
		fatalf("%v", runErr)
	}
}

// cleanRequiredFlagPath 在 filepath.Clean 前拒绝空路径，避免空 flag 被清理成当前目录。
func cleanRequiredFlagPath(name, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s path is required", name)
	}
	return filepath.Clean(trimmed), nil
}

// splitIDs 解析逗号分隔的 gate ID 列表，并跳过空白片段。
func splitIDs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// fatalf 将 CLI 错误写入 stderr 后以 1 退出，保持脚本失败语义一致。
func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
