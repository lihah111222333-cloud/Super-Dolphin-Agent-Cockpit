package claudecli

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/cliadapter"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// TestSmoke_StartSessionWritesNativeFilterSettings exercises the real
// driver.StartSession → start → prepareSessionStart → applyNativeFilter call
// chain end-to-end. launchCLI fails (no Claude binary in test env), but
// applyNativeFilter runs before launchCLI in prepareSessionStart, so
// settings.local.json is on disk before the error propagates back.
//
// This is the closest "real session smoke" we can run without a live Claude
// CLI subprocess. It guards against regressions where the call site of
// applyNativeFilter gets accidentally moved/removed from prepareSessionStart.
func TestSmoke_StartSessionWritesNativeFilterSettings(t *testing.T) {
	libRoot := t.TempDir()
	store := skilllibrary.NewStore(libRoot)
	src := []byte("---\nname: smoke-skill\ndescription: smoke skill body\n---\n# x\n## A\nbody\n")
	if err := store.Install("smoke-skill", src, skilllibrary.SkillMeta{
		Name:           "smoke-skill",
		Origin:         skilllibrary.OriginBuiltin,
		Version:        "1",
		AllowedTools:   []string{"Read", "Edit"},
		ReplacesNative: map[string][]string{"claude": {"replaced-native-skill"}},
	}); err != nil {
		t.Fatalf("install smoke skill: %v", err)
	}

	workspace := t.TempDir()
	d := &driver{
		logger:           slog.New(slog.NewTextHandler(os.Stderr, nil)),
		binaryPath:       "/nonexistent/claude-fake-binary",
		proxyAddrFn:      func() string { return "" },
		skillStore:       store,
		nativeFilterPath: filepath.Join(t.TempDir(), "no-base-config.json"),
	}

	req := dto.StartSessionRequest{
		AgentID: "smoke-agent",
		CWD:     workspace,
		Model:   "claude-opus-4-7",
		Config:  map[string]any{},
	}

	// Call the real StartSession entry point. Expect launchCLI to fail since
	// the binary path is intentionally invalid; we only care that
	// applyNativeFilter runs first.
	_, err := d.StartSession(context.Background(), req)
	if err == nil {
		t.Fatalf("expected StartSession to fail (no real Claude binary), got nil")
	}

	settingsPath := filepath.Join(workspace, ".claude", cliadapter.SettingsFileName)
	data, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		t.Fatalf("settings.local.json not written before launchCLI failure: %v", readErr)
	}
	var got struct {
		Permissions struct {
			Deny  []string `json:"deny"`
			Allow []string `json:"allow"`
		} `json:"permissions"`
		HarnessManagedAt string `json:"_harness_managed_at"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("settings.local.json invalid JSON: %v", err)
	}
	denyJoined := strings.Join(got.Permissions.Deny, "|")
	if !strings.Contains(denyJoined, "Skill(replaced-native-skill)") {
		t.Errorf("Deny missing Skill(replaced-native-skill): %v", got.Permissions.Deny)
	}
	allowJoined := strings.Join(got.Permissions.Allow, "|")
	for _, want := range []string{"Read", "Edit"} {
		if !strings.Contains(allowJoined, want) {
			t.Errorf("Allow missing %q: %v", want, got.Permissions.Allow)
		}
	}
	if got.HarnessManagedAt == "" {
		t.Error("HarnessManagedAt marker missing")
	}
}
