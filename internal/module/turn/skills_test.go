package turn

import (
	"context"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
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

// TestNormalizeSkillNames_KeepsUnspecifiedMode 锁定普通选中技能路径保留
// Mode=Unspecified。这个空值是 provider-aware default marker：codexapp adapter
// 后续会切 Summary；legacy provider 仍通过 SkillMode.Effective() 得到 Full。
func TestNormalizeSkillNames_KeepsUnspecifiedMode(t *testing.T) {
	cases := []struct {
		name  string
		names []string
	}{
		{name: "single", names: []string{"alpha"}},
		{name: "multiple", names: []string{"alpha", "beta", "gamma"}},
		{name: "with whitespace", names: []string{"  alpha  ", "beta"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs := normalizeSkillNames(tc.names)
			if len(refs) == 0 {
				t.Fatalf("refs unexpectedly empty for input %v", tc.names)
			}
			for i, ref := range refs {
				if ref.Mode != dto.SkillModeUnspecified {
					t.Fatalf("refs[%d].Mode = %q, want Unspecified provider default marker", i, ref.Mode)
				}
			}
		})
	}
}

// TestNormalizeSkillNames_EmptyInputReturnsNil 锁定空输入路径：不产生任何 ref，
// 避免空名被付上默认 Mode 后蔦进下游。
func TestNormalizeSkillNames_EmptyInputReturnsNil(t *testing.T) {
	if got := normalizeSkillNames(nil); got != nil {
		t.Fatalf("nil input -> want nil refs, got %+v", got)
	}
	if got := normalizeSkillNames([]string{}, []string{"  ", ""}); got != nil {
		t.Fatalf("empty/whitespace input -> want nil refs, got %+v", got)
	}
}

func TestNormalizePrepareSkillRefs_SelectedManualOnlyWhenManualSelectionTrue(t *testing.T) {
	refs := normalizePrepareSkillRefs(prepareSkillSpec{
		Selected: []string{"manual"},
		Derived:  []string{"derived"},
	}, true)
	if len(refs) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(refs), refs)
	}
	byName := map[string]dto.SkillRef{}
	for _, ref := range refs {
		byName[ref.Name] = ref
	}
	if byName["manual"].Source != dto.SkillSourceManual {
		t.Fatalf("selected source = %q, want manual: %+v", byName["manual"].Source, refs)
	}
	if byName["derived"].Source != dto.SkillSourceUnspecified {
		t.Fatalf("derived source = %q, want unspecified: %+v", byName["derived"].Source, refs)
	}

	legacy := normalizePrepareSkillRefs(prepareSkillSpec{Selected: []string{"legacy"}}, false)
	if len(legacy) != 1 || legacy[0].Source != dto.SkillSourceUnspecified {
		t.Fatalf("legacy selected should stay unspecified: %+v", legacy)
	}
}

// TestApplyHydration_PreservesUnspecifiedMode 锁定 applyHydration 只补 Prompt/Summary/Version，
// 不把 Mode=Unspecified 物化为 Full。Unspecified 必须继续流到 provider adapter，
// 这样 codexapp 普通路径才能切 Summary。
func TestApplyHydration_PreservesUnspecifiedMode(t *testing.T) {
	svc := &service{}
	index := map[string]skillpkg.SkillInfo{
		"alpha": {Name: "alpha", Dir: "/tmp/skills/alpha", Summary: "summary-a", ContentHash: "hash-aaaaaaaaaaaa", Trust: skillpkg.TrustUser},
	}
	ref := dto.SkillRef{Name: "alpha"} // Mode 隐式为 Unspecified
	out := svc.applyHydration(context.Background(), ref, index, skillHydrationPolicy{})
	if out.Mode != dto.SkillModeUnspecified {
		t.Fatalf("Mode = %q, want Unspecified provider default marker", out.Mode)
	}
	if out.Summary != "summary-a" || out.Version == "" {
		t.Fatalf("hydration fields not populated: %+v", out)
	}
	if out.Source != dto.SkillSourceUnspecified {
		t.Fatalf("Source = %q, want legacy unspecified preserved", out.Source)
	}
}

// TestApplyHydration_PreservesExplicitMode 锁定：调用方显式赋了 Mode 的 ref 不被覆盖。
// 未来上游可能传入 Mode==Summary 的 ref（例如某些路径只走排序不走默认注入），
// 本测试防止兑底逻辑重复覆盖上游决策。
func TestApplyHydration_PreservesExplicitMode(t *testing.T) {
	svc := &service{}
	index := map[string]skillpkg.SkillInfo{
		"alpha": {Name: "alpha", Dir: "/tmp/skills/alpha", Summary: "summary-a", ContentHash: "hash-aaaaaaaaaaaa", Trust: skillpkg.TrustUser},
	}
	ref := dto.SkillRef{Name: "alpha", Mode: dto.SkillModeSummary}
	out := svc.applyHydration(context.Background(), ref, index, skillHydrationPolicy{})
	if out.Mode != dto.SkillModeSummary {
		t.Fatalf("explicit Mode overwritten: got %q want %q", out.Mode, dto.SkillModeSummary)
	}
}

func TestApplyHydration_UntrustedSummary_RedactedWhenSourceUnspecified(t *testing.T) {
	svc := &service{}
	index := map[string]skillpkg.SkillInfo{
		"project-skill": {
			Name:        "project-skill",
			Summary:     "SECRET PROJECT SUMMARY",
			ContentHash: "hash-project-summary",
			Trust:       skillpkg.TrustProject,
		},
	}
	out := svc.applyHydration(context.Background(), dto.SkillRef{Name: "project-skill"}, index, skillHydrationPolicy{ManualSkillSelection: true})
	if out.Summary != "" {
		t.Fatalf("untrusted unspecified summary leaked: %+v", out)
	}
	if out.Version != "hash-project" {
		t.Fatalf("Version should still hydrate for stable identity, got %+v", out)
	}
	if out.Source != dto.SkillSourceUnspecified {
		t.Fatalf("Source = %q, want legacy unspecified preserved", out.Source)
	}
}

func TestApplyHydration_UntrustedSummary_AllowsOnlyRealManualSelection(t *testing.T) {
	svc := &service{}
	index := map[string]skillpkg.SkillInfo{
		"project-skill": {
			Name:        "project-skill",
			Summary:     "manual approved summary",
			ContentHash: "hash-manual-summary",
			Trust:       skillpkg.TrustProject,
		},
	}

	manual := svc.applyHydration(context.Background(), dto.SkillRef{Name: "project-skill", Source: dto.SkillSourceManual}, index, skillHydrationPolicy{ManualSkillSelection: true})
	if manual.Summary != "manual approved summary" {
		t.Fatalf("real manual selection should allow summary: %+v", manual)
	}
	if manual.Source != dto.SkillSourceManual {
		t.Fatalf("manual source should be preserved: %+v", manual)
	}

	legacyManualSource := svc.applyHydration(context.Background(), dto.SkillRef{Name: "project-skill", Source: dto.SkillSourceManual}, index, skillHydrationPolicy{})
	if legacyManualSource.Summary != "" {
		t.Fatalf("manual source without ManualSkillSelection flag must stay redacted: %+v", legacyManualSource)
	}
}

func TestApplyHydration_UntrustedSummary_RedactedForTriggerAndForce(t *testing.T) {
	svc := &service{}
	index := map[string]skillpkg.SkillInfo{
		"project-skill": {
			Name:        "project-skill",
			Summary:     "SECRET AUTO SUMMARY",
			ContentHash: "hash-auto-summary",
			Trust:       skillpkg.TrustProject,
		},
	}
	for _, source := range []dto.SkillSource{dto.SkillSourceTrigger, dto.SkillSourceForce} {
		out := svc.applyHydration(context.Background(), dto.SkillRef{Name: "project-skill", Source: source}, index, skillHydrationPolicy{ManualSkillSelection: true})
		if out.Summary != "" {
			t.Fatalf("source %q should not expose untrusted summary: %+v", source, out)
		}
		if out.Source != source {
			t.Fatalf("source %q should be preserved, got %+v", source, out)
		}
	}
}
