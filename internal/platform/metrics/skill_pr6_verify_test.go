package metrics

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePR6VerifyScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-pr6-verify.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02o",
		"one-command PR-6 verification wrapper",
		"SKILL_PD_PR6_VERIFY_SKIP_GO_TESTS",
		"SKILL_PD_PR6_VERIFY_SKIP_GIT_DIFF_CHECK",
		"skill-progressive-disclosure-rollout-smoke.sh",
		"skill-progressive-disclosure-rollout-report.sh",
		"skill-progressive-disclosure-rollout-append.sh",
		"skill-progressive-disclosure-rollout-status.sh",
		"skill-progressive-disclosure-rollout-daily.sh",
		"skill-progressive-disclosure-production-smoke-evidence-generate.sh",
		"skill-progressive-disclosure-claudecli-e2e-evidence-generate.sh",
		"skill-progressive-disclosure-rollout-gate.sh",
		"skill-progressive-disclosure-phase3-preflight.sh",
		"skill-progressive-disclosure-phase3-evidence-bundle.sh",
		"skill-progressive-disclosure-phase3-evidence-collect.sh",
		"skill-progressive-disclosure-phase3-evidence-status.sh",
		"skill-progressive-disclosure-default-switch-guard.sh",
		"script is not executable",
		"go test ./pkg/skillmetrics ./internal/platform/metrics -count=1",
		"go test ./internal/provider/codexapp",
		"go test ./internal/platform/toolbridge",
		"go test ./internal/module/prompt",
		"go test ./internal/module/turn",
		"go test ./internal/ui/wails",
		"git diff --check",
		"P25-HIGH-02o PR-6 verification passed.",
		"report / append / status / daily / production-evidence / claudecli-e2e-evidence / gate / preflight / evidence bundle / evidence collect / phase3-evidence-status",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required PR-6 verify token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosurePR6VerifyScriptSkipGoTestsSmoke(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-pr6-verify.sh"
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_PR6_VERIFY_SKIP_GO_TESTS=true",
		"SKILL_PD_PR6_VERIFY_SKIP_GIT_DIFF_CHECK=true",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected PR-6 verify smoke pass: %v\n%s", err, string(output))
	}
	body := string(output)
	for _, token := range []string{
		"P25-HIGH-02m default-switch guard passed.",
		"SKIP go test commands",
		"SKIP git diff --check",
		"P25-HIGH-02o PR-6 verification passed.",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("PR-6 verify smoke output missing %q\n%s", token, body)
		}
	}
}
