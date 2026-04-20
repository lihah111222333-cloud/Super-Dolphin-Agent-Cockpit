package skill

import "testing"

func TestTrimInjectedSkillBlocks_GoldenCases(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "legacy",
			input: "hello\n[skill:go-testing]\n摘要: run go test\n使用方式: call skill_expand_body(\"go-testing\")\nbody",
			want:  "hello",
		},
		{
			name:  "full",
			input: "hello\n[skill:go-testing::full@v1]\nFULL BODY\n[/skill:go-testing::full@v1]\nfollow-up",
			want:  "hello\nfollow-up",
		},
		{
			name:  "summary",
			input: "hello\n[skill:rpc-tracing::summary@v1]\nTrace JSON-RPC flow.\n[/skill:rpc-tracing]\nask",
			want:  "hello\nask",
		},
		{
			name:  "expanded",
			input: "hello\n[skill:lint-go::expanded@v1]\n[expanded]\n[/skill:lint-go]\nnext",
			want:  "hello\nnext",
		},
		{
			name:  "mixed",
			input: "lead\n[skill:new::full@v1]\nbody\n[/skill:new]\nuser note\n[skill:old]\n摘要: a\n使用方式: b\nlegacy body",
			want:  "lead\nuser note",
		},
		{
			name:  "same-skill-reexpand",
			input: "before\n[skill:planner::summary@v1]\nshort summary\n[/skill:planner]\nbetween\n[skill:planner::expanded@v1]\nfull body\n[/skill:planner]\nafter",
			want:  "before\nbetween\nafter",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TrimInjectedSkillBlocks(tc.input); got != tc.want {
				t.Fatalf("TrimInjectedSkillBlocks() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTrimInjectedSkillBlocks_FailOpenMalformedV1(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "missing-close",
			input: "before\n[skill:foo::full@v1]\nbody without footer\nafter",
		},
		{
			name:  "mismatched-close",
			input: "before\n[skill:foo::full@v1]\nbody\n[/skill:bar]\nafter",
		},
		{
			name:  "legacy-lookalike",
			input: "before\n[skill:foo::full@v1]\n摘要: not enough\nstill user text",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := TrimInjectedSkillBlocks(tc.input); got != tc.input {
				t.Fatalf("fail-open mismatch: got %q want %q", got, tc.input)
			}
		})
	}
}

func TestTrimInjectedSkillBlocks_PreservesClaudeSkillsPrelude(t *testing.T) {
	t.Parallel()

	input := "skills:\n- planner\n\n[skill:planner]\n摘要: do planning\n使用方式: Call skill_expand_body(\"planner\") for full body"
	want := "skills:\n- planner"
	if got := TrimInjectedSkillBlocks(input); got != want {
		t.Fatalf("TrimInjectedSkillBlocks() = %q, want %q", got, want)
	}
}

func TestRenderSkillBlock_StillUsesCurrentWriterSyntax(t *testing.T) {
	t.Parallel()

	got, ok := RenderSkillBlock("planner", "full body", "", "full")
	want := "[skill:planner::full@v1]\nfull body\n[/skill:planner::full@v1]"
	if !ok || got != want {
		t.Fatalf("RenderSkillBlock() = (%q, %v), want (%q, true)", got, ok, want)
	}
}
