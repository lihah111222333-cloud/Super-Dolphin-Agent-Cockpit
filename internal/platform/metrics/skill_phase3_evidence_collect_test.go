package metrics

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePhase3EvidenceCollectScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02n",
		"Phase 3 evidence bundle collector",
		"SKILL_PD_BUNDLE_OUT_DIR",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE",
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE",
		"SKILL_PD_OBSERVATION_FILE",
		"SKILL_PD_REQUIRED_SAMPLE_DAYS",
		"skill-progressive-disclosure-rollout-gate.sh",
		"skill-progressive-disclosure-phase3-preflight.sh",
		"skill-progressive-disclosure-phase3-evidence-bundle.sh",
		"production-smoke-evidence.md",
		"claudecli-e2e-evidence.md",
		"rollout-observation.md",
		"rollout-gate-output.txt",
		"phase3-preflight-output.txt",
		"evidence-bundle-output.txt",
		"MANIFEST_PATH",
		"rm -f",
		"manifest.md",
		"P25-HIGH-02n evidence collect passed.",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing required evidence-collect token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosurePhase3EvidenceCollectPassAndMissingEvidenceFail(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-collect.sh"
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "inputs")
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		t.Fatalf("mkdir inputs: %v", err)
	}

	productionSmoke := filepath.Join(inputDir, "production-smoke-filled.md")
	writePhase3EvidenceCollectFile(t, productionSmoke, strings.Join([]string{
		"Evidence type: production-smoke",
		"Total host tool calls: 22",
		"P25-HIGH-02g smoke passed.",
		"real traffic is non-zero",
		"raw smoke output: PASS",
		"",
	}, "\n"))
	claudeE2E := filepath.Join(inputDir, "claudecli-e2e-filled.md")
	writePhase3EvidenceCollectFile(t, claudeE2E, strings.Join([]string{
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"test name: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"result: PASS",
		"raw E2E output: PASS",
		"",
	}, "\n"))
	observation := filepath.Join(inputDir, "rollout-observation-filled.md")
	writePhase3EvidenceCollectFile(t, observation, phase3CollectObservationRows(
		"| 2026-04-26 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-27 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	))

	bundleDir := filepath.Join(tempDir, "bundle")
	cmd := exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_BUNDLE_OUT_DIR="+bundleDir,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE="+claudeE2E,
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected evidence collect pass: %v\n%s", err, string(output))
	}
	if body := string(output); !strings.Contains(body, "P25-HIGH-02n evidence collect passed.") || !strings.Contains(body, "evidence bundle passed") {
		t.Fatalf("unexpected evidence collect output:\n%s", body)
	}

	for _, name := range []string{
		"production-smoke-evidence.md",
		"claudecli-e2e-evidence.md",
		"rollout-observation.md",
		"rollout-gate-output.txt",
		"phase3-preflight-output.txt",
		"evidence-bundle-output.txt",
		"manifest.md",
	} {
		if _, err := os.Stat(filepath.Join(bundleDir, name)); err != nil {
			t.Fatalf("expected bundle file %s: %v", name, err)
		}
	}
	assertPhase3EvidenceCollectFileContains(t, filepath.Join(bundleDir, "rollout-gate-output.txt"), "sample_days=2")
	assertPhase3EvidenceCollectFileContains(t, filepath.Join(bundleDir, "phase3-preflight-output.txt"), "P25-HIGH-02j phase3 preflight passed.")
	assertPhase3EvidenceCollectFileContains(t, filepath.Join(bundleDir, "evidence-bundle-output.txt"), "P25-HIGH-02l evidence bundle passed.")
	assertPhase3EvidenceCollectFileContains(t, filepath.Join(bundleDir, "manifest.md"), "Evidence type: phase3-evidence-bundle")

	missingDir := filepath.Join(tempDir, "missing-bundle")
	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_BUNDLE_OUT_DIR="+missingDir,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
	)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected evidence collect fail without Claude E2E evidence:\n%s", string(output))
	}
	if body := string(output); !strings.Contains(body, "authenticated Claude CLI E2E evidence path is required") {
		t.Fatalf("missing-evidence failure output mismatch:\n%s", body)
	}

	noSampleObservation := filepath.Join(inputDir, "no-sample-observation.md")
	writePhase3EvidenceCollectFile(t, noSampleObservation, phase3CollectObservationRows(
		"| 2026-04-28 | abc125 | canary | 24h | 0 | 0 | 0 | 0 | 0 | 0 | `SKIP(no samples)` | `SKIP(not applied)` | `SKIP(no release window)` | none | hold | no samples |",
	))
	gateFailDir := filepath.Join(tempDir, "gate-fail-bundle")
	if err := os.MkdirAll(gateFailDir, 0o755); err != nil {
		t.Fatalf("mkdir gate-fail-bundle: %v", err)
	}
	writePhase3EvidenceCollectFile(t, filepath.Join(gateFailDir, "phase3-preflight-output.txt"), "stale preflight pass\n")
	writePhase3EvidenceCollectFile(t, filepath.Join(gateFailDir, "manifest.md"), "stale manifest\n")
	cmd = exec.Command("bash", path)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_BUNDLE_OUT_DIR="+gateFailDir,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE="+claudeE2E,
		"SKILL_PD_OBSERVATION_FILE="+noSampleObservation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=1",
	)
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected evidence collect fail on rollout gate no-sample observation:\n%s", string(output))
	}
	if body := string(output); !strings.Contains(body, "rollout gate failed; see") {
		t.Fatalf("rollout-gate failure output mismatch:\n%s", body)
	}
	assertPhase3EvidenceCollectFileContains(t, filepath.Join(gateFailDir, "rollout-gate-output.txt"), "sample days 0 < required 1")
	assertPhase3EvidenceCollectFileNotExists(t, filepath.Join(gateFailDir, "phase3-preflight-output.txt"))
	assertPhase3EvidenceCollectFileNotExists(t, filepath.Join(gateFailDir, "manifest.md"))
}

func writePhase3EvidenceCollectFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func phase3CollectObservationRows(rows ...string) string {
	header := "| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|\n"
	return header + strings.Join(rows, "\n") + fmt.Sprintln()
}

func assertPhase3EvidenceCollectFileContains(t *testing.T, path, token string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if body := string(raw); !strings.Contains(body, token) {
		t.Fatalf("%s missing %q\n%s", path, token, body)
	}
}

func assertPhase3EvidenceCollectFileNotExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected stale file to be removed: %s", path)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", path, err)
	}
}
