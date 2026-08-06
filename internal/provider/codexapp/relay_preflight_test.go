package codexapp

import (
	"context"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestDevLocalCodexLaunchIgnoresResidualRelayEnv(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "https://relay.example.test/v1")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "privileged")
	d := &driver{logRuntime: testLoggerRuntime(t), mirror: &recordingSkillMirrorReconciler{}}
	_, err := d.prepareStartSessionRequest(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-local",
		CWD:     t.TempDir(),
		Config:  map[string]any{contract.CodexHomeKey: mustDefaultCodexCLIHomeForTest(t)},
	})
	if err != nil {
		t.Fatalf("prepareStartSessionRequest() error = %v, want dev/local launch to ignore residual relay env", err)
	}
}

func TestPackagedAppManagedCodexLaunchFailsFastWithPartialRelayEnv(t *testing.T) {
	t.Setenv(providershared.RuntimeModeEnv, providershared.RuntimeModePackaged)
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BASE_URL", "https://relay.example.test/v1")
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN", "")
	d := &driver{logRuntime: testLoggerRuntime(t), mirror: &recordingSkillMirrorReconciler{}}
	_, err := d.prepareStartSessionRequest(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-managed",
		CWD:     t.TempDir(),
		Config:  map[string]any{contract.CodexHomeKey: ""},
	})
	if err == nil {
		t.Fatal("prepareStartSessionRequest() error = nil, want app-managed relay validation failure")
	}
	if !strings.Contains(err.Error(), "app-managed Codex relay config") || !strings.Contains(err.Error(), "SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN") {
		t.Fatalf("prepareStartSessionRequest() error = %v, want app-managed relay config failure", err)
	}
}

func TestPackagedAppManagedCodexLaunchRejectsPrivilegedRelayAPIKey(t *testing.T) {
	t.Setenv(providershared.RuntimeModeEnv, providershared.RuntimeModePackaged)
	t.Setenv("SUPER_DOLPHIN_CODEX_RELAY_API_KEY", "privileged")
	d := &driver{logRuntime: testLoggerRuntime(t), mirror: &recordingSkillMirrorReconciler{}}
	_, err := d.prepareStartSessionRequest(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-managed",
		CWD:     t.TempDir(),
		Config:  map[string]any{contract.CodexHomeKey: ""},
	})
	if err == nil {
		t.Fatal("prepareStartSessionRequest() error = nil, want privileged relay API key rejection")
	}
	if !strings.Contains(err.Error(), "SUPER_DOLPHIN_CODEX_RELAY_API_KEY") || strings.Contains(err.Error(), "use SUPER_DOLPHIN_CODEX_RELAY_BOOTSTRAP_TOKEN") {
		t.Fatalf("prepareStartSessionRequest() error = %v, want privileged key rejection without bootstrap-token suggestion", err)
	}
}

func mustDefaultCodexCLIHomeForTest(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return mustCanonicalCodexHome(t, home)
}
