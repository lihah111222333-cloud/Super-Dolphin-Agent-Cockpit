package metrics

import (
	"os"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePrometheusConfigArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-prometheus.yml"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02f",
		"rule_files:",
		"skill-progressive-disclosure-alerts.yml",
		"scrape_configs:",
		"job_name: super-dolphin-skill-progressive-disclosure",
		"metrics_path: /metrics",
		"127.0.0.1:4511",
		"host_tool_calls_total",
		"Prometheus Targets shows the job as UP",
		"Prometheus Rules",
		"Alertmanager",
		"skill-progressive-disclosure-rollout-smoke.sh",
		"30-day observation record",
		"alerting:",
		"127.0.0.1:9093",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required prometheus-config token %q", path, token)
		}
	}
}
