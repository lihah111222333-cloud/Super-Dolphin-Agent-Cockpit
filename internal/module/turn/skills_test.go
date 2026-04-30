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

// TestNormalizeSkillNames_EmptyInputReturnsNil 锁定空输入路径：不产生任何 ref，避免空名进入下游。
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

// TestApplyHydration_HydratesSummaryAndVersionForBareRef 锁定 applyHydration 给
// 仅含 Name 的 ref 补出 Summary + Version；Source=Unspecified 不被改写。
func TestApplyHydration_HydratesSummaryAndVersionForBareRef(t *testing.T) {
	svc := &service{}
	index := map[string]skillpkg.SkillInfo{
		"alpha": {Name: "alpha", Dir: "/tmp/skills/alpha", Summary: "summary-a", ContentHash: "hash-aaaaaaaaaaaa", Trust: skillpkg.TrustUser},
	}
	ref := dto.SkillRef{Name: "alpha"}
	out := svc.applyHydration(context.Background(), ref, index, skillHydrationPolicy{})
	if out.Summary != "summary-a" || out.Version == "" {
		t.Fatalf("hydration fields not populated: %+v", out)
	}
	if out.Source != dto.SkillSourceUnspecified {
		t.Fatalf("Source = %q, want legacy unspecified preserved", out.Source)
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
