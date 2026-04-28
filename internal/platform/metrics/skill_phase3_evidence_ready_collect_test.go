package metrics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillProgressiveDisclosurePhase3EvidenceReadyCollectScriptArtifact(t *testing.T) {
	path := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-ready-collect.sh"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	body := string(raw)
	required := []string{
		"P25-HIGH-02v",
		"one-command Phase 3 evidence readiness check + collector runner",
		"SKILL_PD_PHASE3_EVIDENCE_STATUS_SCRIPT",
		"SKILL_PD_PHASE3_EVIDENCE_COLLECT_SCRIPT",
		"SKILL_PD_BUNDLE_OUT_DIR",
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE",
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE",
		"SKILL_PD_OBSERVATION_FILE",
		"SKILL_PD_PHASE3_STATUS_FAIL_ON_BLOCKERS=true",
		"phase3_collect_ready=true",
		"blocker_count=0",
		"phase3-evidence-status-output.txt",
		"phase3-evidence-collect-output.txt",
		"P25-HIGH-02v phase3 evidence ready collect passed.",
		"does not enable ENABLE_SKILL_PROGRESSIVE_DISCLOSURE",
		"merge branches",
	}
	for _, token := range required {
		if !strings.Contains(body, token) {
			t.Fatalf("%s missing phase3 ready collect token %q", path, token)
		}
	}
}

func TestSkillProgressiveDisclosurePhase3EvidenceReadyCollectPassAndStatusFail(t *testing.T) {
	script := "../../../docs/plans/迁移/p25skill优化/skill-progressive-disclosure-phase3-evidence-ready-collect.sh"
	tempDir := t.TempDir()
	inputDir := filepath.Join(tempDir, "inputs")
	if err := os.MkdirAll(inputDir, 0o755); err != nil {
		t.Fatalf("mkdir inputs: %v", err)
	}
	productionSmoke := filepath.Join(inputDir, "production-smoke-evidence.md")
	writePhase3ReadyCollectFile(t, productionSmoke, strings.Join([]string{
		"Evidence type: production-smoke",
		"Total host tool calls: 22",
		"P25-HIGH-02g smoke passed.",
		"real traffic is non-zero",
		"raw smoke output: PASS",
		"",
	}, "\n"))
	claudeE2E := filepath.Join(inputDir, "claudecli-e2e-evidence.md")
	writePhase3ReadyCollectFile(t, claudeE2E, strings.Join([]string{
		"Evidence type: authenticated-claudecli-e2e",
		"Authenticated environment: true",
		"Test name: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"Result: PASS",
		"Skip status: none",
		"--- PASS: TestMcpSkillMode_ClaudeCLIManagedSameBinarySkillE2E",
		"",
	}, "\n"))
	observation := filepath.Join(inputDir, "rollout-observation.md")
	writePhase3ReadyCollectFile(t, observation, phase3ReadyCollectObservationRows(
		"| 2026-04-26 | abc123 | canary | 24h | 10 | 10 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `PASS` | none | continue | clean |",
		"| 2026-04-27 | abc124 | canary | 24h | 12 | 12 | 0 | 0 | 0 | 0 | `PASS` | `PASS` | `SKIP(no release window)` | none | continue | clean |",
	))

	bundleDir := filepath.Join(tempDir, "bundle")
	cmd := exec.Command("bash", script)
	cmd.Env = append(os.Environ(),
		"SKILL_PD_BUNDLE_OUT_DIR="+bundleDir,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE="+claudeE2E,
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected ready collect pass: %v\n%s", err, string(output))
	}
	body := string(output)
	for _, token := range []string{
		"phase3_collect_ready=true",
		"P25-HIGH-02n evidence collect passed.",
		"P25-HIGH-02v phase3 evidence ready collect passed.",
	} {
		if !strings.Contains(body, token) {
			t.Fatalf("ready collect output missing %q\n%s", token, body)
		}
	}
	for _, name := range []string{
		"phase3-evidence-status-output.txt",
		"phase3-evidence-collect-output.txt",
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
	assertPhase3ReadyCollectFileContains(t, filepath.Join(bundleDir, "phase3-evidence-status-output.txt"), "blocker_count=0")
	assertPhase3ReadyCollectFileContains(t, filepath.Join(bundleDir, "phase3-evidence-collect-output.txt"), "P25-HIGH-02n evidence collect passed.")

	missingBundleDir := filepath.Join(tempDir, "missing-bundle")
	missingClaude := exec.Command("bash", script)
	missingClaude.Env = append(os.Environ(),
		"SKILL_PD_BUNDLE_OUT_DIR="+missingBundleDir,
		"SKILL_PD_PRODUCTION_SMOKE_EVIDENCE="+productionSmoke,
		"SKILL_PD_CLAUDECLI_E2E_EVIDENCE="+filepath.Join(inputDir, "missing-claude-evidence.md"),
		"SKILL_PD_OBSERVATION_FILE="+observation,
		"SKILL_PD_REQUIRED_SAMPLE_DAYS=2",
	)
	missingOutput, err := missingClaude.CombinedOutput()
	if err == nil {
		t.Fatalf("expected status fail without Claude evidence:\n%s", string(missingOutput))
	}
	missingBody := string(missingOutput)
	for _, token := range []string{
		"Phase 3 evidence status failed; see",
		"phase3-evidence-status-output.txt",
	} {
		if !strings.Contains(missingBody, token) {
			t.Fatalf("missing-Claude output missing %q\n%s", token, missingBody)
		}
	}
	assertPhase3ReadyCollectFileContains(t, filepath.Join(missingBundleDir, "phase3-evidence-status-output.txt"), "authenticated Claude CLI E2E evidence file not found")
	if _, err := os.Stat(filepath.Join(missingBundleDir, "phase3-evidence-collect-output.txt")); err == nil {
		t.Fatalf("collector should not run when status fails")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat collect output: %v", err)
	}
}

func writePhase3ReadyCollectFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func phase3ReadyCollectObservationRows(rows ...string) string {
	header := "| Date | Version / commit | Switch state | Window | Total host tool calls | ok | error | cwd_missing | approval_required | enrich_failure | Manual smoke result | Production Prometheus smoke result | Rollback drill result | Rollback trigger | Decision | Notes |\n" +
		"|---|---|---|---:|---:|---:|---:|---:|---:|---:|---|---|---|---|---|---|\n"
	return header + strings.Join(rows, "\n") + "\n"
}

func assertPhase3ReadyCollectFileContains(t *testing.T, path, token string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if body := string(raw); !strings.Contains(body, token) {
		t.Fatalf("%s missing %q\n%s", path, token, body)
	}
}
