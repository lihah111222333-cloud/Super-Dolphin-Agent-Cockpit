package prompt

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestEvaluateMatchWhen_NilOrMalformed_NotMatch(t *testing.T) {
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

func TestEvaluateMatchWhen_EmptyObject_AlwaysMatch(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{CWD: "/tmp"}
	if !EvaluateMatchWhen([]byte(`{}`), ctx, "") {
		t.Fatalf("empty object should always match")
	}
}

func TestEvaluateMatchWhen_CWDGlob(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{CWD: "/Users/mac/projects/data-lake"}
	if !EvaluateMatchWhen([]byte(`{"cwd_glob":"/Users/*/projects/data-*"}`), ctx, "") {
		t.Fatalf("glob should match /Users/*/projects/data-*")
	}
	if EvaluateMatchWhen([]byte(`{"cwd_glob":"/Users/*/webapp/*"}`), ctx, "") {
		t.Fatalf("glob must not match unrelated path")
	}
}

func TestEvaluateMatchWhen_CWDPrefix(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{CWD: "/Users/mac/work/myrepo"}
	if !EvaluateMatchWhen([]byte(`{"cwd_prefix":"/Users/mac/work"}`), ctx, "") {
		t.Fatalf("prefix should match")
	}
	if EvaluateMatchWhen([]byte(`{"cwd_prefix":"/etc"}`), ctx, "") {
		t.Fatalf("prefix must not match unrelated root")
	}
}

func TestEvaluateMatchWhen_TagsHas_CaseInsensitive(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{}
	if !EvaluateMatchWhen([]byte(`{"tags_has":"review"}`), ctx, "帮我 Review 这个文件") {
		t.Fatalf("tags_has should match regardless of case")
	}
	if EvaluateMatchWhen([]byte(`{"tags_has":"deploy"}`), ctx, "帮我看看代码") {
		t.Fatalf("tags_has must not match absent keyword")
	}
}

func TestEvaluateMatchWhen_SharedBuildCtxFields(t *testing.T) {
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

func TestEvaluateMatchWhen_AndSemantics(t *testing.T) {
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

func TestEvaluateMatchWhen_UnknownKeyFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := contract.BuildCtx{Language: "zh"}
	if EvaluateMatchWhen([]byte(`{"does_not_exist":"x"}`), ctx, "") {
		t.Fatalf("unknown key must fail-closed")
	}
}
