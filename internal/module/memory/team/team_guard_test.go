package team

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTeamSecretGuardBlocksToolTimeWrite(t *testing.T) {
	autoRoot := filepath.Join(t.TempDir(), "automem")
	guard := NewTeamMemoryGuard(NewTeamMemoryManager(newTestConfig(filepath.Join(autoRoot, teamMemoryRootDirName))))
	_, err := guard.ValidateWrite("notes/secret.md", "token = \"ghp_abcdefghijklmnopqrstuvwxyz1234567890AB\"\n")
	if !errors.Is(err, ErrTeamMemSecretDetected) {
		t.Fatalf("ValidateWrite(secret) error = %v, want %v", err, ErrTeamMemSecretDetected)
	}
	var secretErr *TeamMemSecretError
	if !errors.As(err, &secretErr) {
		t.Fatalf("ValidateWrite(secret) error = %T, want *TeamMemSecretError", err)
	}
	if len(secretErr.Findings) == 0 || secretErr.Findings[0].RuleID != "github_token" {
		t.Fatalf("ValidateWrite(secret) findings = %#v, want github_token match", secretErr.Findings)
	}
}

func TestTeamSecretGuardAllowsSafeToolTimeWrite(t *testing.T) {
	autoRoot := filepath.Join(t.TempDir(), "automem")
	guard := NewTeamMemoryGuard(NewTeamMemoryManager(newTestConfig(filepath.Join(autoRoot, teamMemoryRootDirName))))
	got, err := guard.ValidateWrite(`notes\\safe.md`, "# Notes\n- share roadmap only\n")
	if err != nil {
		t.Fatalf("ValidateWrite(safe) error = %v", err)
	}
	want := filepath.Join(autoRoot, teamMemoryRootDirName, "notes", "safe.md")
	if got != want {
		t.Fatalf("ValidateWrite(safe) path = %q, want %q", got, want)
	}
}

func TestTeamSecretRulesAreCallScoped(t *testing.T) {
	rules := teamSecretRules()
	rules[0].id = "mutated"
	findings := ScanTeamMemContent("-----BEGIN PRIVATE KEY-----\n")
	if len(findings) != 1 || findings[0].RuleID != "private_key" {
		t.Fatalf("ScanTeamMemContent() findings = %#v, want private_key", findings)
	}
}

func TestTeamSecretGuardSkipsOnlySecretFilesOnPrePush(t *testing.T) {
	guard := NewTeamMemoryGuard(nil)
	result := guard.FilterPushFiles(map[string]string{
		"notes/safe.md":  "# Safe\n- roadmap only\n",
		"notes/token.md": "api_key = \"sk-proj-abcdefghijklmnopqrstuvwxyz1234567890\"\n",
		"notes/key.pem":  "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n",
	})
	if len(result.Allowed) != 1 || result.Allowed["notes/safe.md"] == "" {
		t.Fatalf("FilterPushFiles().Allowed = %#v, want only safe file", result.Allowed)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("FilterPushFiles().Skipped = %#v, want 2 skipped files", result.Skipped)
	}
	if result.Skipped[0].Path != "notes/key.pem" || result.Skipped[1].Path != "notes/token.md" {
		t.Fatalf("FilterPushFiles().Skipped paths = %#v, want sorted secret paths", result.Skipped)
	}
}

func TestTeamSecretGuardScansCommonHighConfidencePatterns(t *testing.T) {
	findings := ScanTeamMemContent("-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\nAWS=AKIAABCDEFGHIJKLMNOP\n")
	if len(findings) < 2 {
		t.Fatalf("ScanTeamMemContent() = %#v, want private key and aws key findings", findings)
	}
	if findings[0].Line != 1 {
		t.Fatalf("first finding line = %d, want 1", findings[0].Line)
	}
}
