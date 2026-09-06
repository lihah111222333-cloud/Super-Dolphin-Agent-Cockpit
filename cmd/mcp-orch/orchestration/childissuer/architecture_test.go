package childissuer

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestChildIssuerProductionRemainsTransportNeutral 验证候选不依赖 provider、持久化、框架或 unified/Room。
func TestChildIssuerProductionRemainsTransportNeutral(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		imports, err := productionImports(path)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imported := range imports {
			if forbiddenProductionImport(imported) {
				t.Fatalf("%s imports forbidden production dependency %q", path, imported)
			}
		}
	}
}

// TestChildIssuerCandidateIsNotWiredIntoProduction 扫描非测试源码，阻断候选接入 Fx、工具、provider 或 transport。
func TestChildIssuerCandidateIsNotWiredIntoProduction(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../../"))
	candidateImport := "/cmd/mcp-orch/orchestration/childissuer"
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		err := scanProductionTreeForCandidate(filepath.Join(root, tree), filepath.Dir(currentFile), candidateImport)
		if err != nil {
			if violation, ok := err.(*wiringViolation); ok {
				t.Fatalf("candidate is wired into production: %s imports %q", violation.path, violation.imported)
			}
			t.Fatalf("scan %s: %v", tree, err)
		}
	}
}

// scanProductionTreeForCandidate 扫描一个源码树并返回首个候选导入位置。
func scanProductionTreeForCandidate(root, candidateDir, candidateImport string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !isProductionSource(path, entry, candidateDir) {
			return nil
		}
		imports, err := productionImports(path)
		if err != nil {
			return err
		}
		if imported, ok := findCandidateImport(imports, candidateImport); ok {
			return &wiringViolation{path: path, imported: imported}
		}
		return nil
	})
}

// isProductionSource 判断路径是否为候选包之外的生产 Go 文件。
func isProductionSource(path string, entry os.DirEntry, candidateDir string) bool {
	if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
		return false
	}
	return !strings.HasPrefix(path, candidateDir+string(filepath.Separator))
}

// findCandidateImport 找到候选包的显式 production import。
func findCandidateImport(imports []string, candidateImport string) (string, bool) {
	for _, imported := range imports {
		if strings.HasSuffix(imported, candidateImport) {
			return imported, true
		}
	}
	return "", false
}

func productionImports(path string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		imports = append(imports, strings.Trim(spec.Path.Value, `"`))
	}
	return imports, nil
}

func forbiddenProductionImport(imported string) bool {
	for _, denied := range []string{
		"database/sql", "go.uber.org/fx", "github.com/creachadair/jrpc2",
		"/internal/provider", "/cmd/mcp-orch/store/sqlc", "/internal/store",
		"/internal/platform/db", "/standalone/managedruntime", "/orchestration/room",
		"/unified_v2",
	} {
		if strings.Contains(imported, denied) {
			return true
		}
	}
	return false
}

type wiringViolation struct {
	path     string
	imported string
}

func (v *wiringViolation) Error() string { return v.path + " imports " + v.imported }
