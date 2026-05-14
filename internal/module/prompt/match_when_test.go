package prompt

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestMatchWhen_NilOrMalformed_NotMatch(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{CWD: "/any"}
	cases := map[string][]byte{
		"nil":        nil,
		"empty":      []byte(""),
		"null":       []byte("null"),
		"whitespace": []byte("  "),
		"malformed":  []byte("{broken"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if EvaluateMatchWhen(raw, ctx, "") {
				t.Fatalf("%s: expected NOT matched (opt-out), got true", name)
			}
		})
	}
}

func TestMatchWhen_EmptyObject_AlwaysMatch(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{CWD: "/tmp"}
	if !EvaluateMatchWhen([]byte(`{}`), ctx, "") {
		t.Fatalf("empty object should always match")
	}
}

func TestMatchWhen_CWDGlob(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{CWD: "/Users/mac/projects/data-lake"}
	if !EvaluateMatchWhen([]byte(`{"cwd_glob":"/Users/*/projects/data-*"}`), ctx, "") {
		t.Fatalf("glob should match /Users/*/projects/data-*")
	}
	if EvaluateMatchWhen([]byte(`{"cwd_glob":"/Users/*/webapp/*"}`), ctx, "") {
		t.Fatalf("glob must not match unrelated path")
	}
}

func TestMatchWhen_CWDPrefix(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{CWD: "/Users/mac/work/myrepo"}
	if !EvaluateMatchWhen([]byte(`{"cwd_prefix":"/Users/mac/work"}`), ctx, "") {
		t.Fatalf("prefix should match")
	}
	if EvaluateMatchWhen([]byte(`{"cwd_prefix":"/etc"}`), ctx, "") {
		t.Fatalf("prefix must not match unrelated root")
	}
}

func TestMatchWhen_TagsHas_CaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{}
	if !EvaluateMatchWhen([]byte(`{"tags_has":"review"}`), ctx, "帮我 Review 这个文件") {
		t.Fatalf("tags_has should match regardless of case")
	}
	if EvaluateMatchWhen([]byte(`{"tags_has":"deploy"}`), ctx, "帮我看看代码") {
		t.Fatalf("tags_has must not match absent keyword")
	}
	// 用户 DB 实际存的 match_when 是数组形式（SystemPromptPage UI 自动从 tag chip 生成）。
	// template-level tags_has 必须支持数组，否则 specific 池就算被两阶段评估
	// 选中也命中不了，等于修法 A 在生产 DB 上空跑。
	if !EvaluateMatchWhen([]byte(`{"tags_has":["代码","bug","review"]}`), ctx, "帮我 Review 这个文件") {
		t.Fatalf("tags_has array must hit when any element matches userPrompt substring")
	}
	if EvaluateMatchWhen([]byte(`{"tags_has":["代码","bug"]}`), ctx, "帮我看看天气") {
		t.Fatalf("tags_has array must miss when no element matches")
	}
}

func TestMatchWhen_SharedBuildCtxFields(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh", IsWorktree: true}
	if !EvaluateMatchWhen([]byte(`{"language":"zh"}`), ctx, "") {
		t.Fatalf("shared field language should match")
	}
	if !EvaluateMatchWhen([]byte(`{"isWorktree":true}`), ctx, "") {
		t.Fatalf("shared field isWorktree should match")
	}
	if EvaluateMatchWhen([]byte(`{"language":"en"}`), ctx, "") {
		t.Fatalf("mismatched language must not match")
	}
}

func TestMatchWhen_AndSemantics(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{
		CWD:      "/Users/mac/work/db",
		Language: "zh",
	}
	multi := []byte(`{"cwd_prefix":"/Users/mac/work","language":"zh"}`)
	if !EvaluateMatchWhen(multi, ctx, "") {
		t.Fatalf("all keys satisfied must match (AND)")
	}
	multiMiss := []byte(`{"cwd_prefix":"/Users/mac/work","language":"en"}`)
	if EvaluateMatchWhen(multiMiss, ctx, "") {
		t.Fatalf("one key mismatch must drop entire rule (AND)")
	}
}

func TestMatchWhen_UnknownKeyFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh"}
	if EvaluateMatchWhen([]byte(`{"does_not_exist":"x"}`), ctx, "") {
		t.Fatalf("unknown key must fail-closed")
	}
}

func TestMatchWhenKeyMatches_Cases(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{
		CWD:      "/Users/mac/work/myrepo",
		Language: "zh",
	}

	t.Run("cwd_glob", func(t *testing.T) {
		if !matchWhenKeyMatches("cwd_glob", "/Users/*/work/*", ctx, "") {
			t.Fatalf("cwd_glob should match")
		}
	})

	t.Run("cwd_prefix_empty_string", func(t *testing.T) {
		if matchWhenKeyMatches("cwd_prefix", "", ctx, "") {
			t.Fatalf("empty cwd_prefix must not match")
		}
	})

	t.Run("tags_has_case_insensitive", func(t *testing.T) {
		if !matchWhenKeyMatches("tags_has", "review", ctx, "帮我 Review 这个文件") {
			t.Fatalf("tags_has should match regardless of case")
		}
	})

	t.Run("falls_through_to_enable_when_field", func(t *testing.T) {
		if !matchWhenKeyMatches("language", "zh", ctx, "") {
			t.Fatalf("shared enable_when field should match via fallback")
		}
	})
}
