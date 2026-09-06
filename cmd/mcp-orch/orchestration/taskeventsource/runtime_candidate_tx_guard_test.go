package taskeventsource

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestRuntimeEventTxCandidateSurfaceRemainsTransportNeutral 防止候选引入 Fx、provider、unified、Room 或 PG owner。
func TestRuntimeEventTxCandidateSurfaceRemainsTransportNeutral(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(currentFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read candidate directory: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if err := validateRuntimeEventTxCandidateImports(filepath.Join(dir, entry.Name())); err != nil {
			t.Fatal(err)
		}
	}
}

// validateRuntimeEventTxCandidateImports 校验候选生产文件只保留 transport-neutral 依赖。
func validateRuntimeEventTxCandidateImports(path string) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return err
		}
		if forbidden := runtimeEventTxForbiddenImport(imported); forbidden != "" {
			return &runtimeEventTxCandidateImportViolation{path: path, imported: imported, forbidden: forbidden}
		}
	}
	return nil
}

func runtimeEventTxForbiddenImport(imported string) string {
	for _, forbidden := range []string{"go.uber.org/fx", "/provider", "/unified", "room", "pgx", "postgres"} {
		if strings.Contains(strings.ToLower(imported), forbidden) {
			return forbidden
		}
	}
	return ""
}

// TestRuntimeEventTxCandidateIsNotWiredIntoProduction 保证 candidate-only 类型不会进入 Fx/provider/tool 生产入口。
func TestRuntimeEventTxCandidateIsNotWiredIntoProduction(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../../"))
	candidateDir := filepath.Clean(filepath.Join(root, "cmd/mcp-orch/orchestration/taskeventsource"))
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		if err := scanRuntimeEventTxCandidateTree(filepath.Join(root, tree), candidateDir); err != nil {
			t.Fatalf("candidate production wiring in %s: %v", tree, err)
		}
	}
}

func scanRuntimeEventTxCandidateTree(root, candidateDir string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || strings.HasPrefix(filepath.Clean(path), candidateDir+string(filepath.Separator)) {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(contents)
		if strings.Contains(text, "NewRuntimeEventTxCandidate(") || strings.Contains(text, "RuntimeEventTxCandidate{") {
			return &runtimeEventTxCandidateWiringViolation{path: path}
		}
		return nil
	})
}

type runtimeEventTxCandidateWiringViolation struct{ path string }

func (v *runtimeEventTxCandidateWiringViolation) Error() string {
	return "transaction-bound runtime event candidate referenced by " + v.path
}

type runtimeEventTxCandidateImportViolation struct {
	path, imported, forbidden string
}

func (v *runtimeEventTxCandidateImportViolation) Error() string {
	return v.path + " imports forbidden candidate dependency " + v.imported + " (matches " + v.forbidden + ")"
}
