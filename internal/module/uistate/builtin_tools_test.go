package uistate

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

var testNativeTools = []contract.NativeToolDescriptor{
	{ID: "Read", Label: "读文件", Description: "读取文件", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
	{ID: "Write", Label: "写文件", Description: "写入文件", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
	{ID: "Bash", Label: "执行命令", Description: "执行命令", DefaultDisabled: true, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
	{ID: "WebFetch", Label: "抓取网页", Description: "抓取网页", DefaultDisabled: false, Provider: "claude", FilterMode: contract.NativeToolFilterModeHard},
	{ID: "shell", Label: "执行命令", Description: "Codex shell", DefaultDisabled: true, Provider: "codex", FilterMode: contract.NativeToolFilterModeSoft},
}

func testNativeToolIndex() map[string]contract.NativeToolDescriptor {
	return buildNativeToolIndex(testNativeTools)
}

func TestBuiltinToolsReadReturnsDefaultsWhenNoPreferenceStored(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{}
	toolIndex := testNativeToolIndex()
	res, err := readBuiltinTools(context.Background(), prefs, nil, testNativeTools, toolIndex, "/repo")
	if err != nil {
		t.Fatalf("readBuiltinTools error = %v", err)
	}
	if len(res.Tools) != len(testNativeTools) {
		t.Fatalf("tools length = %d, want %d", len(res.Tools), len(testNativeTools))
	}
	for _, view := range res.Tools {
		descriptor, ok := toolIndex[view.ID]
		if !ok {
			t.Fatalf("unknown tool id in response: %q", view.ID)
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
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
		prefs,
		&sharedFileStoreStub{},
		nil,
	)

	res := dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/write", `{"cwd":"/repo","id":"Read","enabled":true}`)
	readEnabled := toolViewByID(res.Tools, "Read")
	if readEnabled == nil || !readEnabled.Enabled {
		t.Fatalf("Read enabled after write = %#v", readEnabled)
	}
	res = dispatchConfig[builtinToolsReadResult](t, server, "config/builtinTools/write", `{"cwd":"/repo","id":"WebFetch","enabled":false}`)
	webFetch := toolViewByID(res.Tools, "WebFetch")
	if webFetch == nil || webFetch.Enabled {
		t.Fatalf("WebFetch disabled after write = %#v", webFetch)
	}
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo", testNativeTools, testNativeToolIndex())
	want := []string{"Bash", "WebFetch", "Write", "shell"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools = %#v, want %#v", got, want)
	}
}

func TestBuiltinToolsWriteRejectsUnknownID(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{}
	server := newConfigTestServer(
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
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
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/repo"},
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
	got := ResolveDisabledBuiltinTools(context.Background(), nil, "/repo", testNativeTools, testNativeToolIndex())
	want := []string{"Bash", "Read", "Write", "shell"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools(nil prefs) = %#v, want %#v", got, want)
	}
}

func TestResolveDisabledBuiltinToolsHonorsExplicitEmptyOverride(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", builtinToolsDisabledKey): json.RawMessage(`[]`),
	}}
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo", testNativeTools, testNativeToolIndex())
	if len(got) != 0 {
		t.Fatalf("ResolveDisabledBuiltinTools(explicit empty) = %#v, want empty", got)
	}
}

func TestResolveDisabledBuiltinToolsMergesDefaultsForLegacyStoredSet(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", builtinToolsDisabledKey): json.RawMessage(`["shell"]`),
	}}
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo", testNativeTools, testNativeToolIndex())
	want := []string{"Bash", "Read", "Write", "shell"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools(legacy prefs) = %#v, want %#v", got, want)
	}
}

func TestResolveDisabledBuiltinToolsRespectsKnownEnabledDefaults(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", builtinToolsDisabledKey): json.RawMessage(`["shell"]`),
		preferenceStubKey("/repo", builtinToolsKnownIDsKey): json.RawMessage(`["Bash","shell"]`),
	}}
	got := ResolveDisabledBuiltinTools(context.Background(), prefs, "/repo", testNativeTools, testNativeToolIndex())
	want := []string{"Read", "Write", "shell"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveDisabledBuiltinTools(known prefs) = %#v, want %#v", got, want)
	}
}

func TestResolveSoftFilteredBuiltinToolsReturnsOnlySoftTools(t *testing.T) {
	t.Parallel()
	got := ResolveSoftFilteredBuiltinTools(context.Background(), nil, "/repo", testNativeTools, testNativeToolIndex(), "")
	want := []string{"shell"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveSoftFilteredBuiltinTools(nil prefs) = %#v, want %#v", got, want)
	}
}

func TestResolveSoftFilteredBuiltinToolsFiltersProvider(t *testing.T) {
	t.Parallel()
	got := ResolveSoftFilteredBuiltinTools(context.Background(), nil, "/repo", testNativeTools, testNativeToolIndex(), "claude")
	if len(got) != 0 {
		t.Fatalf("ResolveSoftFilteredBuiltinTools(provider=claude) = %#v, want none", got)
	}
}

func TestBuiltinToolsReadReturnsEnforcementTier(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{}
	res, err := readBuiltinTools(context.Background(), prefs, nil, testNativeTools, testNativeToolIndex(), "/repo")
	if err != nil {
		t.Fatalf("readBuiltinTools error = %v", err)
	}
	want := map[string]string{
		"Read":     string(contract.NativeToolEnforcementNativeHard),
		"Write":    string(contract.NativeToolEnforcementNativeHard),
		"Bash":     string(contract.NativeToolEnforcementNativeHard),
		"WebFetch": "",
		"shell":    string(contract.NativeToolEnforcementNativeHard),
	}
	for _, view := range res.Tools {
		if view.Enforcement != want[view.ID] {
			t.Fatalf("tool %q Enforcement = %q, want %q", view.ID, view.Enforcement, want[view.ID])
		}
	}
}

func TestResolveHardEnabledBuiltinToolsReturnsClaudeAllowlist(t *testing.T) {
	t.Parallel()
	got := ResolveHardEnabledBuiltinTools(context.Background(), nil, "/repo", testNativeTools, testNativeToolIndex(), "claude")
	want := []string{"WebFetch"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveHardEnabledBuiltinTools(nil prefs) = %#v, want %#v", got, want)
	}
}

func TestResolveHardEnabledBuiltinToolsHonorsExplicitEmptyOverride(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", builtinToolsDisabledKey): json.RawMessage(`[]`),
	}}
	got := ResolveHardEnabledBuiltinTools(context.Background(), prefs, "/repo", testNativeTools, testNativeToolIndex(), "claude")
	want := []string{"Bash", "Read", "WebFetch", "Write"}
	if !equalSortedStrings(got, want) {
		t.Fatalf("ResolveHardEnabledBuiltinTools(explicit empty) = %#v, want %#v", got, want)
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

func TestBuiltinToolsReadIncludesMultipleProviders(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{}
	res, err := readBuiltinTools(context.Background(), prefs, nil, testNativeTools, testNativeToolIndex(), "/repo")
	if err != nil {
		t.Fatalf("readBuiltinTools error = %v", err)
	}
	providers := make(map[string]bool)
	for _, view := range res.Tools {
		providers[view.Provider] = true
	}
	if !providers["claude"] {
		t.Errorf("expected claude provider in tools")
	}
	if !providers["codex"] {
		t.Errorf("expected codex provider in tools")
	}
}

func TestBuiltinToolsReadReturnsFilterMode(t *testing.T) {
	t.Parallel()
	prefs := &uiPreferenceStoreStub{}
	toolIndex := testNativeToolIndex()
	res, err := readBuiltinTools(context.Background(), prefs, nil, testNativeTools, toolIndex, "/repo")
	if err != nil {
		t.Fatalf("readBuiltinTools error = %v", err)
	}
	for _, view := range res.Tools {
		descriptor := toolIndex[view.ID]
		want := string(descriptor.FilterMode)
		if view.FilterMode != want {
			t.Errorf("tool %q FilterMode = %q, want %q", view.ID, view.FilterMode, want)
		}
	}
}
