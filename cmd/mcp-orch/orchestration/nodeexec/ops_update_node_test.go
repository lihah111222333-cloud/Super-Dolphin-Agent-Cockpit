package nodeexec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// NodePatch 严格 UnmarshalJSON 和 AssignedTo 三态行为的回归测试。
// 设计意图：update_node patch 顶层字段白名单 = {title, assigned_to, depends_on, config}。
// 任何其他键（含 status / node_key / node_type / agent_key）都要 fail-fast 拒绝，
// 防 AI 设计师或表单误传入「禁改字段」蒙混过关。

// TestNodePatch_AssignedTo_ThreeStates 验证 AssignedTo 与 DependsOn 同款三态：
//   - nil = 不改
//   - 指向 "" = 清空 assigned_to（拿走 agent 路由）
//   - 指向 "writer" = 改成此值
func TestNodePatch_AssignedTo_ThreeStates(t *testing.T) {
	t.Parallel()
	noChange := NodePatch{}
	data, _ := json.Marshal(noChange)
	if string(data) != `{}` {
		t.Errorf("AssignedTo nil 应 marshal 成 {}, got %s", data)
	}

	empty := ""
	clear := NodePatch{AssignedTo: &empty}
	data, _ = json.Marshal(clear)
	if string(data) != `{"assigned_to":""}` {
		t.Errorf("AssignedTo clear 应 marshal 成 {\"assigned_to\":\"\"}, got %s", data)
	}

	value := "writer"
	set := NodePatch{AssignedTo: &value}
	data, _ = json.Marshal(set)
	if string(data) != `{"assigned_to":"writer"}` {
		t.Errorf("AssignedTo set marshal got %s", data)
	}
}

// TestNodePatch_UnmarshalStrict_AcceptsAllowedKeys 白名单内的字段必须能正常解码。
func TestNodePatch_UnmarshalStrict_AcceptsAllowedKeys(t *testing.T) {
	t.Parallel()
	raw := `{"title":"new","assigned_to":"writer","depends_on":["a"],"config":{"x":1}}`
	var p NodePatch
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("allowed keys: unmarshal err = %v, want nil", err)
	}
	if p.Title == nil || *p.Title != "new" {
		t.Errorf("Title = %v, want 'new'", p.Title)
	}
	if p.AssignedTo == nil || *p.AssignedTo != "writer" {
		t.Errorf("AssignedTo = %v, want 'writer'", p.AssignedTo)
	}
	if p.DependsOn == nil || len(*p.DependsOn) != 1 || (*p.DependsOn)[0] != "a" {
		t.Errorf("DependsOn = %v, want [a]", p.DependsOn)
	}
	if string(p.Config) == "" {
		t.Errorf("Config 未解出来")
	}
}

// TestNodePatch_UnmarshalStrict_RejectsBannedKeys 关键 case：
// 禁改字段在 patch 顶层出现必须拒。
func TestNodePatch_UnmarshalStrict_RejectsBannedKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string // err 子串
	}{
		{"status", `{"status":"done"}`, "status"},
		{"node_key", `{"node_key":"x"}`, "node_key"},
		{"node_type", `{"node_type":"agent"}`, "node_type"},
		{"agent_key", `{"agent_key":"writer"}`, "agent_key"},
		{"unknown", `{"random_extra":"x"}`, "random_extra"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var p NodePatch
			err := json.Unmarshal([]byte(c.raw), &p)
			if err == nil {
				t.Fatalf("banned key %s: want err, got nil", c.name)
			}
			if !errors.Is(err, ErrNodePatchBannedField) {
				t.Fatalf("banned key %s: err = %v, want errors.Is ErrNodePatchBannedField", c.name, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("banned key %s: err = %v, should contain %q", c.name, err, c.want)
			}
		})
	}
}

// TestNodePatch_UnmarshalStrict_EmptyOk 空 patch 是合法的（noop）。
func TestNodePatch_UnmarshalStrict_EmptyOk(t *testing.T) {
	t.Parallel()
	var p NodePatch
	if err := json.Unmarshal([]byte(`{}`), &p); err != nil {
		t.Fatalf("empty patch: err = %v, want nil", err)
	}
}

// TestNodePatch_UnmarshalStrict_NestedBannedKeysInConfig 关键 case：
// 禁改字段藏在 patch.config 内层也要拒绝。
// agent_key 是例外：完整 config 覆盖必须能保留合法 exec.agent_key /
// exec.verifier.agent_key。
//
// 覆盖 4 个 banned key × 2 个嵌套深度（直接嵌 + 多层嵌套 + 数组里）。
func TestNodePatch_UnmarshalStrict_NestedBannedKeysInConfig(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want string // err 子串
	}{
		{"direct_agent_key", `{"config":{"agent_key":"evil"}}`, "agent_key"},
		{"direct_status", `{"config":{"status":"done"}}`, "status"},
		{"direct_node_key", `{"config":{"node_key":"x"}}`, "node_key"},
		{"direct_node_type", `{"config":{"node_type":"agent"}}`, "node_type"},
		{"nested_object_agent_key", `{"config":{"inner":{"deep":{"agent_key":"evil"}}}}`, "agent_key"},
		{"array_element_agent_key", `{"config":{"items":[{"foo":1},{"agent_key":"evil"}]}}`, "agent_key"},
		{"exec_automation_agent_key", `{"config":{"exec":{"automation":{"agent_key":"evil"}}}}`, "agent_key"},
		{"exec_array_agent_key", `{"config":{"exec":{"items":[{"agent_key":"evil"}]}}}`, "agent_key"},
		{"exec_array_direct_agent_key", `{"config":{"exec":[{"agent_key":"evil"}]}}`, "agent_key"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var p NodePatch
			err := json.Unmarshal([]byte(c.raw), &p)
			if err == nil {
				t.Fatalf("nested banned %s: want err, got nil", c.name)
			}
			if !errors.Is(err, ErrNodePatchBannedField) {
				t.Fatalf("nested banned %s: err = %v, want errors.Is ErrNodePatchBannedField", c.name, err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("nested banned %s: err = %v, should contain %q", c.name, err, c.want)
			}
		})
	}
}

func TestNodePatch_UnmarshalStrict_AllowsExecAgentKeyInFullConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "agent_exec",
			raw:  `{"config":{"exec":{"provider":"codex","model":"opus","agent_key":"writer"},"first_turn":"new prompt"}}`,
			want: `"agent_key":"writer"`,
		},
		{
			name: "hybrid_verifier_exec",
			raw:  `{"config":{"exec":{"automation":{"kind":"command_card","command_ref":"build"},"verifier":{"provider":"codex","model":"opus","agent_key":"reviewer","prompt_key":"main/reviewer","cwd":"/repo/app"}},"outputs":{"to_node_result":true}}}`,
			want: `"agent_key":"reviewer"`,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var p NodePatch
			if err := json.Unmarshal([]byte(c.raw), &p); err != nil {
				t.Fatalf("exec agent_key should be allowed for full config replace: %v", err)
			}
			if !strings.Contains(string(p.Config), c.want) {
				t.Fatalf("Config = %s, want preserved %s", p.Config, c.want)
			}
		})
	}
}

// TestNodePatch_UnmarshalStrict_ConfigNonBannedNestedOK 防误伤：
// config 内层任意「非 banned」字段必须放行。覆盖嵌套 object / 数组 / 标量。
func TestNodePatch_UnmarshalStrict_ConfigNonBannedNestedOK(t *testing.T) {
	t.Parallel()
	cases := []string{
		`{"config":{"prompt":"hi"}}`,
		`{"config":{"nested":{"more":{"value":1}}}}`,
		`{"config":{"items":[{"a":1},{"b":2}]}}`,
		`{"config":{"items":[1,2,3]}}`,
		`{"config":null}`,
	}
	for i, raw := range cases {
		raw := raw
		t.Run("ok_"+string(rune('0'+i)), func(t *testing.T) {
			t.Parallel()
			var p NodePatch
			if err := json.Unmarshal([]byte(raw), &p); err != nil {
				t.Fatalf("non-banned nested %q: err = %v, want nil", raw, err)
			}
		})
	}
}

// TestNodePatch_UnmarshalStrict_DecoderUnknownFieldErrorWrapped 确认
// json.Decoder.DisallowUnknownFields 返回的 unknown-field 错误被包成
// ErrNodePatchBannedField，errors.Is 与原 sentinel 行为一致。
func TestNodePatch_UnmarshalStrict_DecoderUnknownFieldErrorWrapped(t *testing.T) {
	t.Parallel()
	var p NodePatch
	err := json.Unmarshal([]byte(`{"totally_random":42}`), &p)
	if err == nil {
		t.Fatalf("unknown field: want err, got nil")
	}
	if !errors.Is(err, ErrNodePatchBannedField) {
		t.Fatalf("unknown field err = %v, want errors.Is ErrNodePatchBannedField", err)
	}
	if !strings.Contains(err.Error(), "totally_random") {
		t.Errorf("err = %v, want field name 'totally_random' in message", err)
	}
	// 同时确认 decoder 原始 `unknown field` 措辞还在 err chain 内（便于排查）。
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("err = %v, want underlying 'unknown field' phrase", err)
	}
}
