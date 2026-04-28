package memory

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAutoDreamIntentRoundTrip(t *testing.T) {
	root := t.TempDir()

	got, err := ReadAutoDreamIntent(root)
	if err != nil {
		t.Fatalf("ReadAutoDreamIntent(empty) error = %v", err)
	}
	if got != nil {
		t.Fatalf("ReadAutoDreamIntent(empty) = %v, want nil", *got)
	}

	if err := WriteAutoDreamIntent(root, true); err != nil {
		t.Fatalf("WriteAutoDreamIntent(true) error = %v", err)
	}
	got, err = ReadAutoDreamIntent(root)
	if err != nil || got == nil || *got != true {
		t.Fatalf("ReadAutoDreamIntent(after true) = %v err=%v, want *true", got, err)
	}

	if err := WriteAutoDreamIntent(root, false); err != nil {
		t.Fatalf("WriteAutoDreamIntent(false) error = %v", err)
	}
	got, err = ReadAutoDreamIntent(root)
	if err != nil || got == nil || *got != false {
		t.Fatalf("ReadAutoDreamIntent(after false) = %v err=%v, want *false", got, err)
	}
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
