package archtest

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func findNakedGoroutineViolationsFromSnapshot(
	t *testing.T,
	snapshot *productionSourceSnapshot,
	scanRoots []string,
	allowedFiles map[string]struct{},
) []string {
	t.Helper()
	var violations []string
	for _, file := range snapshot.files {
		if !productionSourcePathInRoots(file.relPath, scanRoots) {
			continue
		}
		rel := filepath.FromSlash(file.relPath)
		if isAllowedForNakedGoroutine(rel, allowedFiles) {
			continue
		}
		count := CountNakedGoStmts(file.syntax)
		if count > 0 {
			violations = append(violations, file.relPath+": 发现 "+itoa(count)+" 处裸 go func() — 必须使用 safego.Go(ctx, logger, label, fn)")
		}
	}
	return violations
}

func nakedGoroutineAllowedFiles() map[string]struct{} {
	return map[string]struct{}{
		filepath.Join("internal", "util", "safego", "safego.go"):      {},
		filepath.Join("internal", "platform", "shared", "safe_go.go"): {},
		filepath.Join("internal", "contract", "contracttest"):         {}, // test helpers
	}
}

func nakedGoroutineViolationForFile(root, path string, allowedFiles map[string]struct{}) (string, bool, error) {
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return "", false, nil
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false, err
	}
	if isAllowedForNakedGoroutine(rel, allowedFiles) {
		return "", false, nil
	}
	fset := token.NewFileSet()
	node, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if parseErr != nil {
		return "", false, parseErr
	}
	count := CountNakedGoStmts(node)
	if count == 0 {
		return "", false, nil
	}
	return rel + ": 发现 " + itoa(count) + " 处裸 go func() — 必须使用 safego.Go(ctx, logger, label, fn)", true, nil
}

func isAllowedForNakedGoroutine(rel string, allowedFiles map[string]struct{}) bool {
	if _, ok := allowedFiles[rel]; ok {
		return true
	}
	for prefix := range allowedFiles {
		if strings.HasPrefix(rel, prefix+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
