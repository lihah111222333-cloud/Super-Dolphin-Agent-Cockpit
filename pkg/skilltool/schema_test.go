package skilltool

import (
	"encoding/json"
	"testing"
)

// TestToolNamesAreSnakeCase 锁定命名稳定性：模型可见的工具名一旦改动，会
// 破坏所有已部署 client 的 prompt 模板。本测试硬编码字面量做防漂移。
func TestToolNamesAreSnakeCase(t *testing.T) {
	if ToolNameExpandBody != "skill_expand_body" {
		t.Fatalf("ToolNameExpandBody changed: got %q", ToolNameExpandBody)
	}
	if ToolNameReadResource != "skill_read_resource" {
		t.Fatalf("ToolNameReadResource changed: got %q", ToolNameReadResource)
	}
}

// TestExpandBodyInputSchema_Required 验证 name 是 required + 不暴露 cwd 给模型。
func TestExpandBodyInputSchema_Required(t *testing.T) {
	s := ExpandBodyInputSchema()
	required, ok := s["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "name" {
		t.Fatalf("required must be [name], got %v", s["required"])
	}
	props, _ := s["properties"].(map[string]any)
	if _, leaked := props["cwd"]; leaked {
		t.Fatalf("cwd MUST NOT be exposed in model-facing schema; injected by host runtime")
	}
	wantTypes := map[string]string{"name": "string", "anchor": "string", "max_bytes": "integer"}
	for name, wantType := range wantTypes {
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("missing property %q in schema", name)
		}
		if got := prop["type"]; got != wantType {
			t.Fatalf("property %q type = %v, want %s", name, got, wantType)
		}
	}
	maxBytes := props["max_bytes"].(map[string]any)
	if got := maxBytes["minimum"]; got != 1 {
		t.Fatalf("max_bytes.minimum = %v, want 1", got)
	}
	if got := s["additionalProperties"]; got != false {
		t.Fatalf("additionalProperties = %v, want false", got)
	}
}

// TestReadResourceInputSchema_Required 验证 name + path 是 required，cwd 不暴露。
func TestReadResourceInputSchema_Required(t *testing.T) {
	s := ReadResourceInputSchema()
	required, ok := s["required"].([]string)
	if !ok || len(required) != 2 {
		t.Fatalf("required must be [name, path], got %v", s["required"])
	}
	gotSet := map[string]bool{}
	for _, r := range required {
		gotSet[r] = true
	}
	if !gotSet["name"] || !gotSet["path"] {
		t.Fatalf("required must contain name + path, got %v", required)
	}
	props, _ := s["properties"].(map[string]any)
	if _, leaked := props["cwd"]; leaked {
		t.Fatalf("cwd MUST NOT be exposed in model-facing schema")
	}
	wantTypes := map[string]string{"name": "string", "path": "string", "max_bytes": "integer"}
	for name, wantType := range wantTypes {
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("missing property %q in schema", name)
		}
		if got := prop["type"]; got != wantType {
			t.Fatalf("property %q type = %v, want %s", name, got, wantType)
		}
	}
	maxBytes := props["max_bytes"].(map[string]any)
	if got := maxBytes["minimum"]; got != 1 {
		t.Fatalf("max_bytes.minimum = %v, want 1", got)
	}
	if got := s["additionalProperties"]; got != false {
		t.Fatalf("additionalProperties = %v, want false", got)
	}
}

// TestSchemasMarshalAsJSON 验证 schema 可以被 json.Marshal 而不会 panic
// （map[string]any 内含复杂嵌套）。
func TestSchemasMarshalAsJSON(t *testing.T) {
	for _, s := range []map[string]any{ExpandBodyInputSchema(), ReadResourceInputSchema()} {
		if _, err := json.Marshal(s); err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
	}
}

func TestToolDescriptionsAreGolden(t *testing.T) {
	wantExpand := "Read the body of an installed skill (SKILL.md) by name. " +
		"Use this when a skill is listed in the system prompt but its full content is not yet " +
		"in context. Optionally pass an `anchor` (Markdown H2/H3 heading) to fetch only that " +
		"section. The host returns frontmatter-stripped body text; large files are truncated to " +
		"`max_bytes` (server may apply its own cap). Trust=project skills require user approval " +
		"on first call; the tool will return an approval-required error in that case."
	if DescriptionExpandBody != wantExpand {
		t.Fatalf("DescriptionExpandBody changed:\ngot  %q\nwant %q", DescriptionExpandBody, wantExpand)
	}

	wantRead := "Read a resource file co-located with an installed skill " +
		"(e.g. references/foo.md, scripts/bar.sh). Pass the skill `name` plus the relative `path` " +
		"inside the skill directory. Path traversal (..) and absolute paths are rejected. Binary " +
		"or non-UTF-8 files are rejected; this tool returns text only. Trust=project skills " +
		"require user approval on first call."
	if DescriptionReadResource != wantRead {
		t.Fatalf("DescriptionReadResource changed:\ngot  %q\nwant %q", DescriptionReadResource, wantRead)
	}
}

func TestInputSchemaDescriptionsAreGolden(t *testing.T) {
	expand := ExpandBodyInputSchema()
	assertSchemaDescription(t, expand, "name", "Skill name as listed in the available-skills section of the system prompt.")
	assertSchemaDescription(t, expand, "anchor", "Optional Markdown H2/H3 heading to slice. Empty/omitted returns the full body.")
	assertSchemaDescription(t, expand, "max_bytes", "Optional cap on returned body bytes. Server enforces its own ceiling.")

	read := ReadResourceInputSchema()
	assertSchemaDescription(t, read, "name", "Skill name owning the resource file.")
	assertSchemaDescription(t, read, "path", "Relative path inside the skill directory (e.g. `references/usage.md`). Absolute paths and `..` segments are rejected.")
	assertSchemaDescription(t, read, "max_bytes", "Optional cap on returned content bytes. Server enforces its own ceiling.")
}

func assertSchemaDescription(t *testing.T, schema map[string]any, property string, want string) {
	t.Helper()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties missing or wrong type: %T", schema["properties"])
	}
	prop, ok := props[property].(map[string]any)
	if !ok {
		t.Fatalf("property %q missing or wrong type: %T", property, props[property])
	}
	if got := prop["description"]; got != want {
		t.Fatalf("property %q description changed:\ngot  %q\nwant %q", property, got, want)
	}
}
