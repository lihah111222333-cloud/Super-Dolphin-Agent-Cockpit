package nodeexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestShellCommandRunnerRequiresHighRiskPolicy(t *testing.T) {
	root := t.TempDir()
	runner := newShellCommandRunnerForTest(t)
	_, err := runner.RunCommandCard(context.Background(), AutomationCommandCard{
		CardKey:         "safe.echo",
		CommandTemplate: "echo ok",
		RiskLevel:       "medium",
		Enabled:         true,
	}, json.RawMessage(`{}`), AutomationCommandRunOptions{
		CWD:            root,
		WorkspaceRoots: []string{root},
	})
	if err == nil || !strings.Contains(err.Error(), "high-risk") {
		t.Fatalf("error = %v, want high-risk policy rejection", err)
	}
}

func TestShellCommandRunnerRejectsCWDOutsideWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	runner := newShellCommandRunnerForTest(t)
	_, err := runner.RunCommandCard(context.Background(), AutomationCommandCard{
		CardKey:         "safe.echo",
		CommandTemplate: "echo ok",
		RiskLevel:       "high",
		Enabled:         true,
	}, json.RawMessage(`{}`), AutomationCommandRunOptions{
		CWD:            outside,
		WorkspaceRoots: []string{root},
	})
	if err == nil || !strings.Contains(err.Error(), "outside allowed workspace root") {
		t.Fatalf("error = %v, want workspace escape rejection", err)
	}
}

func TestShellCommandRunnerRejectsDisallowedEnv(t *testing.T) {
	root := t.TempDir()
	runner := newShellCommandRunnerForTest(t)
	_, err := runner.RunCommandCard(context.Background(), AutomationCommandCard{
		CardKey:         "safe.echo",
		CommandTemplate: "echo ok",
		RiskLevel:       "high",
		Enabled:         true,
	}, json.RawMessage(`{}`), AutomationCommandRunOptions{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Env: map[string]string{
			"SECRET_TOKEN": "do-not-run",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "environment variable") {
		t.Fatalf("error = %v, want environment allowlist rejection", err)
	}
}

func TestRedactSensitiveText(t *testing.T) {
	redacted := redactSensitiveText(strings.Join([]string{
		"token=super-secret api_key=sk-test password=hunter2",
		"Authorization: Bearer auth-secret",
		"Cookie: a=b; session=cookie-secret; theme=dark",
	}, "\n"))
	for _, secret := range []string{"super-secret", "sk-test", "hunter2", "Bearer", "auth-secret", "a=b", "session=", "cookie-secret", "theme=dark"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, redacted)
		}
	}
	for _, want := range []string{"token=[REDACTED]", "api_key=[REDACTED]", "password=[REDACTED]", "Authorization: [REDACTED]", "Cookie: [REDACTED]"} {
		if !strings.Contains(redacted, want) {
			t.Fatalf("redacted output = %q, want %q", redacted, want)
		}
	}
}

func TestRedactSensitiveTextScrubsNestedJSONAndPreservesLookalikeKeys(t *testing.T) {
	secrets := []string{"openai-json-secret", "aws-json-secret", "github-json-secret"}
	redacted := redactSensitiveText(`{
		"nested": [
			{"OPENAI_API_KEY": "openai-json-secret"},
			{"credentials": {"AWS_SECRET_ACCESS_KEY": "aws-json-secret"}},
			{"GITHUB_TOKEN": "github-json-secret"}
		],
		"monkey": "banana",
		"secretary": "alice",
		"tokenize": "enabled"
	}`)
	assertSecretsRedacted(t, redacted, secrets...)

	var decoded map[string]any
	if err := json.Unmarshal([]byte(redacted), &decoded); err != nil {
		t.Fatalf("redacted JSON is invalid: %v\n%s", err, redacted)
	}
	for key, want := range map[string]string{
		"monkey":    "banana",
		"secretary": "alice",
		"tokenize":  "enabled",
	} {
		if got := decoded[key]; got != want {
			t.Fatalf("%s = %#v, want %q", key, got, want)
		}
	}
}

func TestRedactSensitiveTextScrubsJSONL(t *testing.T) {
	secrets := []string{"openai-jsonl-secret", "aws-jsonl-secret", "github-jsonl-secret"}
	redacted := redactSensitiveText(strings.Join([]string{
		`{"OPENAI_API_KEY":"openai-jsonl-secret"}`,
		`[{"AWS_SECRET_ACCESS_KEY":"aws-jsonl-secret"}]`,
		`{"nested":{"GITHUB_TOKEN":"github-jsonl-secret"}}`,
	}, "\n"))
	assertSecretsRedacted(t, redacted, secrets...)

	for lineNumber, line := range strings.Split(redacted, "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("redacted JSONL line %d is invalid: %q", lineNumber+1, line)
		}
	}
}

func TestRedactSensitiveTextScrubsQuotedKeysEnvHeadersAndAssignments(t *testing.T) {
	secrets := []string{
		"openai-quoted-secret",
		"aws-export-secret",
		"github-env-secret",
		"bearer-header-secret",
		"cookie-header-secret",
		"plain-token-secret",
	}
	redacted := redactSensitiveText(strings.Join([]string{
		`prefix "OPENAI_API_KEY": "openai-quoted-secret" suffix`,
		`export AWS_SECRET_ACCESS_KEY='aws-export-secret'`,
		`$env:GITHUB_TOKEN="github-env-secret"`,
		`Authorization: Bearer bearer-header-secret`,
		`Cookie: session=cookie-header-secret; theme=dark`,
		`token=plain-token-secret`,
		`monkey=banana secretary=alice tokenize=enabled`,
	}, "\n"))
	assertSecretsRedacted(t, redacted, secrets...)

	for _, preserved := range []string{"monkey=banana", "secretary=alice", "tokenize=enabled"} {
		if !strings.Contains(redacted, preserved) {
			t.Fatalf("redacted text = %q, want preserved %q", redacted, preserved)
		}
	}
}

func TestRedactSensitiveTextFailsSafeOnMalformedStructuredText(t *testing.T) {
	const secret = "malformed-secret"
	redacted := redactSensitiveText(`prefix {"OPENAI_API_KEY":"malformed-secret`)
	assertSecretsRedacted(t, redacted, secret)
}

func TestScrubAutomationResultPayloadUsesCanonicalSensitiveKeys(t *testing.T) {
	secrets := []string{"openai-result-secret", "aws-result-secret", "github-result-secret"}
	redacted := ScrubAutomationResultPayload(json.RawMessage(`{
		"OPENAI_API_KEY": "openai-result-secret",
		"nested": [
			{"AWS_SECRET_ACCESS_KEY": "aws-result-secret"},
			{"GITHUB_TOKEN": "github-result-secret"}
		],
		"monkey": "banana",
		"secretary": "alice",
		"tokenize": "enabled"
	}`))
	assertSecretsRedacted(t, string(redacted), secrets...)

	var decoded map[string]any
	if err := json.Unmarshal(redacted, &decoded); err != nil {
		t.Fatalf("scrubbed result is invalid JSON: %v\n%s", err, redacted)
	}
	for key, want := range map[string]string{
		"monkey":    "banana",
		"secretary": "alice",
		"tokenize":  "enabled",
	} {
		if got := decoded[key]; got != want {
			t.Fatalf("%s = %#v, want %q", key, got, want)
		}
	}
}

func assertSecretsRedacted(t *testing.T, redacted string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, "[REDACTED]") {
		t.Fatalf("redacted output = %q, want redaction marker", redacted)
	}
}

func TestStripAutomationControlFieldsBeforePromptReuse(t *testing.T) {
	got := stripAutomationControlFieldsBeforePromptReuse(`{"stdout":"ok","stderr":"secret","command":"cat token","exit_code":1}`)
	if strings.Contains(got, "stderr") || strings.Contains(got, "command") || strings.Contains(got, "exit_code") {
		t.Fatalf("control fields were not stripped: %s", got)
	}
	if !strings.Contains(got, "stdout") {
		t.Fatalf("business output was stripped: %s", got)
	}
}
