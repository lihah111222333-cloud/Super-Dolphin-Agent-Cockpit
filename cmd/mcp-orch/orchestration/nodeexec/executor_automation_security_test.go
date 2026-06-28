package nodeexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestShellCommandRunnerRequiresHighRiskPolicy(t *testing.T) {
	root := t.TempDir()
	runner := NewShellCommandRunner()
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
	runner := NewShellCommandRunner()
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
	runner := NewShellCommandRunner()
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

func TestStripAutomationControlFieldsBeforePromptReuse(t *testing.T) {
	got := stripAutomationControlFieldsBeforePromptReuse(`{"stdout":"ok","stderr":"secret","command":"cat token","exit_code":1}`)
	if strings.Contains(got, "stderr") || strings.Contains(got, "command") || strings.Contains(got, "exit_code") {
		t.Fatalf("control fields were not stripped: %s", got)
	}
	if !strings.Contains(got, "stdout") {
		t.Fatalf("business output was stripped: %s", got)
	}
}
