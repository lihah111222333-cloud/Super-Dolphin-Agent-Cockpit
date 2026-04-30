package nativefilter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// newTestFilter 构造一个 baseFn 可控的 Filter，便于单元测试。
// post-flag-removal: enabled 字段已删，所有 Filter 默认就是 active。
func newTestFilter(t *testing.T, store *skilllibrary.Store, baseFn func() (Config, error)) *Filter {
	t.Helper()
	return &Filter{store: store, baseFn: baseFn}
}

func TestFilter_NilReceiverSafe(t *testing.T) {
	var f *Filter
	if err := f.Apply("/anywhere"); err != nil {
		t.Errorf("nil Filter.Apply must be no-op, got %v", err)
	}
}

// TestFilter_NilStoreSafe 锁定 store=nil（fx optional 注入未提供）时 Apply 是 no-op
// 不报错也不写文件——避免 mcp-orch standalone 模式或测试 fixture 把它当强依赖。
func TestFilter_NilStoreSafe(t *testing.T) {
	ws := t.TempDir()
	f := newTestFilter(t, nil, func() (Config, error) { return Config{}, nil })
	if err := f.Apply(ws); err != nil {
		t.Errorf("nil store should no-op, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("nil store Apply must not create settings.json (err=%v)", err)
	}
}

func TestFilter_WritesSettings(t *testing.T) {
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
	})
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
