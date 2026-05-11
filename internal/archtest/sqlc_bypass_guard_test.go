package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSQLCBypassGuardFlagsAccessor 是一个「提醒式」archtest（R3 P3 #4）。
//
// F4.1 阶段为了在 task_dag_ops.go 里跳过 sqlc 生成代码 hard 拿 *Queries.db，
// 在 cmd/mcp-orch/store/sqlc/db_accessor.go 加了一个 hand-written QueriesDB。
// 这是临时旁路：一旦 sqlc upgrade 能重生 出同样能力的公开接口，这个旁路应该
// 被删掉。本 archtest 在两个点上拦下忘记：
//
//  1. db_accessor.go 本身必须同时含「TODO」/「remove」/「regen」说明，它是临时
//     存在的。推动者看到 archtest 报错会马上明白它是 tech-debt。
//
//  2. store_dag_ops.go 里只能有唯一一处 sqlc.QueriesDB 调用（装在
//     sqlcDB helper 级别）。多出的调用说明旁路在扩散——应该促使 sqlc
//     upgrade 拼个明白。
//
// 两者并非「正确性 invariant」，而是「tech-debt visibility」护栏。什么时候可以
// 拿掉本文件：sqlc 生成能公开访问 DBTX（e.g. *Queries.DB()），且 store_dag_ops.go
// 完全不再依赖 QueriesDB 后。
func TestSQLCBypassGuardFlagsAccessor(t *testing.T) {
	t.Parallel()

	root := repoRootForGuardTests(t)

	// 1. db_accessor.go 存在 → 读它、要求含「TODO」/「remove」/「sqlc」提醒词。
	accessor := filepath.Join(root, "cmd", "mcp-orch", "store", "sqlc", "db_accessor.go")
	data, err := os.ReadFile(accessor)
	if err != nil {
		// 文件不在了 → 理想状态，逆向提醒作者同时拍掉本 archtest。
		t.Skipf("db_accessor.go absent — sqlc bypass appears removed; please also delete %s",
			filepath.Join("internal", "archtest", "sqlc_bypass_guard_test.go"))
		return
	}
	text := string(data)
	lower := strings.ToLower(text)
	needsAny := []string{"todo", "remove", "regen"}
	found := false
	for _, kw := range needsAny {
		if strings.Contains(lower, kw) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("%s: 未检出 TODO/remove/regen 提醒词。本文件是 sqlc-bypass tech-debt，\n"+
			"请保留 cleanup 提醒注释，避免被误以为是正常机制。\n"+
			"sqlc upgrade 完成后请一并删掉本文件 + accessor file + archtest。", accessor)
	}

	// 2. store_dag_ops.go 里 QueriesDB 调用 ≤ 1 个。独一调用点装在 sqlcDB helper。
	opsFile := filepath.Join(root, "cmd", "mcp-orch", "store", "taskdag", "store_dag_ops.go")
	opsBytes, err := os.ReadFile(opsFile)
	if err != nil {
		t.Fatalf("read store_dag_ops.go: %v", err)
	}
	calls := strings.Count(string(opsBytes), "sqlc.QueriesDB(")
	if calls > 1 {
		t.Errorf("%s: sqlc.QueriesDB 调用点 = %d (>1)。旁路在扩散，请考虑赋 sqlc 重生。", opsFile, calls)
	}
}
