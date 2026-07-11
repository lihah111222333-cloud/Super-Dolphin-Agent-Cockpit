package turn

import (
	"context"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
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
		{name: "same-name scoped ref keeps project identity", ref: dto.SkillRef{Name: "Foo", Scope: "project", Path: "/repo/.agent/skills/foo"}, wantKey: "ref:project::foo:/repo/.agent/skills/foo@"},
		{name: "explicit UI key wins over name", ref: dto.SkillRef{Key: "personal:user:foo:/home/skills/foo", Name: "Foo"}, wantKey: "key:personal:user:foo:/home/skills/foo@"},
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
			{Name: "alpha", Version: "1", Prompt: "body-a1", Summary: "summary-a1"},
			{Name: "ALPHA", Version: "1", Prompt: "body-a1-dup"},
			{Name: "alpha", Version: "2", Prompt: "body-a2", Summary: "summary-a2"},
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
	if a1.Prompt != "" || a1.Summary != "summary-a1" {
		t.Fatalf("alpha@1 metadata mismatch: %+v", a1)
	}
	a2, ok := byKey["alpha@2"]
	if !ok {
		t.Fatalf("missing alpha@2: %+v", out)
	}
	if a2.Prompt != "" || a2.Summary != "summary-a2" {
		t.Fatalf("alpha@2 metadata mismatch: %+v", a2)
	}
	if _, ok := byKey["beta@"]; !ok {
		t.Fatalf("missing beta@: %+v", out)
	}
}

func TestNormalizeSkillRefsKeepsSameNameDifferentScopeRefs(t *testing.T) {
	out := normalizeSkillRefs([]dto.SkillRef{
		{Name: "Docs", Scope: "project", Path: "/repo/.agent/skills/docs"},
		{Name: "Docs", Scope: "personal", PersonalType: "user", Path: "/home/skills/docs"},
	})
	if len(out) != 2 {
		t.Fatalf("same-name scoped refs collapsed: %+v", out)
	}
	if out[0].Scope != "project" || out[1].Scope != "personal" {
		t.Fatalf("scoped refs not preserved: %+v", out)
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
	explicit := []dto.SkillRef{{Name: "tracer", Version: "1", Prompt: "explicit", Summary: "explicit summary"}}
	candidates := []dto.SkillRef{
		{Name: "tracer", Version: "1", Prompt: "auto-same"},
		{Name: "tracer", Version: "2", Prompt: "auto-diff", Summary: "auto summary"},
	}
	got := resolver.Resolve(explicit, candidates, "please use tracer on this run")
	if len(got) != 2 {
		t.Fatalf("want explicit v1 + auto v2, got %+v", got)
	}
	if got[0].Version != "1" || got[0].Prompt != "" || got[0].Summary != "explicit summary" {
		t.Fatalf("explicit v1 lost: %+v", got[0])
	}
	if got[1].Version != "2" || got[1].Prompt != "" || got[1].Summary != "auto summary" {
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
		SelectedRefs: []skillRefParams{{Key: "project::manual:/repo/.agent/skills/manual", Name: "manual", Scope: "project", Path: "/repo/.agent/skills/manual"}},
		Derived:      []string{"derived"},
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
	if byName["manual"].Scope != "project" || byName["manual"].Path != "/repo/.agent/skills/manual" {
		t.Fatalf("selected ref metadata lost: %+v", byName["manual"])
	}
	if byName["derived"].Source != dto.SkillSourceUnspecified {
		t.Fatalf("derived source = %q, want unspecified: %+v", byName["derived"].Source, refs)
	}

	legacy := normalizePrepareSkillRefs(prepareSkillSpec{Selected: []string{"legacy"}}, false)
	if len(legacy) != 1 || legacy[0].Source != dto.SkillSourceUnspecified {
		t.Fatalf("legacy selected should stay unspecified: %+v", legacy)
	}
}

func TestNormalizePrepareSkillRefs_PreservesExplicitRefSource(t *testing.T) {
	refs := normalizePrepareSkillRefs(prepareSkillSpec{
		SelectedRefs: []skillRefParams{
			{Key: "project::manual:/repo/.agent/skills/manual", Name: "manual", Scope: "project", Path: "/repo/.agent/skills/manual", Source: string(dto.SkillSourceManual)},
			{Key: "project::forced:/repo/.agent/skills/forced", Name: "forced", Scope: "project", Path: "/repo/.agent/skills/forced", Source: string(dto.SkillSourceForce)},
		},
	}, true)
	if len(refs) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(refs), refs)
	}
	byName := map[string]dto.SkillRef{}
	for _, ref := range refs {
		byName[ref.Name] = ref
	}
	if byName["manual"].Source != dto.SkillSourceManual {
		t.Fatalf("manual source = %q, want manual: %+v", byName["manual"].Source, refs)
	}
	if byName["forced"].Source != dto.SkillSourceForce {
		t.Fatalf("forced source = %q, want force: %+v", byName["forced"].Source, refs)
	}
}

func TestNormalizePrepareSkillRefsDropsNameOnlyDuplicatesCoveredByExplicitRefs(t *testing.T) {
	refs := normalizePrepareSkillRefs(prepareSkillSpec{
		Selected: []string{"Docs", "Other"},
		SelectedRefs: []skillRefParams{
			{Key: "project::docs:/repo/.agent/skills/docs", Name: "Docs", Scope: "project", Path: "/repo/.agent/skills/docs"},
			{Key: "personal:user:docs:/home/skills/docs", Name: "Docs", Scope: "personal", PersonalType: "user", Path: "/home/skills/docs"},
		},
	}, true)
	gotKeys := make([]string, 0, len(refs))
	for _, ref := range refs {
		gotKeys = append(gotKeys, skillDedupKey(ref))
	}
	wantKeys := []string{
		"key:project::docs:/repo/.agent/skills/docs@",
		"key:personal:user:docs:/home/skills/docs@",
		"other@",
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("normalizePrepareSkillRefs keys = %#v, want %#v; refs=%+v", gotKeys, wantKeys, refs)
	}
}

// TestApplyHydrationWithConflict_HydratesSummaryAndVersionForBareRef 锁定 hydration 给
// 仅含 Name 的 ref 补出 Summary + Version；Source=Unspecified 不被改写。
func TestApplyHydrationWithConflict_HydratesSummaryAndVersionForBareRef(t *testing.T) {
	svc := &service{}
	index := map[string]contract.SkillInfo{
		"alpha": {Name: "alpha", Dir: "/tmp/skills/alpha", Summary: "summary-a", ContentHash: "hash-aaaaaaaaaaaa", Trust: contract.TrustUser},
	}
	ref := dto.SkillRef{Name: "alpha"}
	out, _ := svc.applyHydrationWithConflict(context.Background(), ref, index, skillHydrationPolicy{})
	if out.Summary != "summary-a" || out.Version == "" {
		t.Fatalf("hydration fields not populated: %+v", out)
	}
	if out.Source != dto.SkillSourceUnspecified {
		t.Fatalf("Source = %q, want legacy unspecified preserved", out.Source)
	}
}

func TestApplyHydrationWithConflict_UntrustedSummary_RedactedWhenSourceUnspecified(t *testing.T) {
	svc := &service{}
	index := map[string]contract.SkillInfo{
		"project-skill": {
			Name:        "project-skill",
			Summary:     "SECRET PROJECT SUMMARY",
			ContentHash: "hash-project-summary",
			Trust:       contract.TrustProject,
		},
	}
	out, _ := svc.applyHydrationWithConflict(context.Background(), dto.SkillRef{Name: "project-skill"}, index, skillHydrationPolicy{ManualSkillSelection: true})
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

func TestApplyHydrationWithConflict_UntrustedSummary_AllowsOnlyRealManualSelection(t *testing.T) {
	svc := &service{}
	index := map[string]contract.SkillInfo{
		"project-skill": {
			Name:        "project-skill",
			Summary:     "manual approved summary",
			ContentHash: "hash-manual-summary",
			Trust:       contract.TrustProject,
		},
	}

	manual, _ := svc.applyHydrationWithConflict(context.Background(), dto.SkillRef{Name: "project-skill", Source: dto.SkillSourceManual}, index, skillHydrationPolicy{ManualSkillSelection: true})
	if manual.Summary != "manual approved summary" {
		t.Fatalf("real manual selection should allow summary: %+v", manual)
	}
	if manual.Source != dto.SkillSourceManual {
		t.Fatalf("manual source should be preserved: %+v", manual)
	}

	legacyManualSource, _ := svc.applyHydrationWithConflict(context.Background(), dto.SkillRef{Name: "project-skill", Source: dto.SkillSourceManual}, index, skillHydrationPolicy{})
	if legacyManualSource.Summary != "" {
		t.Fatalf("manual source without ManualSkillSelection flag must stay redacted: %+v", legacyManualSource)
	}
}

func TestApplyHydrationWithConflict_UntrustedSummary_RedactedForTriggerAndForce(t *testing.T) {
	svc := &service{}
	index := map[string]contract.SkillInfo{
		"project-skill": {
			Name:        "project-skill",
			Summary:     "SECRET AUTO SUMMARY",
			ContentHash: "hash-auto-summary",
			Trust:       contract.TrustProject,
		},
	}
	for _, source := range []dto.SkillSource{dto.SkillSourceTrigger, dto.SkillSourceForce} {
		out, _ := svc.applyHydrationWithConflict(context.Background(), dto.SkillRef{Name: "project-skill", Source: source}, index, skillHydrationPolicy{ManualSkillSelection: true})
		if out.Summary != "" {
			t.Fatalf("source %q should not expose untrusted summary: %+v", source, out)
		}
		if out.Source != source {
			t.Fatalf("source %q should be preserved, got %+v", source, out)
		}
	}
}
