package nodeexec

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// F4.2: NodePatch strict UnmarshalJSON + AssignedTo 字段位单测。
//
// 设计意图：update_node patch 顶层字段白名单 = {title, assigned_to, depends_on, config}。
// 任何其他键（含 status / node_key / node_type / agent_key）都要 fail-fast 拒绝，
// 防 AI 设计师或表单误传入「禁改字段」蒙混过关。

// TestNodePatch_AssignedTo_ThreeStates 验证 AssignedTo 与 DependsOn 同款三态：
//   - nil = 不改
//   - 指向 "" = 清空 assigned_to（拿走 agent 路由）
//   - 指向 "writer" = 改成此值
func TestNodePatch_AssignedTo_ThreeStates(t *testing.T) {
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
		t.Run(c.name, func(t *testing.T) {
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
	var p NodePatch
	if err := json.Unmarshal([]byte(`{}`), &p); err != nil {
		t.Fatalf("empty patch: err = %v, want nil", err)
	}
}
