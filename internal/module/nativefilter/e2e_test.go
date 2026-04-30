package nativefilter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// TestE2E_FullPipeline 构造一个完整的 fixture：
//   - 临时 library 含 1 个 active skill（ReplacesNative.claude=[simplify]）
//   - 1 个 disabled skill（ReplacesNative.claude=[loop]，必须被跳过）
//   - 1 个全局 base config 写 disabled_skills:[init] + disabled_tools:[Read]
//
// Apply 后断言 workspace settings.json 含 Skill:simplify + Skill:init + Read，
// 不含 Skill:loop（disabled skill 跳过）。覆盖 P5 主链路：
//
//	base config 解析 + skilllibrary.Store.List + AggregateReplacesNative +
//	BuildClaudeSettings + WriteWorkspaceSettings。
func TestE2E_FullPipeline(t *testing.T) {
	libDir := t.TempDir()
	store := skilllibrary.NewStore(libDir)

	// active skill：声明替代 native simplify
	if err := store.Install("tdd-replacement",
		[]byte("---\nname: tdd-replacement\ndescription: x\n---\n# tdd\n"),
		skilllibrary.SkillMeta{
			Name:           "tdd-replacement",
			Origin:         skilllibrary.OriginLocal,
			Version:        "0.0.1",
			VersionHash:    "h1",
			ReplacesNative: map[string][]string{"claude": {"simplify"}},
		}); err != nil {
		t.Fatal(err)
	}
	// disabled skill：ReplacesNative 必须被忽略
	if err := store.Install("dead-skill",
		[]byte("---\nname: dead-skill\ndescription: x\n---\n# dead\n"),
		skilllibrary.SkillMeta{
			Name:           "dead-skill",
			Origin:         skilllibrary.OriginLocal,
			Version:        "0.0.1",
			VersionHash:    "h2",
			Disabled:       true,
			ReplacesNative: map[string][]string{"claude": {"loop"}},
		}); err != nil {
		t.Fatal(err)
	}

	// base config：disabled_skills + disabled_tools
	baseFn := func() (Config, error) {
		return Config{
			Claude: ClaudeConfig{
				DisabledSkills: []string{"init"},
				DisabledTools:  []string{"Read"},
			},
		}, nil
	}

	// Apply 写入 workspace settings.json
	ws := t.TempDir()
	f := &Filter{store: store, baseFn: baseFn, enabled: true}
	if err := f.Apply(ws); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// 解析渲染结果
	body, err := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("parse settings.json: %v\nraw: %s", err, body)
	}
	deny := got.Permissions.Deny
	sort.Strings(deny)

	// 断言：Read（base disabled_tools）+ Skill:init（base disabled_skills）+
	//      Skill:simplify（active skill ReplacesNative）；不能含 Skill:loop（disabled）。
	want := []string{"Read", "Skill:init", "Skill:simplify"}
	if len(deny) != len(want) {
		t.Fatalf("deny len = %d, want %d: %v", len(deny), len(want), deny)
	}
	for i, w := range want {
		if deny[i] != w {
			t.Errorf("deny[%d] = %q, want %q (full: %v)", i, deny[i], w, deny)
		}
	}
	for _, d := range deny {
		if d == "Skill:loop" {
			t.Errorf("disabled skill ReplacesNative leaked into deny: %v", deny)
		}
	}
}

// TestE2E_DisabledFlagSkipsAll 验证 feature flag 关闭时端到端 no-op：
// 不写 settings.json 即使 store 含真实 ReplacesNative。
func TestE2E_DisabledFlagSkipsAll(t *testing.T) {
	libDir := t.TempDir()
	store := skilllibrary.NewStore(libDir)
	if err := store.Install("x",
		[]byte("---\nname: x\ndescription: x\n---\n"),
		skilllibrary.SkillMeta{
			Name:           "x",
			Origin:         skilllibrary.OriginLocal,
			Version:        "0",
			VersionHash:    "h",
			ReplacesNative: map[string][]string{"claude": {"would-be-blocked"}},
		}); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	f := &Filter{store: store, baseFn: func() (Config, error) {
		return Config{Claude: ClaudeConfig{DisabledSkills: []string{"init"}}}, nil
	}, enabled: false}
	if err := f.Apply(ws); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("disabled flag must not write settings.json (err=%v)", err)
	}
}
