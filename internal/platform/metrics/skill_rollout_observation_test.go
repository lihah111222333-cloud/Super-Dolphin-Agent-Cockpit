package metrics

import (
	"os"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureRolloutObservationTemplateArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-observation.md"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02d",
		"Observation date",
		"Version / commit",
		"`ENABLE_SKILL_PROGRESSIVE_DISCLOSURE`",
		"Total host tool calls",
		"`ok` calls",
		"`error` calls",
		"`cwd_missing` calls",
		"`approval_required` calls",
		"enrich_failure",
		"enrich_failures_total",
		"Manual smoke result",
		"Production Prometheus smoke result",
		"skill-progressive-disclosure-rollout-smoke.sh",
		"skill-progressive-disclosure-rollout-report.sh",
		"skill-progressive-disclosure-rollout-append.sh",
		"incomplete evidence",
		"no-sample rows",
		"skill-progressive-disclosure-rollout-status.sh",
		"next",
		"skill-progressive-disclosure-rollout-daily.sh",
		"report -> append -> status",
		"skill-progressive-disclosure-rollout-gate.sh",
		"30-day rollout gate verifier",
		"Rollback drill result",
		"Rollback trigger",
		"Decision",
		"30 days",
		"No-sample rule",
		"无样本",
		"no samples",
		"Do not report a success rate",
		"p25skill优化.md §9.3",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required rollout-observation token %q", path, token)
		}
	}
}
