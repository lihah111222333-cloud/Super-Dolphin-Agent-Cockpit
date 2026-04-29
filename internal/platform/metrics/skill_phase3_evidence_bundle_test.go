package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePhase3EvidenceBundleScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02l",
		"SKILL_PD_EVIDENCE_BUNDLE_DIR",
		"SKILL_PD_EVIDENCE_BUNDLE_WRITE_MANIFEST",
		"production-smoke-evidence.md",
		"claudecli-e2e-evidence.md",
		"rollout-observation.md",
		"rollout-gate-output.txt",
		"phase3-preflight-output.txt",
		"Evidence type: production-smoke",
		"Evidence type: authenticated-claudecli-e2e",
		"P25-HIGH-02i rollout gate passed",
		"P25-HIGH-02j phase3 preflight passed.",
		"Evidence type: phase3-evidence-bundle",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required evidence-bundle token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosurePhase3EvidenceBundleScriptPassAndMissingFileFail(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-bundle.sh"
	bundleDir := t.TempDir()
	writePhase3EvidenceBundleFile(t, bundleDir, "production-smoke-evidence.md", strings.Join([]string{
		"Evidence type: production-smoke",
		"Total host tool calls: 22",
		"P25-HIGH-02g smoke passed.",
		"real traffic is non-zero",
		"",
	}, "\n"))
	writePhase3EvidenceBundleFile(t, bundleDir, "claudecli-e2e-evidence.md", strings.Join([]string{
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"PASS",
		"",
	}, "\n"))
	writePhase3EvidenceBundleFile(t, bundleDir, "rollout-observation.md", phase3BundleObservationRows(
		"| 2026-04-26 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-27 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	))
	writePhase3EvidenceBundleFile(t, bundleDir, "rollout-gate-output.txt", strings.Join([]string{
		"P25-HIGH-02i rollout gate passed: sample_days=2 total_host_tool_calls=22 non_ok_calls=0 non_ok_rate=0.0000 required_sample_days=2 rollback_drill_pass=true",
		"",
	}, "\n"))
	writePhase3EvidenceBundleFile(t, bundleDir, "phase3-preflight-output.txt", strings.Join([]string{
		"P25-HIGH-02j phase3 preflight passed.",
		"30-day rollout gate verifier passed with real samples.",
		"This script does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE and does not delete overrideSkillsToSummary.",
		"",
	}, "\n"))

	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_EVIDENCE_BUNDLE_DIR="+bundleDir,
		"SKILL_PD_EVIDENCE_BUNDLE_WRITE_MANIFEST=true",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected evidence bundle pass: %v\n%s", err, string(output))
	}
	if body := string(output); !strings.Contains(body, "evidence bundle passed") || !strings.Contains(body, "phase3-preflight-output.txt") {
		t.Fatalf("unexpected evidence bundle output:\n%s", body)
	}
	manifest, err := os.ReadFile(filepath.Join(bundleDir, "manifest.md"))
	if err != nil {
		t.Fatalf("read generated manifest: %v", err)
	}
	if body := string(manifest); !strings.Contains(body, "Evidence type: phase3-evidence-bundle") || !strings.Contains(body, "P25-HIGH-02l evidence bundle passed.") {
		t.Fatalf("unexpected manifest:\n%s", body)
	}

	missingDir := t.TempDir()
	writePhase3EvidenceBundleFile(t, missingDir, "production-smoke-evidence.md", "Evidence type: production-smoke\nTotal host tool calls: 1\nP25-HIGH-02g smoke passed.\nreal traffic is non-zero\n")
	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(), "SKILL_PD_EVIDENCE_BUNDLE_DIR="+missingDir)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected evidence bundle fail for missing files:\n%s", string(output))
	}
	if body := string(output); !strings.Contains(body, "missing authenticated Claude CLI E2E evidence") {
		t.Fatalf("missing-file failure output mismatch:\n%s", body)
	}
}

func writePhase3EvidenceBundleFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func phase3BundleObservationRows(rows ...string) string {
	header := "| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|\n"
	return header + strings.Join(rows, "\n") + "\n"
}
