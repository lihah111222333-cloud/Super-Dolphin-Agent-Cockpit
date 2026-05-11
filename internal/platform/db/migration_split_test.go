package db

import (
	"strings"
	"testing"
)

// splitMigrationBody 拆段单测。
//
// 用例覆盖：
//   - 无 sentinel：保持单段（行为与历史 pool.Exec(全文) 一致）。
//   - 有 sentinel：拆为两段，每段原样保留（含原 SQL + 注释）。
//   - 多个 sentinel：拆为多段，跳过空段（连续 sentinel 之间无内容）。
//   - sentinel 在文件开头/结尾：前后空段被跳过。

func TestSplitMigrationBody_NoSentinel_KeepsSingleSegment(t *testing.T) {
	body := "BEGIN;\nALTER TABLE x ADD COLUMN y INT;\nCOMMIT;\n"
	got := splitMigrationBody(body)
	if len(got) != 1 {
		t.Fatalf("no-sentinel: got %d segments, want 1", len(got))
	}
	if got[0] != body {
		t.Fatalf("no-sentinel: segment[0] not preserved; got %q want %q", got[0], body)
	}
}

func TestSplitMigrationBody_OneSentinel_SplitsIntoTwo(t *testing.T) {
	body := `BEGIN;
ALTER TABLE x ADD COLUMN y INT;
COMMIT;

-- SPLIT --

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_x_y ON x (y);
`
	got := splitMigrationBody(body)
	if len(got) != 2 {
		t.Fatalf("one-sentinel: got %d segments, want 2; segments=%v", len(got), got)
	}
	if !strings.Contains(got[0], "BEGIN;") || !strings.Contains(got[0], "COMMIT;") {
		t.Fatalf("one-sentinel: seg0 missing BEGIN/COMMIT: %q", got[0])
	}
	if !strings.Contains(got[1], "CREATE INDEX CONCURRENTLY") {
		t.Fatalf("one-sentinel: seg1 missing CREATE INDEX CONCURRENTLY: %q", got[1])
	}
	if strings.Contains(got[1], "BEGIN;") {
		t.Fatalf("one-sentinel: seg1 leaked BEGIN; into tx-free segment: %q", got[1])
	}
}

func TestSplitMigrationBody_MultipleSentinels_DropsEmptySegments(t *testing.T) {
	// 连续 sentinel 之间无内容（仅空白）应被跳过。
	body := "ALTER TABLE x ADD COLUMN a INT;\n-- SPLIT --\n   \n-- SPLIT --\nCREATE INDEX CONCURRENTLY i1 ON x(a);\n"
	got := splitMigrationBody(body)
	if len(got) != 2 {
		t.Fatalf("multi-sentinel: got %d segments, want 2 (empty middle dropped)", len(got))
	}
}

func TestSplitMigrationBody_LeadingTrailingSentinel_IgnoresEmpties(t *testing.T) {
	body := "-- SPLIT --\nALTER TABLE x ADD COLUMN a INT;\n-- SPLIT --\n"
	got := splitMigrationBody(body)
	if len(got) != 1 {
		t.Fatalf("leading/trailing sentinel: got %d segments, want 1", len(got))
	}
	if !strings.Contains(got[0], "ALTER TABLE") {
		t.Fatalf("leading/trailing sentinel: lone segment lost content: %q", got[0])
	}
}
