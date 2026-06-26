package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/archtest"
)

const (
	sharedFileEffectiveLineTarget  = 500
	sharedTotalEffectiveLineTarget = 2000
	// sharedTotalEffectiveLineBaseline 记录当前 shared 包的既有预算债。
	// 注释治理不应改变有效行数；真实代码继续增长时仍会触发 ratchet。
	sharedTotalEffectiveLineBaseline = 2384
)

var sharedFileEffectiveLineBaselines = map[string]int{
	"internal/platform/shared/workflowtemplates/validation.go": 522,
}

func TestSharedBudget(t *testing.T) {
	root := repoRoot(t)
	if !dirExists(root, "internal/platform/shared") {
		t.Skip("directory not yet created")
	}
	files := walkGoFiles(t, root, "internal/platform/shared")
	var total int
	var violations []string
	for _, absPath := range files {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		data, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		lines := archtest.CountEffectiveLines(data)
		total += lines
		if limit := sharedFileEffectiveLineLimit(relPath); lines > limit {
			violations = append(violations, fmt.Sprintf("%s has %d effective lines > limit %d", relPath, lines, limit))
		}
		for _, imp := range parseImports(t, absPath) {
			prefix := internalPrefix("internal/module/")
			if imp == prefix || strings.HasPrefix(imp, prefix+"/") {
				violations = append(violations, fmt.Sprintf("%s imports %s", relPath, imp))
			}
		}
	}
	if total > sharedTotalEffectiveLineLimit() {
		violations = append(violations, fmt.Sprintf("internal/platform/shared has %d effective lines > limit %d", total, sharedTotalEffectiveLineLimit()))
	}
	failIfViolations(t, violations)
}

func TestSharedBudgetEffectiveLinesIgnoreComments(t *testing.T) {
	src := []byte(`package sample

// Package-level explanatory comment.
// another comment
func run() {
	// line comment inside function
	value := 1 // trailing comments stay on a code line
	/*
	   block comment only
	*/
	_ = value
}
`)
	if got, want := archtest.CountEffectiveLines(src), 5; got != want {
		t.Fatalf("CountEffectiveLines() = %d, want %d", got, want)
	}
}

func sharedFileEffectiveLineLimit(relPath string) int {
	if baseline, ok := sharedFileEffectiveLineBaselines[relPath]; ok && baseline > sharedFileEffectiveLineTarget {
		return baseline
	}
	return sharedFileEffectiveLineTarget
}

func sharedTotalEffectiveLineLimit() int {
	if sharedTotalEffectiveLineBaseline > sharedTotalEffectiveLineTarget {
		return sharedTotalEffectiveLineBaseline
	}
	return sharedTotalEffectiveLineTarget
}
