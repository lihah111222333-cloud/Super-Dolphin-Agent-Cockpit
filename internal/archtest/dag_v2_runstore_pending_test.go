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

// DAG v2 骨架阶段 T0.5 (PC-1): RunStore 接口位待 T1.2 实现的提醒守护。
//
// 骨架阶段（commit 9130f601 / S3.5）在 store/taskdag/contract.go 加了 RunStore
// interface 但**未并入 Store 聚合**——产品 store 暂未 implement。这是预期的：
// T1.2 加 SQL 实现时再并入。问题：T1.2 worker 漏 implement，编译不会报错
// （没人 import RunStore），到 T1.2 集成测试才暴露。
//
// 本测试在 T1.2 落地前持续提醒：当 production store 仍未 implement RunStore
// 时，t.Log 一条 INFO（不 fail），让 CI 输出可见；T1.2 完成后此 t.Log 应消失，
// 改为正向断言"production *store implements RunStore"。
//
// 触发更新条件：T1.2 完成时，把 t.Log 改成正向断言并删除本注释。

const (
	taskdagStoreFile = "cmd/mcp-orch/store/taskdag/store.go"
	runStoreMethod1  = "CreateRun"
	runStoreMethod2  = "GetRun"
	runStoreMethod3  = "ListRuns"
)

func TestDAGv2_RunStore_T12_PendingImplementation(t *testing.T) {
	root := repoRoot(t)
	storePath := filepath.Join(root, taskdagStoreFile)

	src, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("read %s: %v", storePath, err)
	}

	implemented := hasAllMethods(t, storePath, src, runStoreMethod1, runStoreMethod2, runStoreMethod3)
	if !implemented {
		t.Logf("INFO (T0.5/PC-1): production *store 尚未 implement RunStore (CreateRun/GetRun/ListRuns); T1.2 落地时必须补齐. 接口契约见 docs/adr/0001-dag-v2-contracts.md §3 状态表 + cmd/mcp-orch/store/taskdag/contract.go::RunStore.")
		return
	}

	// 已实现 → T1.2 完成；本测试应改为正向断言。
	t.Errorf("T0.5 守护应在 T1.2 完成后改为正向断言；现在 production *store 已 implement RunStore，请把 t.Log 改成 var _ taskdag.RunStore = (*taskdag.Store)(nil) 形式")
}

// hasAllMethods 用 AST 扫 store.go，检查 *store 是否声明了全部给定方法名。
func hasAllMethods(t *testing.T, path string, src []byte, methods ...string) bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	found := make(map[string]bool, len(methods))
	for _, m := range methods {
		found[m] = false
	}
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			return true
		}
		// 收件人类型要是 *store
		if !isStoreReceiver(fn.Recv) {
			return true
		}
		if _, want := found[fn.Name.Name]; want {
			found[fn.Name.Name] = true
		}
		return true
	})
	for _, ok := range found {
		if !ok {
			return false
		}
	}
	return true
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
