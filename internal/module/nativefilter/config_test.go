package nativefilter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig_FullClaude(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "native-cli-filter.json")
	body := `{
      "claude": {
        "disabled_skills": ["simplify", "init"],
        "disabled_tools": ["Read"],
        "allowed_tools": null
      },
      "codex": {
        "disabled_tools": []
      }
    }`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Claude.DisabledSkills) != 2 {
		t.Fatalf("disabled_skills len = %d, want 2: %v", len(cfg.Claude.DisabledSkills), cfg.Claude.DisabledSkills)
	}
	if cfg.Claude.DisabledSkills[0] != "simplify" || cfg.Claude.DisabledSkills[1] != "init" {
		t.Errorf("disabled_skills wrong: %v", cfg.Claude.DisabledSkills)
	}
	if len(cfg.Claude.DisabledTools) != 1 || cfg.Claude.DisabledTools[0] != "Read" {
		t.Errorf("disabled_tools wrong: %v", cfg.Claude.DisabledTools)
	}
	if cfg.Claude.AllowedTools != nil {
		t.Errorf("allowed_tools=null should yield nil slice, got %v", cfg.Claude.AllowedTools)
	}
}

func TestLoadConfig_MissingFileReturnsEmpty(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/native-cli-filter.json")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if cfg.Claude.DisabledSkills != nil || cfg.Codex.DisabledTools != nil {
		t.Errorf("missing file should yield empty config, got %+v", cfg)
	}
}

func TestLoadConfig_MalformedJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("malformed json should return error")
	}
}

func TestLoadConfig_PartialClaudeOnly(t *testing.T) {
	// codex 段缺失也应正常解析为空 CodexConfig
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.json")
	if err := os.WriteFile(path, []byte(`{"claude":{"disabled_skills":["x"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Claude.DisabledSkills) != 1 || cfg.Claude.DisabledSkills[0] != "x" {
		t.Errorf("partial claude wrong: %+v", cfg.Claude)
	}
	if cfg.Codex.DisabledTools != nil {
		t.Errorf("missing codex section should yield nil, got %v", cfg.Codex.DisabledTools)
	}
}

func TestLoadConfig_EmptyJSONObject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Claude.DisabledSkills != nil || cfg.Codex.DisabledTools != nil {
		t.Errorf("empty {} should yield empty config, got %+v", cfg)
	}
}
