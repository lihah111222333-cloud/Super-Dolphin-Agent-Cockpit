package cliadapter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/nativefilter"
)

func TestWriteClaudeSettingsLocal_EmptyWorkspaceErrors(t *testing.T) {
	if err := WriteClaudeSettingsLocal("", nativefilter.ClaudeSettings{}); !errors.Is(err, ErrEmptyArgs) {
		t.Fatalf("want ErrEmptyArgs, got %v", err)
	}
}

func TestWriteClaudeSettingsLocal_CreatesClaudeDirAndFile(t *testing.T) {
	ws := t.TempDir()
	settings := nativefilter.ClaudeSettings{
		Permissions: nativefilter.ClaudePermissions{
			Deny:  []string{"Bash", "Skill(math-olympiad)"},
			Allow: []string{"Read"},
		},
	}
	if err := WriteClaudeSettingsLocal(ws, settings); err != nil {
		t.Fatalf("WriteClaudeSettingsLocal: %v", err)
	}
	target := filepath.Join(ws, ".claude", SettingsFileName)
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read written settings: %v", err)
	}
	var got settingsEnvelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal written settings: %v", err)
	}
	if len(got.Permissions.Deny) != 2 || got.Permissions.Deny[0] != "Bash" {
		t.Errorf("deny not preserved: %+v", got.Permissions.Deny)
	}
	if len(got.Permissions.Allow) != 1 || got.Permissions.Allow[0] != "Read" {
		t.Errorf("allow not preserved: %+v", got.Permissions.Allow)
	}
	if got.HarnessManagedAt == "" {
		t.Error("HarnessManagedAt marker missing")
	}
}

func TestWriteClaudeSettingsLocal_OverwritesExisting(t *testing.T) {
	ws := t.TempDir()
	claudeDir := filepath.Join(ws, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(claudeDir, SettingsFileName)
	if err := os.WriteFile(stale, []byte(`{"junk": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings := nativefilter.ClaudeSettings{
		Permissions: nativefilter.ClaudePermissions{Deny: []string{"Bash"}},
	}
	if err := WriteClaudeSettingsLocal(ws, settings); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(stale)
	if string(data) == `{"junk": true}` {
		t.Error("WriteClaudeSettingsLocal must overwrite stale content")
	}
	var got settingsEnvelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Permissions.Deny) != 1 || got.Permissions.Deny[0] != "Bash" {
		t.Errorf("deny not written: %+v", got.Permissions.Deny)
	}
}

func TestWriteClaudeSettingsLocal_CreatesNestedDirs(t *testing.T) {
	// workspaceDir 不存在时应当连同 .claude 一并创建——MkdirAll 兜底。
	ws := filepath.Join(t.TempDir(), "deep", "workspace")
	if err := WriteClaudeSettingsLocal(ws, nativefilter.ClaudeSettings{}); err != nil {
		t.Fatalf("WriteClaudeSettingsLocal on nested path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", SettingsFileName)); err != nil {
		t.Errorf("settings file not created: %v", err)
	}
}

func TestWriteClaudeSettingsLocal_OmitsEmptyPermissionFields(t *testing.T) {
	ws := t.TempDir()
	if err := WriteClaudeSettingsLocal(ws, nativefilter.ClaudeSettings{}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(ws, ".claude", SettingsFileName))
	// 空 Deny/Allow 不应落到 JSON（节省读取成本 + 让 settings 文件干净）。
	if got := string(data); contains(got, `"deny"`) || contains(got, `"allow"`) {
		t.Errorf("empty deny/allow should be omitted: %s", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
