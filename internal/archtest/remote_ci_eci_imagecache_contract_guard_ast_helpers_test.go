package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// remoteCIProductionSnapshotEntry 串行化候选根目录的首次快照加载。
// archtest 在一个进程内运行大量源码守卫，必须共享不可变解析视图，不能
// 让每个守卫竞争执行自己的 WalkDir/parser 扫描。
type remoteCIProductionSnapshotEntry struct {
	once     sync.Once
	snapshot *remoteCIProductionSnapshot
	err      error
}

// remoteCIProductionSnapshot 保存候选生产源码的只读字节和 AST 集合。
type remoteCIProductionSnapshot struct {
	root  string
	files []productionSourceFile
}

var (
	// archguard:ignore global_vars -- immutable snapshot registries are shared by parallel guards
	remoteCIProductionSnapshots sync.Map // map[string]*remoteCIProductionSnapshotEntry
	// archguard:ignore global_vars -- immutable snapshot registries are shared by parallel guards
	remoteCIProductionFileIndex sync.Map // map[string]*productionSourceFile
	// archguard:ignore global_vars -- immutable snapshot registries are shared by parallel guards
	remoteCIProductionSnapshotBuilds atomic.Int64
)

// remoteCIProductionFiles and the following helpers are shared by the
// remote-CI contract guards; keeping AST traversal separate leaves each guard
// file focused on one contract surface.
func remoteCIProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	snapshot := loadRemoteCIProductionSnapshot(t, root)
	paths := make([]string, 0, len(snapshot.files))
	for _, file := range snapshot.files {
		paths = append(paths, filepath.Join(snapshot.root, filepath.FromSlash(file.relPath)))
	}
	return paths
}

func remoteCIContractConsumerFiles(t *testing.T, root string) []string {
	t.Helper()
	snapshot := loadRemoteCIProductionSnapshot(t, root)
	paths := make([]string, 0, len(snapshot.files))
	for _, file := range snapshot.files {
		if !productionSourcePathInRoots(file.relPath, []string{
			"cmd/super-dolphin-gate",
			"internal/devtools/gate",
			"internal/devtools/remoteci",
			"internal/devtools/alicloud/eci",
			"internal/devtools/alicloud/oss",
		}) {
			continue
		}
		paths = append(paths, filepath.Join(snapshot.root, filepath.FromSlash(file.relPath)))
	}
	return paths
}

// loadRemoteCIProductionSnapshot 为每个候选根目录只读一次并解析可执行
// remote-CI 源文件；返回的 AST 和字节按约定不可变，可供守卫并发读取。
func loadRemoteCIProductionSnapshot(t *testing.T, root string) *remoteCIProductionSnapshot {
	t.Helper()
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("resolve remote CI production root %q: %v", root, err)
	}
	entry := &remoteCIProductionSnapshotEntry{}
	actual, _ := remoteCIProductionSnapshots.LoadOrStore(filepath.Clean(absoluteRoot), entry)
	entry = actual.(*remoteCIProductionSnapshotEntry)
	entry.once.Do(func() {
		remoteCIProductionSnapshotBuilds.Add(1)
		files := make([]productionSourceFile, 0)
		for _, scanRoot := range []string{
			"cmd/super-dolphin-gate",
			"internal/devtools/remoteci",
			"internal/devtools/alicloud/eci",
			"internal/devtools/alicloud/oss",
			"internal/devtools/gate",
		} {
			if err := appendProductionSourceRoot(absoluteRoot, scanRoot, DefaultSkipDirs(), &files); err != nil {
				entry.err = err
				return
			}
		}
		sort.Slice(files, func(left, right int) bool { return files[left].relPath < files[right].relPath })
		entry.snapshot = &remoteCIProductionSnapshot{root: filepath.Clean(absoluteRoot), files: files}
		for index := range files {
			filePath := filepath.Clean(filepath.Join(absoluteRoot, filepath.FromSlash(files[index].relPath)))
			remoteCIProductionFileIndex.Store(filePath, &entry.snapshot.files[index])
		}
	})
	if entry.err != nil {
		t.Fatalf("load remote CI production source snapshot: %v", entry.err)
	}
	if entry.snapshot == nil {
		t.Fatal("load remote CI production source snapshot returned no snapshot")
	}
	return entry.snapshot
}

func parseRemoteCIContractGuardFile(t *testing.T, path string) *ast.File {
	t.Helper()
	if cached, ok := remoteCIProductionSourceFile(path); ok && cached.syntax != nil {
		return cached.syntax
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}

// remoteCIProductionSourceFile 从已注册的不可变快照索引返回源码文件。
func remoteCIProductionSourceFile(path string) (*productionSourceFile, bool) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, false
	}
	value, ok := remoteCIProductionFileIndex.Load(filepath.Clean(absolutePath))
	if !ok {
		return nil, false
	}
	file, ok := value.(*productionSourceFile)
	return file, ok && file != nil
}

// TestRemoteCIProductionSnapshotCachesOneParsePerRoot 验证同一根目录的
// 并发守卫只构建一次快照，并复用同一个 AST 指针。
func TestRemoteCIProductionSnapshotCachesOneParsePerRoot(t *testing.T) {
	root := newRemoteCIProductionSnapshotFixture(t)
	before := remoteCIProductionSnapshotBuilds.Load()
	const concurrentLoads = 8
	snapshots := make([]*remoteCIProductionSnapshot, concurrentLoads)
	t.Run("parallel-loads", func(t *testing.T) {
		for index := range snapshots {
			index := index
			t.Run("load", func(t *testing.T) {
				t.Parallel()
				snapshots[index] = loadRemoteCIProductionSnapshot(t, root)
			})
		}
	})
	var first *remoteCIProductionSnapshot
	for _, snapshot := range snapshots {
		if first == nil {
			first = snapshot
			continue
		}
		if first != snapshot {
			t.Fatal("remote CI production snapshot was rebuilt for the same root")
		}
	}
	if got := remoteCIProductionSnapshotBuilds.Load() - before; got != 1 {
		t.Fatalf("remote CI production snapshot builds = %d, want 1", got)
	}
	paths := remoteCIProductionFiles(t, root)
	if len(paths) != 1 {
		t.Fatalf("snapshot production paths = %#v, want one fixture", paths)
	}
	if firstFile := parseRemoteCIContractGuardFile(t, paths[0]); firstFile != parseRemoteCIContractGuardFile(t, paths[0]) {
		t.Fatal("remote CI production AST was reparsed instead of reused")
	}
}

// newRemoteCIProductionSnapshotFixture 建立只含一个 Go 源文件的快照测试根目录。
func newRemoteCIProductionSnapshotFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	productionPath := filepath.Join(root, "internal", "devtools", "gate", "snapshot_fixture.go")
	if err := os.MkdirAll(filepath.Dir(productionPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(productionPath, []byte("package gate\nfunc SnapshotFixture() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{
		"cmd/super-dolphin-gate",
		"internal/devtools/remoteci",
		"internal/devtools/alicloud/eci",
		"internal/devtools/alicloud/oss",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func remoteCIForbiddenIdentifiers(file *ast.File) map[string]bool {
	found := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier != nil {
			found[identifier.Name] = true
		}
		return true
	})
	return found
}

func remoteCIStringLiterals(file *ast.File) []string {
	var values []string
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			values = append(values, strings.Trim(literal.Value, "\"`"))
		}
		return true
	})
	return values
}

func remoteCIImportsContractOwner(file *ast.File) bool {
	for _, importSpec := range file.Imports {
		if strings.Trim(importSpec.Path.Value, "\"") == "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract" {
			return true
		}
	}
	return false
}

func remoteCIRepeatsContractValue(file *ast.File) bool {
	for _, literal := range remoteCIStringLiterals(file) {
		switch literal {
		case "linux/amd64", "claimed", "building", "cache_preparing", "ready_validated", "promoted", "retiring", "failed":
			return true
		}
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		if remoteCIContractDuration(node) {
			found = true
			return false
		}
		return true
	})
	return found
}

func remoteCIContractDuration(node ast.Node) bool {
	expression, ok := node.(*ast.BinaryExpr)
	if !ok || expression.Op != token.MUL {
		return false
	}
	literal, ok := expression.X.(*ast.BasicLit)
	if !ok || literal.Kind != token.INT {
		return false
	}
	selector, ok := expression.Y.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	contractUnit := (literal.Value == "2" && selector.Sel.Name == "Hour") || (literal.Value == "100" && selector.Sel.Name == "Second")
	return remoteCIAll(remoteCIExpressionName(selector.X) == "time", contractUnit)
}

const remoteCISQLCreateTablePattern = `(?i)\bCREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?\s+([A-Za-z_][A-Za-z0-9_]*)`

// remoteCISQLSchemaTableNames 提取 gate schema 中所有 CREATE TABLE 的物理表名。
func remoteCISQLSchemaTableNames(source string) []string {
	seen := make(map[string]struct{})
	for _, match := range regexp.MustCompile(remoteCISQLCreateTablePattern).FindAllStringSubmatch(source, -1) {
		if len(match) == 2 {
			seen[match[1]] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// remoteCIUnregisteredSQLSchemaTables 返回 schema 中未被 cicontract 登记的额外表。
func remoteCIUnregisteredSQLSchemaTables(source string) []string {
	registered := make(map[string]struct{}, len(cicontract.SQLAuthoritySchemaTables()))
	for _, table := range cicontract.SQLAuthoritySchemaTables() {
		registered[table] = struct{}{}
	}
	var violations []string
	for _, table := range remoteCISQLSchemaTableNames(source) {
		if _, ok := registered[table]; !ok {
			violations = append(violations, table)
		}
	}
	return violations
}

func remoteCIAll(values ...bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

func remoteCIUsesFilesystemStateStore(file *ast.File) bool {
	used := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !remoteCIFileStoreCall(call) {
			return true
		}
		used = true
		return false
	})
	return used
}

func remoteCIFileStoreCall(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "os" {
		return false
	}
	switch selector.Sel.Name {
	case "ReadFile", "WriteFile", "Open", "OpenFile", "Create":
		return true
	default:
		return false
	}
}

func isECICreateRequest(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "CreateRequest" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "eci"
}

func remoteCICreateRequestHasSnapshot(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if ok && key.Name == "ImageCacheSnapshotID" {
			return true
		}
	}
	return false
}

func remoteCIFunctionCalls(file *ast.File, functionName, calledName string) bool {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != functionName {
			continue
		}
		return remoteCIFunctionCallCount(&ast.File{Decls: []ast.Decl{function}}, calledName) != 0
	}
	return false
}
