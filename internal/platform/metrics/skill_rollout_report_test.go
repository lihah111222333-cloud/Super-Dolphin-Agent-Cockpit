package metrics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosureRolloutReportScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02h",
		"PROMETHEUS_URL",
		"SKILL_PD_QUERY_WINDOW",
		"SKILL_PD_SWITCH_STATE",
		"SKILL_PD_RUN_ROLLOUT_SMOKE",
		"SKILL_PD_ROLLOUT_SMOKE_SCRIPT",
		"/api/v1/query",
		`host_tool_calls_total{outcome=\"ok\"}`,
		`host_tool_calls_total{outcome=\"error\"}`,
		`host_tool_calls_total{outcome=\"cwd_missing\"}`,
		`host_tool_calls_total{outcome=\"approval_required\"}`,
		"enrich_failures_total",
		"skill_artifact_approval_miss_total",
		"skill-progressive-disclosure-rollout-smoke.sh",
		"无样本 / no samples; gate remains open",
		"Artifact approval cache miss",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required rollout-report token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosureRolloutReportScriptNoSampleRule(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-rollout-report.sh"
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1714176000,"0"]}]}}`)
	}))
	defer prom.Close()

	tempDir := t.TempDir()
	smokePath := filepath.Join(tempDir, "fake-rollout-smoke.sh")
	if err := os.WriteFile(smokePath, []byte("#!/usr/bin/env bash\necho 'fake smoke passed'\n"), 0o755); err != nil {
		t.Fatalf("write smoke script: %v", err)
	}

	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"PROMETHEUS_URL="+prom.URL,
		"SKILL_PD_QUERY_WINDOW=24h",
		"SKILL_PD_DATE=2026-04-27",
		"SKILL_PD_VERSION=abc123",
		"SKILL_PD_SWITCH_STATE=false",
		"SKILL_PD_RUN_ROLLOUT_SMOKE=true",
		"SKILL_PD_ROLLOUT_SMOKE_SCRIPT="+smokePath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s: %v\n%s", path, err, string(output))
	}
	body := string(output)
	required := []string{
		"# P25-HIGH-02h rollout observation report",
		"| 2026-04-27 | abc123 | false | 24h | 0 | 0 | 0 | 0 | 0 | 0 | `SKIP(no samples)` | `PASS` | `SKIP(no release window)` | none | hold | 无样本 / no samples; gate remains open; artifact_approval_miss=0 |",
		"Artifact approval cache miss: 0",
		"## Production smoke output",
		"fake smoke passed",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("rollout report output missing %q\n%s", token, body)
		}
	}
}
