package nativefilter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// newTestFilter 构造一个绕过环境变量、可控的 Filter，便于单元测试。
func newTestFilter(t *testing.T, store *skilllibrary.Store, baseFn func() (Config, error), enabled bool) *Filter {
	t.Helper()
	return &Filter{store: store, baseFn: baseFn, enabled: enabled}
}

func TestFilter_DisabledNoOp(t *testing.T) {
	ws := t.TempDir()
	f := newTestFilter(t, nil, func() (Config, error) { return Config{}, nil }, false)
	if err := f.Apply(ws); err != nil {
		t.Fatalf("disabled Filter.Apply should not error: %v", err)
	}
	// 不应写出 settings.json
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("disabled Apply must not create settings.json (err=%v)", err)
	}
}

func TestFilter_NilReceiverSafe(t *testing.T) {
	var f *Filter
	if err := f.Apply("/anywhere"); err != nil {
		t.Errorf("nil Filter.Apply must be no-op, got %v", err)
	}
	if f.Enabled() {
		t.Errorf("nil Filter.Enabled must be false")
	}
}

func TestFilter_NilStoreSafe(t *testing.T) {
	ws := t.TempDir()
	f := newTestFilter(t, nil, func() (Config, error) { return Config{}, nil }, true)
	if err := f.Apply(ws); err != nil {
		t.Errorf("nil store with enabled flag should no-op, got %v", err)
	}
}

func TestFilter_EnabledWritesSettings(t *testing.T) {
	// 用真 skilllibrary.Store 的 fixture：临时 library 含一个 active skill
	// 声明 ReplacesNative.claude=["simplify"]
	libDir := t.TempDir()
	store := skilllibrary.NewStore(libDir)
	skillMD := []byte("---\nname: tdd-replacement\ndescription: x\n---\n# tdd\n")
	meta := skilllibrary.SkillMeta{
		Name:           "tdd-replacement",
		Origin:         skilllibrary.OriginLocal,
		Version:        "0.0.1",
		VersionHash:    "deadbeef",
		ReplacesNative: map[string][]string{"claude": {"simplify"}},
	}
	if err := store.Install("tdd-replacement", skillMD, meta); err != nil {
		t.Fatal(err)
	}

	ws := t.TempDir()
	f := newTestFilter(t, store, func() (Config, error) {
		return Config{Claude: ClaudeConfig{DisabledSkills: []string{"init"}}}, nil
	}, true)
	if err := f.Apply(ws); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(ws, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(body, "Skill:simplify") {
		t.Errorf("expected Skill:simplify (from skill ReplacesNative), got: %s", body)
	}
	if !contains(body, "Skill:init") {
		t.Errorf("expected Skill:init (from base config), got: %s", body)
	}
}

func TestNewFilter_RespectsEnvFlag(t *testing.T) {
	t.Setenv(envFlag, "on")
	libDir := t.TempDir()
	f := NewFilter(skilllibrary.NewStore(libDir))
	if !f.Enabled() {
		t.Errorf("env=on should enable filter")
	}

	t.Setenv(envFlag, "")
	f2 := NewFilter(skilllibrary.NewStore(libDir))
	if f2.Enabled() {
		t.Errorf("env=\"\" should disable filter")
	}
}

func contains(haystack []byte, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack []byte, needle string) int {
	n := len(needle)
	if n == 0 {
		return 0
	}
	for i := 0; i+n <= len(haystack); i++ {
		if string(haystack[i:i+n]) == needle {
			return i
		}
	}
	return -1
}
