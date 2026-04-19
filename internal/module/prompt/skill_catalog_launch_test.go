package prompt

import (
	"testing"

	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

// ============================================================================
// P20.4 — applyLaunchSkillSelection pin / force 测试
// ============================================================================

func info(name string) skillpkg.SkillInfo {
	return skillpkg.SkillInfo{Name: name, Trust: skillpkg.TrustUser}
}

func names(infos []skillpkg.SkillInfo) []string {
	out := make([]string, 0, len(infos))
	for _, i := range infos {
		out = append(out, i.Name)
	}
	return out
}

func TestApplyLaunchSkillSelection_EmptyNames_ReturnsInputUnchanged(t *testing.T) {
	in := []skillpkg.SkillInfo{info("a"), info("b"), info("c")}
	got := applyLaunchSkillSelection(in, nil, false)
	if g := names(got); len(g) != 3 || g[0] != "a" || g[1] != "b" || g[2] != "c" {
		t.Fatalf("empty names: want [a b c], got %v", g)
	}
	got2 := applyLaunchSkillSelection(in, []string{"  ", ""}, true)
	if g := names(got2); len(g) != 3 {
		t.Fatalf("whitespace-only names with force: want pass-through, got %v", g)
	}
}

func TestApplyLaunchSkillSelection_Pin_NoForce_PinnedAtTopRestKept(t *testing.T) {
	in := []skillpkg.SkillInfo{info("alpha"), info("bravo"), info("charlie"), info("delta")}
	got := applyLaunchSkillSelection(in, []string{"charlie", "alpha"}, false)
	g := names(got)
	// charlie / alpha 进 pinned；保留原输入相对顺序（alpha 在 charlie 前）
	if len(g) != 4 || g[0] != "alpha" || g[1] != "charlie" || g[2] != "bravo" || g[3] != "delta" {
		t.Fatalf("pin no-force: want [alpha charlie bravo delta], got %v", g)
	}
}

func TestApplyLaunchSkillSelection_Force_OnlyMatching(t *testing.T) {
	in := []skillpkg.SkillInfo{info("alpha"), info("bravo"), info("charlie"), info("delta")}
	got := applyLaunchSkillSelection(in, []string{"bravo"}, true)
	g := names(got)
	if len(g) != 1 || g[0] != "bravo" {
		t.Fatalf("force: want [bravo], got %v", g)
	}
}

func TestApplyLaunchSkillSelection_Force_NoMatch_ReturnsEmpty(t *testing.T) {
	in := []skillpkg.SkillInfo{info("alpha"), info("bravo")}
	got := applyLaunchSkillSelection(in, []string{"ghost"}, true)
	if len(got) != 0 {
		t.Fatalf("force no-match: want empty, got %v", names(got))
	}
}

func TestApplyLaunchSkillSelection_CaseInsensitive(t *testing.T) {
	in := []skillpkg.SkillInfo{info("Alpha"), info("BRAVO"), info("charlie")}
	got := applyLaunchSkillSelection(in, []string{"ALPHA", "bravo"}, true)
	g := names(got)
	if len(g) != 2 || g[0] != "Alpha" || g[1] != "BRAVO" {
		t.Fatalf("case-insensitive match: want [Alpha BRAVO], got %v", g)
	}
}

func TestApplyLaunchSkillSelection_DoesNotMutateInput(t *testing.T) {
	in := []skillpkg.SkillInfo{info("alpha"), info("bravo")}
	_ = applyLaunchSkillSelection(in, []string{"bravo"}, true)
	if in[0].Name != "alpha" || in[1].Name != "bravo" {
		t.Fatalf("input mutated: %v", names(in))
	}
}
