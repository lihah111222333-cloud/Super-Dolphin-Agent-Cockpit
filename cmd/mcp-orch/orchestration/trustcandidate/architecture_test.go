package trustcandidate

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestProductionImportsRemainStdlibOnly 保证 candidate 源码不导入 provider/store/Fx/network/UI。
func TestProductionImportsRemainStdlibOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := entry.Name()
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			importPath := strings.Trim(spec.Path.Value, `"`)
			if forbiddenTrustImport(importPath) {
				t.Fatalf("%s imports forbidden dependency %q", path, importPath)
			}
		}
	}
}

// TestCandidateIsNotWiredIntoProduction 扫描仓库源码，确认没有 runtime 接线 candidate 包。
func TestCandidateIsNotWiredIntoProduction(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve trustcandidate package")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "../../../.."))
	packageImport := "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/trustcandidate"
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		err := scanCandidateProduction(filepath.Join(root, dir), current, packageImport)
		if err != nil {
			if violation, ok := err.(wiringViolation); ok {
				t.Fatalf("candidate package is wired into production: %s", violation.path)
			}
			t.Fatalf("scan %s: %v", dir, err)
		}
	}
}

// scanCandidateProduction 扫描一个源码根，忽略测试文件并返回 wiring 违规。
func scanCandidateProduction(base, current, packageImport string) error {
	return filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if skipProductionPath(path, entry, current) {
			return nil
		}
		return checkCandidateImports(path, packageImport)
	})
}

// skipProductionPath 判断目录、非 Go 文件、测试文件和当前 guard 文件。
func skipProductionPath(path string, entry os.DirEntry, current string) bool {
	return entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == current
}

// checkCandidateImports 检查单个生产 Go 文件是否导入 candidate 包。
func checkCandidateImports(path, packageImport string) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, `"`) == packageImport {
			return wiringViolation{path: path}
		}
	}
	return nil
}

func forbiddenTrustImport(importPath string) bool {
	for _, token := range []string{"/provider", "/store", "/contentcrypto", "/contentobject", "go.uber.org/fx", "net/http", "/network", "/ui", "eventstore"} {
		if strings.Contains(importPath, token) {
			return true
		}
	}
	return strings.Contains(importPath, "github.com/")
}

type wiringViolation struct{ path string }

func (v wiringViolation) Error() string { return "candidate wired into production: " + v.path }
