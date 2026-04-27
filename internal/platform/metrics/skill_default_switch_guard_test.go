package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureDefaultSwitchGuardScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-default-switch-guard.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02m",
		"SKILL_PD_REPO_ROOT",
		"EnableSkillProgressiveDisclosure: parseBoolEnv(envEnableSkillProgressiveDisclosure, false)",
		"EnableSkillProgressiveDisclosure: parseBoolEnv(envEnableSkillProgressiveDisclosure, true)",
		"TestSkillProgressiveDisclosure_DefaultDisabled",
		"func overrideSkillsToSummary",
		"overrideSkillsToSummary(req.Skills)",
		"P25-HIGH-02j phase3 preflight passed.",
		"P25-HIGH-02l evidence bundle passed.",
		"P25-HIGH-02n evidence collect passed.",
		"skill-progressive-disclosure-phase3-evidence-collect.sh",
		"PR-6 混入 Phase 3 default policy 删除 override",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required default-switch-guard token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureDefaultSwitchGuardPassAndDefaultTrueFail(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-default-switch-guard.sh"
	cmd := exec.Command("bash", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected default-switch guard pass on current repo: %v\n%s", err, string(output))
	}
	if body := string(output); !strings.Contains(body, "default-switch guard passed") || !strings.Contains(body, "default is still false") {
		t.Fatalf("unexpected guard pass output:\n%s", body)
	}

	fakeRoot := t.TempDir()
	writeDefaultSwitchGuardFixture(t, fakeRoot, "internal/module/prompt/config.go", "package prompt\nfunc x(){ _ = parseBoolEnv(envEnableSkillProgressiveDisclosure, true) }\nvar _ = Config{EnableSkillProgressiveDisclosure: parseBoolEnv(envEnableSkillProgressiveDisclosure, true)}\n")
	writeDefaultSwitchGuardFixture(t, fakeRoot, "internal/module/prompt/skill_catalog_fx_test.go", "package prompt\nfunc TestSkillProgressiveDisclosure_DefaultDisabled(){}\n")
	writeDefaultSwitchGuardFixture(t, fakeRoot, "internal/provider/codexapp/skill_mode_override.go", "package codexapp\n// Phase 2\nfunc overrideSkillsToSummary(){}\n")
	writeDefaultSwitchGuardFixture(t, fakeRoot, "internal/provider/codexapp/skill_mode_override_test.go", "package codexapp\nfunc TestOverrideSkillsToSummary_FlipsUnspecifiedToSummary(){}\nfunc TestOverrideSkillsToSummary_PreservesExplicitFull(){}\n")
	writeDefaultSwitchGuardFixture(t, fakeRoot, "internal/provider/codexapp/session_turn.go", "package codexapp\nfunc a(){ overrideSkillsToSummary(req.Skills); overrideSkillsToSummary(req.Skills) }\n")
	writeDefaultSwitchGuardFixture(t, fakeRoot, "docs/plans/迁移/p25skill优化/p25skill优化.md", "PR-6 混入 Phase 3 default policy 删除 override\n")
	writeDefaultSwitchGuardFixture(t, fakeRoot, "docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh", "P25-HIGH-02j phase3 preflight passed.\ndoes not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE\n")
	writeDefaultSwitchGuardFixture(t, fakeRoot, "docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh", "P25-HIGH-02l evidence bundle passed.\ndoes not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE\n")

	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(), "SKILL_PD_REPO_ROOT="+fakeRoot)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected default-switch guard fail for default=true fixture:\n%s", string(output))
	}
	body := string(output)
	if !strings.Contains(body, "prompt config default-disabled policy missing token") || !strings.Contains(body, "parseBoolEnv(envEnableSkillProgressiveDisclosure, false)") {
		t.Fatalf("default=true failure output mismatch:\n%s", body)
	}

	missingCollectorRoot := t.TempDir()
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "internal/module/prompt/config.go", "package prompt\nvar _ = Config{EnableSkillProgressiveDisclosure: parseBoolEnv(envEnableSkillProgressiveDisclosure, false)}\n")
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "internal/module/prompt/skill_catalog_fx_test.go", "package prompt\nfunc TestSkillProgressiveDisclosure_DefaultDisabled(){}\n")
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "internal/provider/codexapp/skill_mode_override.go", "package codexapp\n// Phase 2\nfunc overrideSkillsToSummary(){}\n")
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "internal/provider/codexapp/skill_mode_override_test.go", "package codexapp\nfunc TestOverrideSkillsToSummary_FlipsUnspecifiedToSummary(){}\nfunc TestOverrideSkillsToSummary_PreservesExplicitFull(){}\n")
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "internal/provider/codexapp/session_turn.go", "package codexapp\nfunc a(){ overrideSkillsToSummary(req.Skills); overrideSkillsToSummary(req.Skills) }\n")
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "docs/plans/迁移/p25skill优化/p25skill优化.md", "PR-6 混入 Phase 3 default policy 删除 override\n")
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-preflight.sh", "P25-HIGH-02j phase3 preflight passed.\ndoes not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE\n")
	writeDefaultSwitchGuardFixture(t, missingCollectorRoot, "docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh", "P25-HIGH-02l evidence bundle passed.\ndoes not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE\n")
	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(), "SKILL_PD_REPO_ROOT="+missingCollectorRoot)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected default-switch guard fail for missing evidence collector:\n%s", string(output))
	}
	if body := string(output); !strings.Contains(body, "missing Phase 3 evidence collector") || !strings.Contains(body, "skill-progressive-disclosure-phase3-evidence-collect.sh") {
		t.Fatalf("missing-collector failure output mismatch:\n%s", body)
	}
}

func writeDefaultSwitchGuardFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}
