package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSkillRef_LegacyUnmarshalStillWorks 旧 client 发送的 {name, prompt} payload
// 必须能被新 server 反序列化。Mode 字段已删除，Prompt 仍是有效字段。
func TestSkillRef_LegacyUnmarshalStillWorks(t *testing.T) {
	legacy := `{"name":"go-testing","prompt":"# Skill body\nrun go test"}`
	var ref SkillRef
	if err := json.Unmarshal([]byte(legacy), &ref); err != nil {
		t.Fatalf("Unmarshal legacy: %v", err)
	}
	if ref.Name != "go-testing" {
		t.Fatalf("Name = %q", ref.Name)
	}
	if !strings.Contains(ref.Prompt, "run go test") {
		t.Fatalf("Prompt = %q", ref.Prompt)
	}
	if ref.Summary != "" || ref.Source != "" || ref.Version != "" {
		t.Fatalf("new fields should be zero: %+v", ref)
	}
}

// TestSkillRef_DropsRetiredModeFieldFromWire 旧 client 仍可能发送 `mode` 字段。
// SkillRef 不再持有 Mode field，json unmarshaling 默认忽略未知字段，结果 ref 应当
// 无任何与 mode 相关的状态；marshal 出来的 JSON 也不应再包含 `mode` key。
// 这条测试守护"D commit 之后再有人加回 Mode 字段"的回归。
func TestSkillRef_DropsRetiredModeFieldFromWire(t *testing.T) {
	wire := []byte(`{"name":"foo","mode":"summary","prompt":"body"}`)
	var ref SkillRef
	if err := json.Unmarshal(wire, &ref); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ref.Name != "foo" || ref.Prompt != "body" {
		t.Fatalf("expected name+prompt preserved, got %+v", ref)
	}
	out, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(out), `"mode"`) {
		t.Fatalf("retired mode field should not reappear on marshal: %s", out)
	}
}

// TestSkillRef_NewPayloadRoundTrip 新 payload 的 marshal/unmarshal round-trip。
func TestSkillRef_NewPayloadRoundTrip(t *testing.T) {
	original := SkillRef{
		Name:    "lint-go",
		Version: "v1.2",
		Summary: "golangci-lint runner with preset",
		Source:  SkillSourceTrigger,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, substr := range []string{`"name":"lint-go"`, `"version":"v1.2"`, `"summary":"golangci`, `"source":"trigger"`} {
		if !strings.Contains(string(data), substr) {
			t.Fatalf("marshaled JSON missing %q: %s", substr, data)
		}
	}
	var back SkillRef
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal round-trip: %v", err)
	}
	if back != original {
		t.Fatalf("round-trip mismatch:\nwant %+v\ngot  %+v", original, back)
	}
}

// TestSkillRef_OmitemptyZeroValues 验证零值不落 wire，节省 token + 兼容 legacy。
func TestSkillRef_OmitemptyZeroValues(t *testing.T) {
	minimal := SkillRef{Name: "foo"}
	data, err := json.Marshal(minimal)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"name":"foo"}`
	if string(data) != want {
		t.Fatalf("minimal payload = %s, want %s", data, want)
	}
	for _, key := range []string{"version", "mode", "summary", "source", "prompt"} {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Fatalf("unexpected key %q in minimal payload: %s", key, data)
		}
	}
}

// TestSkillRef_OldServerReadsNewPayload 模拟"旧 server 接收新 client 发出的 payload"：
// 旧 server 只认识 name/prompt，丢弃其它字段。
func TestSkillRef_OldServerReadsNewPayload(t *testing.T) {
	newPayload := SkillRef{
		Name:    "foo",
		Version: "v1",
		Prompt:  "FULL BODY HERE",
		Summary: "short desc",
		Source:  SkillSourceForce,
	}
	data, err := json.Marshal(newPayload)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	type legacyServerShape struct {
		Name   string `json:"name"`
		Prompt string `json:"prompt,omitempty"`
	}
	var legacy legacyServerShape
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatalf("legacy Unmarshal: %v", err)
	}
	if legacy.Name != "foo" || legacy.Prompt != "FULL BODY HERE" {
		t.Fatalf("legacy shape did not recover name+prompt: %+v", legacy)
	}
}

// TestSkillSource_Valid 单点控制 SkillSource 合法值；未来扩展枚举走这里。
func TestSkillSource_Valid(t *testing.T) {
	valid := []SkillSource{SkillSourceUnspecified, SkillSourceManual, SkillSourceForce, SkillSourceTrigger, SkillSourceExpand, SkillSourceNative}
	for _, s := range valid {
		if !s.Valid() {
			t.Fatalf("%q should be Valid", s)
		}
	}
	invalid := []SkillSource{"MANUAL", "auto", "robot", " force ", "system"}
	for _, s := range invalid {
		if s.Valid() {
			t.Fatalf("%q should NOT be Valid", s)
		}
	}
}

// TestSkillRef_SteerRequestEmbedding 确保 SteerRequest 路径上的 skills round-trip 完整。
func TestSkillRef_SteerRequestEmbedding(t *testing.T) {
	req := SteerRequest{
		ThreadID:             "t1",
		ExpectedTurnID:       "t1-turn-2",
		ManualSkillSelection: true,
		Skills: []SkillRef{
			{Name: "a", Summary: "hi", Source: SkillSourceManual},
			{Name: "b", Version: "v2", Prompt: "body", Source: SkillSourceForce},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back SteerRequest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(back.Skills) != 2 {
		t.Fatalf("skills len = %d", len(back.Skills))
	}
	if back.Skills[0].Source != SkillSourceManual || back.Skills[0].Summary != "hi" {
		t.Fatalf("skill[0] lost: %+v", back.Skills[0])
	}
	if back.Skills[1].Version != "v2" || back.Skills[1].Source != SkillSourceForce {
		t.Fatalf("skill[1] lost: %+v", back.Skills[1])
	}
	if !back.ManualSkillSelection {
		t.Fatalf("ManualSkillSelection lost")
	}
}

// TestSkillRef_TurnRequestEmbedding 确认 SkillRef 嵌入 TurnRequest 序列化正常。
func TestSkillRef_TurnRequestEmbedding(t *testing.T) {
	req := TurnRequest{
		ThreadID: "t1",
		Skills: []SkillRef{
			{Name: "a", Summary: "hi"},
			{Name: "b", Prompt: "body"},
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal TurnRequest: %v", err)
	}
	if !strings.Contains(string(data), `"skills":[`) {
		t.Fatalf("skills array missing: %s", data)
	}
	var back TurnRequest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal TurnRequest: %v", err)
	}
	if len(back.Skills) != 2 {
		t.Fatalf("skills len = %d", len(back.Skills))
	}
	if back.Skills[0].Summary != "hi" || back.Skills[1].Prompt != "body" {
		t.Fatalf("payload not preserved: %+v", back.Skills)
	}
}
