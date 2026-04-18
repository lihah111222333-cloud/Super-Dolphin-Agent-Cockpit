package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSkillRef_LegacyUnmarshalStillWorks 覆盖 P20 §6.1 向后兼容：
// 旧 client 发送的 {name, prompt} payload 必须能被新 server 反序列化，
// Mode 字段在零值下等价于 Full。
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
	if ref.Mode != SkillModeUnspecified {
		t.Fatalf("legacy payload must yield Unspecified, got %q", ref.Mode)
	}
	if ref.Mode.Effective() != SkillModeFull {
		t.Fatalf("Effective() of Unspecified should be Full")
	}
	if ref.Summary != "" || ref.Source != "" || ref.Version != "" {
		t.Fatalf("new fields should be zero: %+v", ref)
	}
}

// TestSkillRef_NewPayloadRoundTrip 新 payload 的 marshal/unmarshal round-trip。
func TestSkillRef_NewPayloadRoundTrip(t *testing.T) {
	original := SkillRef{
		Name:    "lint-go",
		Version: "v1.2",
		Mode:    SkillModeSummary,
		Summary: "golangci-lint runner with preset",
		Source:  SkillSourceTrigger,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// 确认关键字段都落到 wire 上
	for _, substr := range []string{`"name":"lint-go"`, `"version":"v1.2"`, `"mode":"summary"`, `"summary":"golangci`, `"source":"trigger"`} {
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
	// 不应含新字段任何 key
	for _, key := range []string{"version", "mode", "summary", "source", "prompt"} {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Fatalf("unexpected key %q in minimal payload: %s", key, data)
		}
	}
}

// TestSkillRef_OldServerReadsNewPayload 模拟“旧 server 接收新 client 发出的 payload”：
// 旧 server 只认识 name/prompt，对 mode/summary/source/version 丢弃即可。Prompt 仍然
// 可用，行为退化为 Full 注入。（我们用一个只有两字段的目标结构体模拟旧 server。）
func TestSkillRef_OldServerReadsNewPayload(t *testing.T) {
	newPayload := SkillRef{
		Name:    "foo",
		Version: "v1",
		Mode:    SkillModeFull,
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

// TestSkillMode_Valid 覆盖枚举 Valid() 方法的合法/非法分支。
func TestSkillMode_Valid(t *testing.T) {
	valid := []SkillMode{SkillModeUnspecified, SkillModeFull, SkillModeSummary, SkillModeNone}
	for _, m := range valid {
		if !m.Valid() {
			t.Fatalf("%q should be Valid", m)
		}
	}
	invalid := []SkillMode{"banana", "FULL", "body", " full "}
	for _, m := range invalid {
		if m.Valid() {
			t.Fatalf("%q should NOT be Valid", m)
		}
	}
}

// TestSkillMode_Effective 空值必须规范化为 Full，合法值保持不变。
func TestSkillMode_Effective(t *testing.T) {
	if SkillModeUnspecified.Effective() != SkillModeFull {
		t.Fatalf("Unspecified → Full")
	}
	if SkillModeSummary.Effective() != SkillModeSummary {
		t.Fatalf("Summary should stay")
	}
	if SkillModeNone.Effective() != SkillModeNone {
		t.Fatalf("None should stay")
	}
}

// TestSkillMode_EffectiveUnknownFallsBackToFull 防御未知 mode 值（wire 伪造 / 未来旧 server
// 遇到新枚举时）的失败展开行为：兑底返回 Full，不能静默跳过。
// 这条单测同时限定“Effective 不返回任意未知值”，重构时保护该不变量。
func TestSkillMode_EffectiveUnknownFallsBackToFull(t *testing.T) {
	unknowns := []SkillMode{"banana", "FULL", "body", " full ", "partial", "skip"}
	for _, m := range unknowns {
		if got := m.Effective(); got != SkillModeFull {
			t.Fatalf("Effective(%q) = %q, want Full (fail-open fallback)", m, got)
		}
	}
}

// TestSkillRef_UnmarshalKeepsUnknownMode 确认反序列化本身保留原始未知 mode
// （供诊断 / 对端调试），规范化必须由 Effective() 显式完成。防止未来
// “偏到 UnmarshalJSON 里 rewrite” 与“保留原值交给观测层” 两种路径混淆。
func TestSkillRef_UnmarshalKeepsUnknownMode(t *testing.T) {
	data := []byte(`{"name":"foo","mode":"banana"}`)
	var ref SkillRef
	if err := json.Unmarshal(data, &ref); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(ref.Mode) != "banana" {
		t.Fatalf("Mode should keep raw wire value for observability: got %q", ref.Mode)
	}
	if ref.Mode.Valid() {
		t.Fatalf("unknown mode should NOT be Valid")
	}
	if ref.Mode.Effective() != SkillModeFull {
		t.Fatalf("Effective should fail-open to Full")
	}
}

// TestSkillRef_TurnRequestEmbedding 确认 SkillRef 嵌入 TurnRequest 序列化正常。
func TestSkillRef_TurnRequestEmbedding(t *testing.T) {
	req := TurnRequest{
		ThreadID: "t1",
		Skills: []SkillRef{
			{Name: "a", Mode: SkillModeSummary, Summary: "hi"},
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
	if back.Skills[0].Mode != SkillModeSummary || back.Skills[1].Mode != SkillModeUnspecified {
		t.Fatalf("mode not preserved: %+v", back.Skills)
	}
}
