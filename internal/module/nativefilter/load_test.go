package nativefilter

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadBaseConfig_EmptyPath(t *testing.T) {
	cfg, err := LoadBaseConfig("")
	if err != nil {
		t.Fatalf("empty path should be fail-open: %v", err)
	}
	if !reflect.DeepEqual(cfg, BaseConfig{}) {
		t.Errorf("empty path should yield zero BaseConfig: %+v", cfg)
	}
}

func TestLoadBaseConfig_MissingFile(t *testing.T) {
	cfg, err := LoadBaseConfig(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should be fail-open: %v", err)
	}
	if !reflect.DeepEqual(cfg, BaseConfig{}) {
		t.Errorf("missing file should yield zero BaseConfig: %+v", cfg)
	}
}

func TestLoadBaseConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "filter.json")
	body := `{
  "claude": {
    "disabled_skills": ["math-olympiad", "playground"],
    "disabled_tools": ["Bash"],
    "allowed_tools": ["Read", "Edit"]
  },
  "codex": {
    "disabled_tools": ["web_search"]
  }
}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBaseConfig(p)
	if err != nil {
		t.Fatalf("LoadBaseConfig: %v", err)
	}
	if cfg.Claude.DisabledSkills[0] != "math-olympiad" {
		t.Errorf("disabled_skills not parsed: %+v", cfg.Claude.DisabledSkills)
	}
	if cfg.Claude.AllowedTools == nil {
		t.Fatal("allowed_tools should be non-nil pointer when present in JSON")
	}
	if (*cfg.Claude.AllowedTools)[0] != "Read" {
		t.Errorf("allowed_tools not parsed: %+v", *cfg.Claude.AllowedTools)
	}
	if cfg.Codex.DisabledTools[0] != "web_search" {
		t.Errorf("codex disabled_tools not parsed: %+v", cfg.Codex.DisabledTools)
	}
}

func TestLoadBaseConfig_NullAllowedToolsStaysNil(t *testing.T) {
	// allowed_tools: null 是 spec §8.1 的合法语义（不施加 allowlist）。
	// LoadBaseConfig 必须保留 nil 指针，AggregateClaude 才能正确区分
	// "未声明 allowlist" 与 "声明空 allowlist"。
	dir := t.TempDir()
	p := filepath.Join(dir, "filter.json")
	body := `{"claude": {"allowed_tools": null}, "codex": {}}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadBaseConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Claude.AllowedTools != nil {
		t.Errorf("allowed_tools=null should yield nil pointer: %v", cfg.Claude.AllowedTools)
	}
}

func TestLoadBaseConfig_InvalidJSONReturnsError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "filter.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseConfig(p); err == nil {
		t.Fatal("invalid JSON should return error, not silent fail-open")
	}
}
