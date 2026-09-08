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

// TestCandidateSupervisorRemainsUnwired 禁止候选owner符号进入生产组装；允许同包的只读receipt能力。
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

// scanCandidateTree 检查生产源码树是否使用候选owner，保留全部生产目录覆盖。
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
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return err
	}
	return findCandidateImport(path, file, candidateImport)
}

// skipCandidateSource 只筛掉目录、测试文件和 candidate 自身的生产树。
func skipCandidateSource(path string, entry os.DirEntry, candidateRoot string) bool {
	return entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || isCandidatePath(path, candidateRoot)
}

// findCandidateImport 按真实import别名定位候选owner引用，不把合法receipt导入误判成接线。
func findCandidateImport(path string, file *ast.File, candidateImport string) error {
	for _, imported := range file.Imports {
		name, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return err
		}
		if name != candidateImport {
			continue
		}
		alias := "managedruntime"
		if imported.Name != nil {
			alias = imported.Name.Name
		}
		if err := findCandidateReference(path, file, alias); err != nil {
			return err
		}
	}
	return nil
}

// findCandidateReference 阻止类型、构造器、函数别名或复合字面量形式的候选owner引用。
func findCandidateReference(path string, file *ast.File, alias string) error {
	var found error
	ast.Inspect(file, func(node ast.Node) bool {
		if found != nil {
			return false
		}
		if candidateReference(node, alias) {
			found = &candidateWiringError{path: path}
			return false
		}
		return true
	})
	return found
}

// candidateReference 识别包别名和dot导入，未知引用形状不会扩大候选owner权限。
func candidateReference(node ast.Node, alias string) bool {
	switch value := node.(type) {
	case *ast.SelectorExpr:
		owner, ok := value.X.(*ast.Ident)
		return ok && owner.Name == alias && isCandidateSymbol(value.Sel.Name)
	case *ast.Ident:
		return alias == "." && isCandidateSymbol(value.Name)
	default:
		return false
	}
}

// isCandidateSymbol 从候选类型与构造器真实命名界定生产禁入边界。
func isCandidateSymbol(name string) bool {
	return name == "CandidateSupervisor" || name == "MemorySupervisor" || name == "NewCandidateSupervisor" || name == "NewMemorySupervisor"
}

func isCandidatePath(path, candidateRoot string) bool {
	return path == candidateRoot || strings.HasPrefix(path, candidateRoot+string(filepath.Separator))
}

type candidateWiringError struct{ path string }

func (e *candidateWiringError) Error() string { return "managed runtime candidate is wired: " + e.path }

// TestCandidateWiringGuardDistinguishesReceipts 验证生产receipt可用，同时构造器/类型/别名/dot接线均阻断。
func TestCandidateWiringGuardDistinguishesReceipts(t *testing.T) {
	const imported = "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/managedruntime"
	for _, tc := range []struct {
		name, alias, expression string
		reject                  bool
	}{
		{"receipt", "mr", "var value mr.PreparedReceipt", false},
		{"verifier", "mr", "var factory = mr.NewProcessReceiptVerifier", false},
		{"constructor", "mr", "var factory = mr.NewCandidateSupervisor", true},
		{"memory-constructor", "mr", "var factory = mr.NewMemorySupervisor", true},
		{"type", "mr", "var owner *mr.CandidateSupervisor", true},
		{"memory-type", "mr", "var owner mr.MemorySupervisor", true},
		{"dot", ".", "var factory = NewCandidateSupervisor", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := "package fixture\nimport " + tc.alias + " " + strconv.Quote(imported) + "\n" + tc.expression
			file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			err = findCandidateImport("fixture.go", file, imported)
			if (err != nil) != tc.reject {
				t.Fatalf("candidate guard error=%v reject=%v", err, tc.reject)
			}
		})
	}
}
