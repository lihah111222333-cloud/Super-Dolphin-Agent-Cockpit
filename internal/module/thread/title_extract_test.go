package thread

import (
	"testing"
)

func TestExtractTitle(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect string
	}{
		// @mention prefix is stripped; remainder kept as-is
		{"去@mention", "@头脑风暴，订单表优化", "订单表优化"},
		// "帮我" stripped by filler prefix, "一下" removed as particle
		{"去寒暄前缀", "帮我优化一下订单表", "优化订单表"},
		// "看看" stripped as filler prefix; backtick block removed; "这个函数" remains (3 units)
		{"去代码块", "看看 `func SetName()` 这个函数", "这个函数"},
		// sentence split on 。; first sentence "修复bug" taken
		{"取首句", "修复bug。然后跑测试", "修复bug"},
		// sentence split on ？; first sentence "对话框命名怎么运行的" → strip 的 → 9 CJK chars → truncate to 8
		{"问号分句", "对话框命名怎么运行的？后面还有内容", "对话框命名怎么运"},
		// technical terms preserved; no Chinese so no CJK particle stripping; 4 units (spawn.go + race + condition + 问题)
		{"技术词保留", "spawn.go race condition 问题", "spawn.go race condition 问题"},
		// no filler prefix match; particles 一下/的/了/吧 all stripped
		{"中文虚词丢弃", "看一下订单表的JOIN优化了吧", "看订单表JOIN优化"},
		// single CJK char → ≤2 units → fallback ""
		{"兜底太短", "好", ""},
		// single pronoun token → isAllPronouns → fallback ""
		{"兜底代词", "这个", ""},
		{"空字符串", "", ""},
		// pure English: stop words "the", "in" removed; "fix", "race", "condition", "spawn" kept
		{"纯英文", "fix the race condition in spawn", "fix race condition spawn"},
		// 问题1回归: filler "帮我" stripped before comma split → "优化订单表" not empty
		{"filler后逗号", "帮我，优化订单表", "优化订单表"},
		// 问题2: triple-backtick fenced block removed entirely
		{"三重反引号", "看看 ```go\nfunc Foo() {}\n``` 这个函数", "这个函数"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractTitle(tc.input)
			if got != tc.expect {
				t.Errorf("ExtractTitle(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestCountDisplayUnits(t *testing.T) {
	cases := []struct {
		input  string
		expect int
	}{
		// 订单 + 表 + JOIN + 优 + 化 = wait: CJK chars are individual units
		// 订(1)单(2)表(3) JOIN(4) 优(5)化(6) = 6
		{"订单表 JOIN 优化", 6},
		// spawn.go(1) race(2) condition(3) = 3
		{"spawn.go race condition", 3},
		{"hello", 1},
		// 你(1)好(2)世(3)界(4) = 4
		{"你好世界", 4},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := countDisplayUnits(tc.input)
			if got != tc.expect {
				t.Errorf("countDisplayUnits(%q) = %d, want %d", tc.input, got, tc.expect)
			}
		})
	}
}

func TestContinuationName(t *testing.T) {
	cases := []struct {
		input  string
		expect string
	}{
		{"订单表 JOIN 优化", "订单表 JOIN 优化 (续)"},
		{"订单表 JOIN 优化 (续)", "订单表 JOIN 优化 (续 2)"},
		{"订单表 JOIN 优化 (续 2)", "订单表 JOIN 优化 (续 3)"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := continuationName(tc.input)
			if got != tc.expect {
				t.Errorf("continuationName(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestDefaultName(t *testing.T) {
	got := defaultThreadName()
	if got != "新对话" {
		t.Errorf("defaultThreadName() = %q, want %q", got, "新对话")
	}
}
