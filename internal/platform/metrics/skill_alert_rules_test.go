package metrics

import (
	"os"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureAlertRulesArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-alerts.yml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"SkillHostToolHighErrorRate",
		`host_tool_calls_total{outcome!="ok"}`,
		"clamp_min(sum(rate(host_tool_calls_total[5m])), 1)",
		") > 0.05",
		"for: 5m",
		"SkillHostToolCWDMissing",
		`host_tool_calls_total{outcome="cwd_missing"}`,
		"SkillHostToolApprovalRequiredStuck",
		`host_tool_calls_total{outcome="approval_required"}`,
		"SkillToolEnrichFailures",
		"enrich_failures_total[5m]",
		"p25skill优化.md §9.3",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required alert token %q", path, token)
		}
	}
}
