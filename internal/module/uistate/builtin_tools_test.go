package uistate

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

func TestBuiltinToolsReadReturnsDefaultsWhenNoPreferenceStored(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{}
	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
		prefs,
		&sharedFileStoreStub{},
		nil,
	)

	res := dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/read", `{"cwd":"/repo"}`)
	if len(res.Tools) != len(builtinToolRegistry) {
		t.Fatalf("tools length = %d, want %d", len(res.Tools), len(builtinToolRegistry))
	}
	for _, view := range res.Tools {
		descriptor, ok := builtinToolIndex[view.ID]
		if !ok {
			t.Fatalf("unknown tool id in response: %q", view.ID)
		}
		if view.Label != descriptor.Label {
			t.Fatalf("tool %q label = %q, want %q", view.ID, view.Label, descriptor.Label)
		}
		wantEnabled := !descriptor.DefaultDisabled
		if view.Enabled != wantEnabled {
			t.Fatalf("tool %q enabled = %v, want %v", view.ID, view.Enabled, wantEnabled)
		}
	}
}

func TestBuiltinToolsWritePersistsDisabledAndReturnsCurrentView(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{}
	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
		prefs,
		&sharedFileStoreStub{},
		nil,
	)

	// Enable the default-disabled Read tool.
	res := dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/write", `{"cwd":"/repo","id":"Read","enabled":true}`)
	readEnabled := toolViewByID(res.Tools, "Read")
	if readEnabled == nil || !readEnabled.Enabled {
		t.Fatalf("Read enabled after write = %#v", readEnabled)
	}
	// Disable WebFetch (default enabled).
	res = dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/write", `{"cwd":"/repo","id":"WebFetch","enabled":false}`)
	webFetch := toolViewByID(res.Tools, "WebFetch")
	if webFetch == nil || webFetch.Enabled {
		t.Fatalf("WebFetch disabled after write = %#v", webFetch)
	}

	// ResolveDisabledBuiltinTools should reflect the persisted state, not defaults.
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo")
	want := []string{"Bash", "Edit", "Glob", "Grep", "LS", "MultiEdit", "WebFetch", "Write"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools = %#v, want %#v", got, want)
	}

	// The stored preference should be a sorted string array including WebFetch.
	raw, err := prefs.GetValue(context.Background(), "/repo", builtinToolsDisabledKey)
	if err != nil {
		t.Fatalf("GetValue() error = %v", err)
	}
	var stored []string
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("json.Unmarshal(stored) error = %v; raw=%s", err, raw)
	}
	if !equalSortedStrings(stored, want) {
		t.Fatalf("stored disabled ids = %#v, want %#v", stored, want)
	}
}

func TestBuiltinToolsWriteRejectsUnknownID(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{}
	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
		prefs,
		&sharedFileStoreStub{},
		nil,
	)
	_, err := server.Dispatch(
		context.Background(),
		"config/builtinTools/write",
		json.RawMessage(`{"cwd":"/repo","id":"NotARealTool","enabled":false}`),
	)
	if err == nil {
		t.Fatalf("Dispatch(unknown id) error = nil, want non-nil")
	}
}

func TestBuiltinToolsWriteRequiresPreferenceStore(t *testing.T) {
	t.Parallel()

	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
		nil,
		&sharedFileStoreStub{},
		nil,
	)
	_, err := server.Dispatch(
		context.Background(),
		"config/builtinTools/write",
		json.RawMessage(`{"cwd":"/repo","id":"Read","enabled":true}`),
	)
	if err == nil || !strings.Contains(err.Error(), errConfigPreferenceStoreRequired.Error()) {
		t.Fatalf("Dispatch(no prefs) error = %v, want wrapped %v", err, errConfigPreferenceStoreRequired)
	}
}

func TestResolveDisabledBuiltinToolsFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	got := ResolveDisabledBuiltinTools(context.Background(), nil, "/repo")
	want := []string{"Bash", "Edit", "Glob", "Grep", "LS", "MultiEdit", "Read", "Write"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools(nil prefs) = %#v, want %#v", got, want)
	}
}

func TestResolveDisabledBuiltinToolsHonorsExplicitEmptyOverride(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", builtinToolsDisabledKey): json.RawMessage(`[]`),
	}}
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo")
	if len(got) != 0 {
		t.Fatalf("ResolveDisabledBuiltinTools(explicit empty) = %#v, want empty", got)
	}
}

func toolViewByID(views []BuiltinToolView, id string) *BuiltinToolView {
	for i := range views {
		if views[i].ID == id {
			return &views[i]
		}
	}
	return nil
}

func equalSortedStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

func TestBuiltinToolsReadIncludesProviderAndCodexNote(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{}
	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
		prefs,
		&sharedFileStoreStub{},
		nil,
	)

	res := dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/read", `{"cwd":"/repo"}`)

	// Every tool view must carry its provider so the UI can group them.
	for _, view := range res.Tools {
		if view.Provider != BuiltinToolProviderClaude {
			t.Errorf("tool %q provider = %q, want %q", view.ID, view.Provider, BuiltinToolProviderClaude)
		}
	}

	// The codex provider note must be present so the UI can render an
	// explanatory card under a Codex header even though codex tools are not
	// individually disable-able from this project.
	if len(res.ProviderNotes) == 0 {
		t.Fatalf("expected at least one provider note (codex), got none")
	}
	var sawCodex bool
	for _, note := range res.ProviderNotes {
		if note.Provider == BuiltinToolProviderCodex {
			sawCodex = true
			if !strings.Contains(note.Note, "JSON-RPC") {
				t.Errorf("codex note missing protocol explanation: %q", note.Note)
			}
		}
	}
	if !sawCodex {
		t.Errorf("provider notes missing codex entry: %+v", res.ProviderNotes)
	}
}
