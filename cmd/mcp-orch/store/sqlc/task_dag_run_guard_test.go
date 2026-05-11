package sqlc

import (
	"strings"
	"testing"
)

// TestAppendTaskDagRunEvent_UsesJsonbBuildArray 守住 R1 P0 #1 工艺修复：
// task_dag_runs.events 数组 append 必须用 jsonb_build_array() 把右操作数包成
// 数组，否则当 bind 端传 JSON object 时 PG `||` 会走 object-merge 而非 append，
// 历史链会被静默合并/覆盖。
//
// 本测试不连库，只对 sqlc 手维出的 SQL 常量做字符串断言；若未来 sqlc realignment
// 重新生成本文件，需在 sqlc.yaml schema 列表中确保 0083 等仍 included、然后
// 重新跑本测试确认 jsonb_build_array 没被洗掉。
func TestAppendTaskDagRunEvent_UsesJsonbBuildArray(t *testing.T) {
	if !strings.Contains(appendTaskDagRunEvent, "jsonb_build_array($2::jsonb)") {
		t.Fatalf("appendTaskDagRunEvent must wrap bind via jsonb_build_array() to force array-append semantics; got:\n%s", appendTaskDagRunEvent)
	}
	if strings.Contains(appendTaskDagRunEvent, "events     = events || $2::jsonb") {
		t.Fatalf("appendTaskDagRunEvent still uses raw `|| $2::jsonb` which silently does object-merge when bind sends an object; got:\n%s", appendTaskDagRunEvent)
	}
}
