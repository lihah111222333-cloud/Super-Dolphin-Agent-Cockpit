package managedruntime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestCandidateSupervisorKeepsTheNarrowContract 锁定 candidate 只能实现四个合同方法。
func TestCandidateSupervisorKeepsTheNarrowContract(t *testing.T) {
	if got := reflect.TypeFor[Supervisor]().NumMethod(); got != 4 {
		t.Fatalf("Supervisor method count = %d, want 4", got)
	}
	if !reflect.TypeFor[*CandidateSupervisor]().Implements(reflect.TypeFor[Supervisor]()) {
		t.Fatal("CandidateSupervisor no longer implements Supervisor")
	}
}

// TestCandidateSupervisorImportsRemainEffectFree 防止 candidate 引入启动、持久化或运行时依赖。
func TestCandidateSupervisorImportsRemainEffectFree(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	path := filepath.Join(filepath.Dir(current), "supervisor.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse supervisor imports: %v", err)
	}
	allowed := map[string]struct{}{"context": {}, "errors": {}, "fmt": {}, "sync": {}}
	for _, imported := range file.Imports {
		name, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatalf("decode supervisor import: %v", err)
		}
		if _, ok := allowed[name]; !ok {
			t.Fatalf("supervisor.go imports non-candidate dependency %q", name)
		}
	}
}

// TestCandidateSupervisorRemainsUnwired 扫描生产源码，阻止 candidate 进入真实 runtime 组装。
func TestCandidateSupervisorRemainsUnwired(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(current), "../../../../"))
	candidateRoot := filepath.Clean(filepath.Join(repoRoot, "cmd/mcp-orch/standalone/managedruntime"))
	candidateImport := "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime"
	for _, tree := range []string{"cmd", "internal", "pkg"} {
		if err := scanCandidateTree(filepath.Join(repoRoot, tree), candidateRoot, candidateImport); err != nil {
			t.Fatal(err)
		}
	}
}

// scanCandidateTree 检查一棵生产源码树是否导入 managedruntime candidate。
func scanCandidateTree(root, candidateRoot, candidateImport string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		return inspectCandidateSource(path, entry, walkErr, candidateRoot, candidateImport)
	})
}

// inspectCandidateSource 跳过测试/候选目录并检查单个生产文件的 imports。
func inspectCandidateSource(path string, entry os.DirEntry, walkErr error, candidateRoot, candidateImport string) error {
	if walkErr != nil {
		return walkErr
	}
	if skipCandidateSource(path, entry, candidateRoot) {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return err
	}
	return findCandidateImport(path, file, candidateImport)
}

// skipCandidateSource 只筛掉目录、测试文件和 candidate 自身的生产树。
func skipCandidateSource(path string, entry os.DirEntry, candidateRoot string) bool {
	return entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || isCandidatePath(path, candidateRoot)
}

// findCandidateImport 返回发现真实 wiring 时的脱敏错误。
func findCandidateImport(path string, file *ast.File, candidateImport string) error {
	for _, imported := range file.Imports {
		name, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return err
		}
		if name == candidateImport {
			return &candidateWiringError{path: path}
		}
	}
	return nil
}

func isCandidatePath(path, candidateRoot string) bool {
	return path == candidateRoot || strings.HasPrefix(path, candidateRoot+string(filepath.Separator))
}

type candidateWiringError struct{ path string }

func (e *candidateWiringError) Error() string { return "managed runtime candidate is wired: " + e.path }
