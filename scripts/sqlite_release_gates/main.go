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

const defaultReportPath = "docs/cc/数据库切换/sqlite-release-gate-report.md"

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

func cleanRequiredFlagPath(name, raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("%s path is required", name)
	}
	return filepath.Clean(trimmed), nil
}

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

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
