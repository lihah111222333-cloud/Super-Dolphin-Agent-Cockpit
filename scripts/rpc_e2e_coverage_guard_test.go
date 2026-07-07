package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	path := filepath.Join("..", "docs", "internal-notes", "RPC能力E2E覆盖矩阵.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read RPC E2E coverage matrix: %v", err)
	}
	return string(data)
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

func countMatrixMethodRows(matrix string) int {
	count := 0
	for line := range strings.SplitSeq(matrix, "\n") {
		if strings.HasPrefix(line, "| `") {
			count++
		}
	}
	return count
}
