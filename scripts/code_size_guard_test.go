//go:build ignore

package main

import "testing"

// ── effectiveLinesInRange CC guardrail ────────────────────────────────────────
// 锁定 effectiveLinesInRange 的所有分支路径行为，为 CC 14→≤13 重构提供回归保障。

type effectiveLinesInRangeSnapshotCase struct {
	name  string
	lines []string
	start int
	end   int
	want  int
}

var effectiveLinesInRangeSnapshotCases = []effectiveLinesInRangeSnapshotCase{
	{
		name:  "empty input",
		lines: nil,
		start: 1, end: -1,
		want: 0,
	},
	{
		name:  "all blank lines",
		lines: []string{"", "  ", "\t"},
		start: 1, end: -1,
		want: 0,
	},
	{
		name:  "pure code lines",
		lines: []string{"func main() {", "\tfmt.Println()", "}"},
		start: 1, end: -1,
		want: 3,
	},
	{
		name:  "line comment skipped",
		lines: []string{"// comment", "code", "// another"},
		start: 1, end: -1,
		want: 1,
	},
	{
		name:  "single-line block comment",
		lines: []string{"/* block */", "code"},
		start: 1, end: -1,
		want: 1,
	},
	{
		name:  "single-line block comment with trailing code",
		lines: []string{"/* comment */ x := 1"},
		start: 1, end: -1,
		want: 1,
	},
	{
		name:  "single-line block comment with trailing line comment",
		lines: []string{"/* comment */ // nope"},
		start: 1, end: -1,
		want: 0,
	},
	{
		name:  "multi-line block comment",
		lines: []string{"/* start", "middle", "end */", "code"},
		start: 1, end: -1,
		want: 1,
	},
	{
		name:  "multi-line block comment with trailing code after close",
		lines: []string{"/* start", "end */ code_here"},
		start: 1, end: -1,
		want: 1,
	},
	{
		name:  "multi-line block comment close followed by line comment",
		lines: []string{"/* start", "end */ // nope"},
		start: 1, end: -1,
		want: 0,
	},
	{
		name:  "range subset — start=2",
		lines: []string{"line1", "line2", "line3"},
		start: 2, end: 3,
		want: 2,
	},
	{
		name:  "range subset — start=2 end=2",
		lines: []string{"line1", "line2", "line3"},
		start: 2, end: 2,
		want: 1,
	},
	{
		name:  "end exceeds length — clamped",
		lines: []string{"code"},
		start: 1, end: 999,
		want: 1,
	},
	{
		name:  "mixed: code + blank + comment + block",
		lines: []string{"code1", "", "// comment", "/* block */", "code2"},
		start: 1, end: -1,
		want: 2,
	},
	{
		name:  "blank lines inside block comment",
		lines: []string{"/* start", "", "still in block", "end */"},
		start: 1, end: -1,
		want: 0,
	},
}

func Test_effectiveLinesInRange_snapshot(t *testing.T) {
	for _, tt := range effectiveLinesInRangeSnapshotCases {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveLinesInRange(tt.lines, tt.start, tt.end)
			if got != tt.want {
				t.Errorf("effectiveLinesInRange() = %d, want %d", got, tt.want)
			}
		})
	}
}
