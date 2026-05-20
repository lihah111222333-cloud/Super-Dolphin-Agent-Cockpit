package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAutoDreamIntentRoundTrip(t *testing.T) {
	root := t.TempDir()

	assertAutoDreamIntentMissing(t, root)
	writeAutoDreamIntent(t, root, true)
	assertAutoDreamIntentValue(t, root, true, "after true")
	writeAutoDreamIntent(t, root, false)
	assertAutoDreamIntentValue(t, root, false, "after false")
}

func TestAutoDreamIntentEmptyRootDir(t *testing.T) {
	if got, err := ReadAutoDreamIntent(""); err != nil || got != nil {
		t.Fatalf("ReadAutoDreamIntent(\"\") = %v err=%v, want nil nil", got, err)
	}
	if err := WriteAutoDreamIntent("", true); err == nil {
		t.Fatal("WriteAutoDreamIntent(\"\") error = nil, want non-nil")
	}
}

func TestSetAutoDreamIntentRPCPersists(t *testing.T) {
	root := t.TempDir()
	cfg := &Config{
		Enabled:     true,
		EnableTools: true,
		RootDir:     root,
		ProjectRoot: filepath.Join(root, "project"),
	}
	deps := memoryHandlerDeps{
		Service: newServiceWithConsolidator(cfg, nil, nil, nil),
	}

	resp, err := setAutoDreamIntent(context.Background(), deps, uiAutoDreamIntentParams{Enabled: true})
	if err != nil {
		t.Fatalf("setAutoDreamIntent(true) error = %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("setAutoDreamIntent(true) resp = %#v, want ok=true", resp)
	}
	got, err := ReadAutoDreamIntent(root)
	if err != nil || got == nil || *got != true {
		t.Fatalf("ReadAutoDreamIntent after RPC = %v err=%v, want *true", got, err)
	}

	if _, err := setAutoDreamIntent(context.Background(), deps, uiAutoDreamIntentParams{Enabled: false}); err != nil {
		t.Fatalf("setAutoDreamIntent(false) error = %v", err)
	}
	got, _ = ReadAutoDreamIntent(root)
	if got == nil || *got != false {
		t.Fatalf("ReadAutoDreamIntent after RPC(false) = %v, want *false", got)
	}
}

func writeAutoDreamIntent(t *testing.T, root string, enabled bool) {
	t.Helper()
	if err := WriteAutoDreamIntent(root, enabled); err != nil {
		t.Fatalf("WriteAutoDreamIntent(%v) error = %v", enabled, err)
	}
}

func assertAutoDreamIntentMissing(t *testing.T, root string) {
	t.Helper()
	if got, err := ReadAutoDreamIntent(root); err == nil || got != nil {
		t.Fatalf("ReadAutoDreamIntent(missing) = %v err=%v, want missing file error", got, err)
	}
}

func assertAutoDreamIntentValue(t *testing.T, root string, want bool, label string) {
	t.Helper()
	got, err := ReadAutoDreamIntent(root)
	if err != nil || got == nil || *got != want {
		t.Fatalf("ReadAutoDreamIntent(%s) = %v err=%v, want *%v", label, got, err, want)
	}
}
