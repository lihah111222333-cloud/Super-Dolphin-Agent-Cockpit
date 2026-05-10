package archtest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DAG v2 T1.2 完成后的正向守护（从 T0.5/PC-1 转正）：production *store
// 必须实现 RunStore 接口的全部 5 个方法。缺任何一个则 fail。
//
// 背景：骨架阶段（commit 9130f601 / S3.5）在 contract.go 加了 RunStore
// interface 但产品 store 暂未 implement，需等 T1.2 补齐。旧版守护是实现不齐
// 时 t.Log、实现齐了反老 fail 提醒转正。T1.2 实现后正式转为正向断言。
//
// 覆盖范围：扫 cmd/mcp-orch/store/taskdag/ 下所有 .go（非 _test.go），
// 找 *store receiver 上声明的方法；RunStore 实现可以拆到 store_run.go
// 或任意其它同包文件。

var runStoreMethods = []string{
	"CreateRun",
	"GetRun",
	"ListRuns",
	"CountActiveRunsByDagKey",
	"PromoteRootNodesToReady",
}

func TestDAGv2_RunStore_T12_Implemented(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "cmd/mcp-orch/store/taskdag")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}

	found := make(map[string]string, len(runStoreMethods))
	for _, m := range runStoreMethods {
		found[m] = ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		collectStoreMethods(t, path, found)
	}

	var missing []string
	for _, method := range runStoreMethods {
		if found[method] == "" {
			missing = append(missing, method)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("production *store 未实现 RunStore 方法：%s\n接口契约见 cmd/mcp-orch/store/taskdag/contract.go::RunStore\n事实上该接口 5 个方法应在 store_run.go 全部完成（T1.2-mid）。",
			strings.Join(missing, ", "))
	}
}

// collectStoreMethods 用 AST 扫一个 .go 文件，为每个 *store receiver
// 上声明的 RunStore 方法记录其所在文件名（为了在 fail 消息里能跟踪）。
func collectStoreMethods(t *testing.T, path string, found map[string]string) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			return true
		}
		if !isStoreReceiver(fn.Recv) {
			return true
		}
		if _, want := found[fn.Name.Name]; want {
			found[fn.Name.Name] = filepath.Base(path)
		}
		return true
	})
}

func isStoreReceiver(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	expr := recv.List[0].Type
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	if !ok {
		return false
	}
	return strings.EqualFold(ident.Name, "store")
}
