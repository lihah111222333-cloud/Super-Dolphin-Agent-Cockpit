package skilllibrary

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_FiresOnSkillFileChange(t *testing.T) {
	srcRoot := t.TempDir()
	mkDevSkill(t, srcRoot, "demo", "v1")

	events := make(chan string, 4)
	w, err := NewDevWatcher(srcRoot, func(name string) {
		events <- name
	})
	if err != nil {
		t.Fatalf("NewDevWatcher: %v", err)
	}
	defer w.Close()

	// 触发一次变更
	mkDevSkill(t, srcRoot, "demo", "v2")

	select {
	case got := <-events:
		if got != "demo" {
			t.Errorf("event name = %s, want demo", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after 2s")
	}
}

func TestWatcher_CloseStopsCallbacks(t *testing.T) {
	srcRoot := t.TempDir()
	events := make(chan string, 4)
	w, err := NewDevWatcher(srcRoot, func(name string) { events <- name })
	if err != nil {
		t.Fatalf("NewDevWatcher: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Subsequent Close should not panic
	_ = w.Close()
}

func mkDevSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: "+body+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
