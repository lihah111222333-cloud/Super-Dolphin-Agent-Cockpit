package main

// 本文件是公共跨平台的 E2E 覆盖静态门禁，不直接运行被审计的 E2E；
// 普通测试必须在每个平台发现覆盖漂移，因此故意不加 e2e build tag。

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	rpcRuntimeE2EMakeTarget = "test-e2e-rpc-runtime"
	rpcRuntimeE2EMakeRecipe = "$(TEST_WITH_GUARD) -tags=e2e ./internal/e2e/rpc_runtime -v -timeout 120s -count=1"
)

var rpcRuntimeE2ETestFuncRE = regexp.MustCompile(`(?m)^func\s+(TestAgentRuntimeRPC[A-Za-z0-9_]*)\s*\(`)

type rpcCapabilityRow struct {
	Line   int
	Family string
	Status string
	Target string
}

func TestRPCE2ECoverageMatrixTracksDiscoveredMethods(t *testing.T) {
	methods := discoveredRPCMethods(t)
	matrix := readRPCE2ECoverageMatrix(t)
	assertCoverageMatrixHasNoPlaceholders(t, matrix)

	for _, method := range methods {
		token := "`" + method + "`"
		if !strings.Contains(matrix, token) {
			t.Fatalf("coverage matrix missing discovered method %s", token)
		}
	}
}

func TestRPCE2ECoverageMatrixDoesNotStopAtSmoke(t *testing.T) {
	methods := discoveredRPCMethods(t)
	matrix := readRPCE2ECoverageMatrix(t)
	if len(methods) < 20 {
		t.Fatalf("discovered method count = %d, want broad runtime coverage", len(methods))
	}
	for _, method := range []string{
		"observability/status",
		"ctl/register",
		"dashboard/insights/list",
		"memory/consolidate",
		"thread/messages",
		"turn/interrupt",
		"ui/sidebar/get",
	} {
		if !strings.Contains(matrix, "`"+method+"`") {
			t.Fatalf("coverage matrix must include non-smoke method %q", method)
		}
	}
	if countMatrixMethodRows(matrix) <= 1 {
		t.Fatal("coverage matrix must not stop at a single smoke method")
	}
}

func TestRPCE2ECoverageMatrixEnforcesRuntimeTargetContract(t *testing.T) {
	matrix := readRPCE2ECoverageMatrix(t)
	rows := parseRPCCapabilityRows(t, matrix)
	assertCoverageMatrixStatuses(t, rows)

	makefile := readRepoFile(t, filepath.Join("..", "Makefile"))
	assertMakeTargetRunsRPCRuntimeE2E(t, makefile)

	runtimeTests := runtimeRPCE2ETestFunctions(t)
	for _, row := range rows {
		if row.Status != "covered" {
			continue
		}
		if !strings.HasPrefix(row.Target, "TestAgentRuntimeRPC") {
			t.Fatalf("coverage matrix line %d: covered row %s target %q must use TestAgentRuntimeRPC* naming", row.Line, row.Family, row.Target)
		}
		if row.Target == "TestAgentRuntimeRPCBlackBox" {
			t.Fatalf("coverage matrix line %d: covered row %s must not claim the generic smoke test as capability coverage", row.Line, row.Family)
		}
		if !runtimeTests[row.Target] {
			t.Fatalf("coverage matrix line %d: covered row %s points to missing runtime E2E test %q", row.Line, row.Family, row.Target)
		}
	}
}

func discoveredRPCMethods(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("go", "run", "scripts/extract_jsonrpc_methods.go")
	cmd.Dir = ".."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("extract discovered RPC methods: %v\n%s", err, string(out))
	}
	methods := nonEmptyLines(string(out))
	if len(methods) == 0 {
		t.Fatal("extract discovered RPC methods returned no methods")
	}
	return methods
}

func readRPCE2ECoverageMatrix(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, filepath.Join("..", "docs", "internal-notes", "RPC能力E2E覆盖矩阵.md"))
}

func assertCoverageMatrixHasNoPlaceholders(t *testing.T, matrix string) {
	t.Helper()
	for _, forbidden := range []string{
		"exact method list",
		"TODO",
		"TBD",
		"catch-all",
		"Any app.Module",
		"...",
	} {
		if strings.Contains(matrix, forbidden) {
			t.Fatalf("coverage matrix contains forbidden placeholder %q", forbidden)
		}
	}
}

func parseRPCCapabilityRows(t *testing.T, matrix string) []rpcCapabilityRow {
	t.Helper()
	var rows []rpcCapabilityRow
	inCoverageMatrix := false
	for i, line := range strings.Split(matrix, "\n") {
		lineNo := i + 1
		switch {
		case strings.HasPrefix(line, "## Coverage Matrix"):
			inCoverageMatrix = true
			continue
		case inCoverageMatrix && strings.HasPrefix(line, "## "):
			inCoverageMatrix = false
		}
		if !inCoverageMatrix || !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := splitMarkdownTableRow(t, lineNo, line)
		if len(cells) != 6 {
			t.Fatalf("coverage matrix line %d: got %d columns, want 6", lineNo, len(cells))
		}
		family := requireBacktickCell(t, lineNo, "capability family", cells[0])
		status := requireBacktickCell(t, lineNo, "status", cells[2])
		target := requireBacktickCell(t, lineNo, "black-box E2E target", cells[3])
		rows = append(rows, rpcCapabilityRow{
			Line:   lineNo,
			Family: family,
			Status: status,
			Target: target,
		})
	}
	if len(rows) == 0 {
		t.Fatal("coverage matrix has no capability rows")
	}
	return rows
}

func splitMarkdownTableRow(t *testing.T, lineNo int, line string) []string {
	t.Helper()
	row := strings.TrimSpace(line)
	if !strings.HasPrefix(row, "|") || !strings.HasSuffix(row, "|") {
		t.Fatalf("coverage matrix line %d: malformed markdown row %q", lineNo, line)
	}
	row = strings.TrimPrefix(strings.TrimSuffix(row, "|"), "|")
	rawCells := strings.Split(row, "|")
	cells := make([]string, 0, len(rawCells))
	for _, cell := range rawCells {
		cells = append(cells, strings.TrimSpace(cell))
	}
	return cells
}

func requireBacktickCell(t *testing.T, lineNo int, column string, cell string) string {
	t.Helper()
	if !strings.HasPrefix(cell, "`") || !strings.HasSuffix(cell, "`") || len(cell) < 2 {
		t.Fatalf("coverage matrix line %d: %s cell must be a single backtick token, got %q", lineNo, column, cell)
	}
	value := strings.TrimSuffix(strings.TrimPrefix(cell, "`"), "`")
	if strings.TrimSpace(value) == "" {
		t.Fatalf("coverage matrix line %d: %s cell is empty", lineNo, column)
	}
	return value
}

func assertCoverageMatrixStatuses(t *testing.T, rows []rpcCapabilityRow) {
	t.Helper()
	allowed := map[string]bool{
		"blocked":      true,
		"covered":      true,
		"desktop-only": true,
		"planned":      true,
	}
	for _, row := range rows {
		if !allowed[row.Status] {
			t.Fatalf("coverage matrix line %d: capability %s has invalid status %q", row.Line, row.Family, row.Status)
		}
		if row.Status != "desktop-only" && !strings.HasPrefix(row.Target, "TestAgentRuntimeRPC") {
			t.Fatalf("coverage matrix line %d: capability %s target %q must use TestAgentRuntimeRPC* naming", row.Line, row.Family, row.Target)
		}
	}
}

func assertMakeTargetRunsRPCRuntimeE2E(t *testing.T, makefile string) {
	t.Helper()
	lines := strings.Split(makefile, "\n")
	targetLine := rpcRuntimeE2EMakeTarget + ":"
	for i, line := range lines {
		if strings.TrimSpace(line) != targetLine {
			continue
		}
		for _, recipeLine := range lines[i+1:] {
			switch {
			case strings.TrimSpace(recipeLine) == "":
				continue
			case !strings.HasPrefix(recipeLine, "\t"):
				t.Fatalf("Makefile target %s does not run %q", targetLine, rpcRuntimeE2EMakeRecipe)
			case strings.TrimSpace(recipeLine) == rpcRuntimeE2EMakeRecipe:
				return
			}
		}
		t.Fatalf("Makefile target %s has no recipe", targetLine)
	}
	t.Fatalf("Makefile missing %s target", targetLine)
}

func runtimeRPCE2ETestFunctions(t *testing.T) map[string]bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "internal", "e2e", "rpc_runtime", "*_test.go"))
	if err != nil {
		t.Fatalf("glob runtime RPC E2E tests: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no runtime RPC E2E test files found")
	}
	tests := make(map[string]bool)
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read runtime RPC E2E test file %s: %v", path, err)
		}
		for _, match := range rpcRuntimeE2ETestFuncRE.FindAllStringSubmatch(string(data), -1) {
			tests[match[1]] = true
		}
	}
	if len(tests) == 0 {
		t.Fatal("no TestAgentRuntimeRPC* functions found in runtime RPC E2E tests")
	}
	return tests
}

func countMatrixMethodRows(matrix string) int {
	count := 0
	for line := range strings.SplitSeq(matrix, "\n") {
		if strings.HasPrefix(line, "| `") {
			count++
		}
	}
	return count
}
