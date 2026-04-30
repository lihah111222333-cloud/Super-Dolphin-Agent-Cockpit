package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/cliadapter"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// TestApplyNativeFilter_WritesSettingsFromActiveSkills covers the P5b 接线
// 路径：driver 启动 prepareSessionStart 时调用 applyNativeFilter，把 base
// config + active skills 聚合写到 <workspace>/.claude/settings.local.json。
func TestApplyNativeFilter_WritesSettingsFromActiveSkills(t *testing.T) {
	libRoot := t.TempDir()
	store := skilllibrary.NewStore(libRoot)

	// 装 2 个 skill：alpha 启用、beta 禁用（应被聚合时跳过）
	src := []byte("---\nname: alpha\ndescription: a\n---\n# x\n## A\nbody\n")
	if err := store.Install("alpha", src, skilllibrary.SkillMeta{
		Name:           "alpha",
		Origin:         skilllibrary.OriginBuiltin,
		Version:        "1",
		AllowedTools:   []string{"Read", "Edit"},
		ReplacesNative: map[string][]string{"claude": {"foo"}},
	}); err != nil {
		t.Fatalf("install alpha: %v", err)
	}
	if err := store.Install("beta", src, skilllibrary.SkillMeta{
		Name:           "beta",
		Origin:         skilllibrary.OriginBuiltin,
		Version:        "1",
		Disabled:       true,
		AllowedTools:   []string{"Bash"},
		ReplacesNative: map[string][]string{"claude": {"never-shown"}},
	}); err != nil {
		t.Fatalf("install beta: %v", err)
	}

	// 写一份 base config
	baseDir := t.TempDir()
	basePath := filepath.Join(baseDir, "native-cli-filter.json")
	baseBody := `{
  "claude": {
    "disabled_skills": ["math-olympiad"],
    "disabled_tools": ["WebFetch"]
  }
}`
	if err := os.WriteFile(basePath, []byte(baseBody), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	d := &driver{
		skillStore:       store,
		nativeFilterPath: basePath,
	}
	d.applyNativeFilter(workspace)

	settingsPath := filepath.Join(workspace, ".claude", cliadapter.SettingsFileName)
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings file not written: %v", err)
	}
	var got struct {
		Permissions struct {
			Deny  []string `json:"deny"`
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}

	// Deny 应当包含 base.disabled_tools (WebFetch 裸名) +
	// Skill(math-olympiad) (来自 base) + Skill(foo) (来自 alpha.ReplacesNative)
	// 不应包含 Skill(never-shown)（beta 被 Disabled）
	denyJoined := strings.Join(got.Permissions.Deny, "|")
	for _, want := range []string{"WebFetch", "Skill(foo)", "Skill(math-olympiad)"} {
		if !strings.Contains(denyJoined, want) {
			t.Errorf("Deny missing %q: %v", want, got.Permissions.Deny)
		}
	}
	if strings.Contains(denyJoined, "never-shown") {
		t.Errorf("Disabled skill leaked into Deny: %v", got.Permissions.Deny)
	}

	// Allow 应当只包含 alpha.AllowedTools (Read, Edit)；beta 被 Disabled 不参与。
	allowJoined := strings.Join(got.Permissions.Allow, "|")
	for _, want := range []string{"Read", "Edit"} {
		if !strings.Contains(allowJoined, want) {
			t.Errorf("Allow missing %q: %v", want, got.Permissions.Allow)
		}
	}
	if strings.Contains(allowJoined, "Bash") {
		t.Errorf("Disabled skill's AllowedTools leaked: %v", got.Permissions.Allow)
	}
}

// TestApplyNativeFilter_NilStoreNoOp 验证 driver 在 fx optional 注入未提供
// Store 时 applyNativeFilter 是 no-op，不报错也不写文件。
func TestApplyNativeFilter_NilStoreNoOp(t *testing.T) {
	workspace := t.TempDir()
	d := &driver{} // skillStore = nil
	d.applyNativeFilter(workspace)
	settingsPath := filepath.Join(workspace, ".claude", cliadapter.SettingsFileName)
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Errorf("nil store should be no-op; settings unexpectedly written: %v", err)
	}
}

// TestApplyNativeFilter_BaseConfigMissingFailOpen 验证 base config 文件不存在
// 时仍按空 base 完成聚合写入（fail-open 语义）。
func TestApplyNativeFilter_BaseConfigMissingFailOpen(t *testing.T) {
	libRoot := t.TempDir()
	store := skilllibrary.NewStore(libRoot)
	src := []byte("---\nname: alpha\ndescription: a\n---\n# x\n## A\nbody\n")
	if err := store.Install("alpha", src, skilllibrary.SkillMeta{
		Name:           "alpha",
		Origin:         skilllibrary.OriginBuiltin,
		Version:        "1",
		ReplacesNative: map[string][]string{"claude": {"foo"}},
	}); err != nil {
		t.Fatal(err)
	}

	workspace := t.TempDir()
	d := &driver{
		skillStore:       store,
		nativeFilterPath: filepath.Join(t.TempDir(), "does-not-exist.json"),
	}
	d.applyNativeFilter(workspace)

	settingsPath := filepath.Join(workspace, ".claude", cliadapter.SettingsFileName)
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings should still be written despite missing base: %v", err)
	}
}
