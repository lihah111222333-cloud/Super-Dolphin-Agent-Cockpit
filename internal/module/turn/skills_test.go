package turn

import (
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestSkillDedupKey(t *testing.T) {
	cases := []struct {
		name    string
		ref     dto.SkillRef
		wantKey string
	}{
		{name: "empty name -> empty key", ref: dto.SkillRef{}, wantKey: ""},
		{name: "whitespace name -> empty key", ref: dto.SkillRef{Name: "   "}, wantKey: ""},
		{name: "bare name preserves legacy shape", ref: dto.SkillRef{Name: "Foo"}, wantKey: "foo@"},
		{name: "name + version", ref: dto.SkillRef{Name: "Foo", Version: "1.0.0"}, wantKey: "foo@1.0.0"},
		{name: "trims + lowercases name", ref: dto.SkillRef{Name: "  MySkill ", Version: "  v2 "}, wantKey: "myskill@v2"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := skillDedupKey(tc.ref); got != tc.wantKey {
				t.Fatalf("skillDedupKey(%+v) = %q, want %q", tc.ref, got, tc.wantKey)
			}
		})
	}
}

func TestNormalizeSkillRefsDedupesByNameAndVersion(t *testing.T) {
	out := normalizeSkillRefs(
		[]dto.SkillRef{
			{Name: "alpha", Version: "1", Prompt: "body-a1"},
			{Name: "ALPHA", Version: "1", Prompt: "body-a1-dup"},
			{Name: "alpha", Version: "2", Prompt: "body-a2"},
		},
		[]dto.SkillRef{
			{Name: "alpha", Version: "2", Prompt: "extra-a2"},
			{Name: "beta", Prompt: "body-beta"},
		},
	)
	if len(out) != 3 {
		t.Fatalf("want 3 entries, got %d: %+v", len(out), out)
	}

	byKey := map[string]dto.SkillRef{}
	for _, ref := range out {
		byKey[skillDedupKey(ref)] = ref
	}
	a1, ok := byKey["alpha@1"]
	if !ok {
		t.Fatalf("missing alpha@1: %+v", out)
	}
	if a1.Prompt != "body-a1\nbody-a1-dup" {
		t.Fatalf("alpha@1 merge mismatch: %q", a1.Prompt)
	}
	a2, ok := byKey["alpha@2"]
	if !ok {
		t.Fatalf("missing alpha@2: %+v", out)
	}
	if a2.Prompt != "body-a2\nextra-a2" {
		t.Fatalf("alpha@2 merge mismatch: %q", a2.Prompt)
	}
	if _, ok := byKey["beta@"]; !ok {
		t.Fatalf("missing beta@: %+v", out)
	}
}

func TestSkillResolverKeepsSameNameDifferentVersion(t *testing.T) {
	resolver := &skillResolver{}
	got := resolver.Resolve(
		[]dto.SkillRef{
			{Name: "foo", Version: "1", Prompt: "v1"},
			{Name: "foo", Version: "2", Prompt: "v2"},
		},
		nil,
		"",
	)
	if len(got) != 2 {
		t.Fatalf("expected both foo@1 and foo@2, got %+v", got)
	}
	if got[0].Version != "1" || got[1].Version != "2" {
		t.Fatalf("version order lost: %+v", got)
	}
}

func TestSkillResolverAutoMatchSkipsAlreadyExplicitVersion(t *testing.T) {
	resolver := &skillResolver{}
	explicit := []dto.SkillRef{{Name: "tracer", Version: "1", Prompt: "explicit"}}
	candidates := []dto.SkillRef{
		{Name: "tracer", Version: "1", Prompt: "auto-same"},
		{Name: "tracer", Version: "2", Prompt: "auto-diff"},
	}
	got := resolver.Resolve(explicit, candidates, "please use tracer on this run")
	if len(got) != 2 {
		t.Fatalf("want explicit v1 + auto v2, got %+v", got)
	}
	if got[0].Version != "1" || got[0].Prompt != "explicit" {
		t.Fatalf("explicit v1 lost: %+v", got[0])
	}
	if got[1].Version != "2" || got[1].Prompt != "auto-diff" {
		t.Fatalf("auto v2 lost: %+v", got[1])
	}
}

// ============================================================================
// P20.1 Phase 7 Step C: ApplyNativeSkillOverride
// ============================================================================

func TestApplyNativeSkillOverride_HitOverridesModeAndSource(t *testing.T) {
	refs := []dto.SkillRef{
		{Name: "foo", Mode: dto.SkillModeFull, Prompt: "body", Source: dto.SkillSourceManual},
		{Name: "bar", Mode: dto.SkillModeSummary, Summary: "s", Source: dto.SkillSourceTrigger},
	}
	out := ApplyNativeSkillOverride(refs, []string{"foo"})
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Mode != dto.SkillModeNone || out[0].Source != dto.SkillSourceNative {
		t.Fatalf("foo should be overridden: got %+v", out[0])
	}
	// 其他字段保留
	if out[0].Name != "foo" || out[0].Prompt != "body" {
		t.Fatalf("foo non-mode fields should be preserved: %+v", out[0])
	}
	// bar 未命中，保持原值
	if out[1].Mode != dto.SkillModeSummary || out[1].Source != dto.SkillSourceTrigger {
		t.Fatalf("bar should be untouched: %+v", out[1])
	}
}

func TestApplyNativeSkillOverride_EmptyNamesReturnsOriginal(t *testing.T) {
	refs := []dto.SkillRef{{Name: "foo", Mode: dto.SkillModeFull}}
	if out := ApplyNativeSkillOverride(refs, nil); len(out) != 1 || out[0].Mode != dto.SkillModeFull {
		t.Fatalf("nil nativeNames should no-op: %v", out)
	}
	if out := ApplyNativeSkillOverride(refs, []string{}); len(out) != 1 || out[0].Mode != dto.SkillModeFull {
		t.Fatalf("empty nativeNames should no-op: %v", out)
	}
	if out := ApplyNativeSkillOverride(refs, []string{"  ", ""}); len(out) != 1 || out[0].Mode != dto.SkillModeFull {
		t.Fatalf("whitespace-only nativeNames should no-op: %v", out)
	}
}

func TestApplyNativeSkillOverride_EmptyRefsReturnsOriginal(t *testing.T) {
	if out := ApplyNativeSkillOverride(nil, []string{"foo"}); out != nil {
		t.Fatalf("nil refs should return nil")
	}
	if out := ApplyNativeSkillOverride([]dto.SkillRef{}, []string{"foo"}); len(out) != 0 {
		t.Fatalf("empty refs: %v", out)
	}
}

func TestApplyNativeSkillOverride_CaseInsensitive(t *testing.T) {
	refs := []dto.SkillRef{{Name: "Foo-Bar", Mode: dto.SkillModeFull, Prompt: "b"}}
	out := ApplyNativeSkillOverride(refs, []string{"FOO-BAR"})
	if out[0].Mode != dto.SkillModeNone {
		t.Fatalf("case-insensitive match should override: %+v", out[0])
	}
}

func TestApplyNativeSkillOverride_DoesNotMutateInput(t *testing.T) {
	refs := []dto.SkillRef{{Name: "foo", Mode: dto.SkillModeFull}}
	_ = ApplyNativeSkillOverride(refs, []string{"foo"})
	// 原 slice 不应被修改
	if refs[0].Mode != dto.SkillModeFull {
		t.Fatalf("input slice MUST NOT be mutated: %+v", refs[0])
	}
}
