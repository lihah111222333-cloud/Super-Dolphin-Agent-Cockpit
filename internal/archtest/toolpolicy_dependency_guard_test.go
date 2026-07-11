package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestToolPolicyDependencyGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	dir := filepath.Join(root, "internal", "platform", "toolpolicy")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	var violations []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		violations = append(violations, toolPolicyDependencyViolations(path, entry.Name())...)
	}
	if len(violations) > 0 {
		t.Fatalf("toolpolicy must stay a leaf stdlib package:\n%s", strings.Join(violations, "\n"))
	}
}

func toolPolicyDependencyViolations(path, name string) []string {
	node, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return []string{fmt.Sprintf("%s: parse imports: %v", name, err)}
	}

	var violations []string
	for _, spec := range node.Imports {
		imp, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: unquote import %s: %v", name, spec.Path.Value, err))
			continue
		}
		if strings.HasPrefix(imp, "github.com/lihah111222333-cloud/super-dolphin-agent/") {
			violations = append(violations, fmt.Sprintf("%s imports repo package %s", name, imp))
			continue
		}
		if strings.Contains(strings.Split(imp, "/")[0], ".") {
			violations = append(violations, fmt.Sprintf("%s imports non-stdlib package %s", name, imp))
		}
	}
	return violations
}
