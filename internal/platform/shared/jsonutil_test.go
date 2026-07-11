package shared

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

func TestDecodeInputRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	var input struct {
		DryRun bool `json:"dry_run,omitempty"`
	}
	err := DecodeInput(json.RawMessage(`{"dry_run":true,"dryRun":true}`), &input)
	if err == nil {
		t.Fatal("DecodeInput() error = nil, want unknown dryRun rejection")
	}
	if !strings.Contains(err.Error(), `unknown field "dryRun"`) {
		t.Fatalf("DecodeInput() error = %v, want unknown field dryRun", err)
	}
}

func TestDecodeInputAllowsDeclaredLegacyAlias(t *testing.T) {
	t.Parallel()

	var input struct {
		TemplateID      string `json:"template_id,omitempty"`
		TemplateIDCamel string `json:"templateId,omitempty"`
	}
	if err := DecodeInput(json.RawMessage(`{"templateId":"tpl-1"}`), &input); err != nil {
		t.Fatalf("DecodeInput() error = %v, want declared alias accepted", err)
	}
	if input.TemplateIDCamel != "tpl-1" {
		t.Fatalf("TemplateIDCamel = %q, want tpl-1", input.TemplateIDCamel)
	}
}

func TestCloneSelectorClonesScope(t *testing.T) {
	t.Parallel()

	scope := &mcp.SelectorScope{AgentID: "agent-1"}
	cloned := CloneSelector(mcp.Selector{
		Subscription: "topic",
		Scope:        scope,
	})
	if cloned.Scope == nil {
		t.Fatal("scope = nil")
	}
	if cloned.Scope == scope {
		t.Fatal("scope pointer was not cloned")
	}
	scope.AgentID = "agent-2"
	if cloned.Scope.AgentID != "agent-1" {
		t.Fatalf("cloned scope = %#v", cloned.Scope)
	}
}

func TestCloneHookPayloadClonesContext(t *testing.T) {
	t.Parallel()

	payload := mcp.HookPayload{
		Topic:   "before",
		Context: json.RawMessage(`{"ok":true}`),
	}
	cloned := CloneHookPayload(payload)
	if len(cloned.Context) == 0 {
		t.Fatal("cloned context is empty")
	}
	payload.Context[2] = 'X'
	if string(cloned.Context) != `{"ok":true}` {
		t.Fatalf("cloned context = %s", cloned.Context)
	}
}

func TestCloneStringMapClonesEntries(t *testing.T) {
	t.Parallel()

	input := map[string]string{"k": "v"}
	cloned := CloneStringMap(input)
	if cloned["k"] != "v" {
		t.Fatalf("cloned map = %#v", cloned)
	}
	input["k"] = "changed"
	if cloned["k"] != "v" {
		t.Fatalf("cloned map mutated = %#v", cloned)
	}
}

func TestNormalizeAbsolutePath(t *testing.T) {
	t.Parallel()

	if path, err := NormalizeAbsolutePath("   "); err != nil || path != "" {
		t.Fatalf("blank path = %q, %v", path, err)
	}

	root := t.TempDir()
	path, err := NormalizeAbsolutePath("  " + filepath.Join(root, ".", "child", "..") + "  ")
	if err != nil {
		t.Fatalf("NormalizeAbsolutePath error = %v", err)
	}
	want := filepath.Clean(root)
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = filepath.Clean(resolved)
	}
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}
