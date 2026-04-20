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

// TestSkillMode_EffectiveUnknownFallsBackToNone （P20.1 §3.5 修订）
// 非空非法值 → None（保守降级），不再“失败展开 Full”。空值仍→ Full（legacy 兼容）。
func TestSkillMode_EffectiveUnknownFallsBackToNone(t *testing.T) {
	unknowns := []SkillMode{"banana", "FULL", "body", " full ", "partial", "skip"}
	for _, m := range unknowns {
		if got := m.Effective(); got != SkillModeNone {
			t.Fatalf("Effective(%q) = %q, want None (P20.1 conservative downgrade)", m, got)
		}
	}
	// 空值仍是 Full
	if got := SkillModeUnspecified.Effective(); got != SkillModeFull {
		t.Fatalf("Unspecified → Full (legacy compat), got %q", got)
	}
}

// TestSkillRef_UnmarshalKeepsUnknownMode 确认反序列化本身保留原始未知 mode
// （供诊断 / 对端调试），规范化由 Effective() 显式完成。
//
// P20.1 后 Effective("banana") → None（不注入），限定的稳定行为——防止未来有人
// 在 UnmarshalJSON 里悄悄 rewrite（那样会丢失 raw mode 的观测性）。
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
	if ref.Mode.Effective() != SkillModeNone {
		t.Fatalf("P20.1: Effective should conservatively downgrade to None for unknown mode")
	}
}

// TestSkillSource_Valid 对称 TestSkillMode_Valid——确保未来延伸 SkillSource 枚举时
// 什么值是合法的由 Valid() 单点控制，而不是散在各个 switch 里。
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

// TestSkillRef_SteerRequestEmbedding 对称 TurnRequest——确保 SteerRequest 路径上的 skills
// 反序列化行为在 Phase 2 扩展后仍保留字段完整性。
func TestSkillRef_SteerRequestEmbedding(t *testing.T) {
	req := SteerRequest{
		ThreadID:             "t1",
		ExpectedTurnID:       "t1-turn-2",
		ManualSkillSelection: true,
		Skills: []SkillRef{
			{Name: "a", Mode: SkillModeSummary, Summary: "hi", Source: SkillSourceManual},
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
	if back.Skills[0].Source != SkillSourceManual || back.Skills[0].Mode != SkillModeSummary {
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

func TestSkillPromptCarrierRoundTrip(t *testing.T) {
	carrier := "skills:\n- planner\n\n[skill:planner::full@v1]\nfull body\n[/skill:planner::full@v1]"

	turn := TurnRequest{ThreadID: "t1", SkillPrompt: carrier}
	turnData, err := json.Marshal(turn)
	if err != nil {
		t.Fatalf("Marshal TurnRequest: %v", err)
	}
	var turnWire map[string]any
	if err := json.Unmarshal(turnData, &turnWire); err != nil {
		t.Fatalf("Unmarshal TurnRequest wire: %v", err)
	}
	if turnWire["skillPrompt"] != carrier {
		t.Fatalf("TurnRequest skillPrompt wire = %#v, want %q", turnWire["skillPrompt"], carrier)
	}
	var turnBack TurnRequest
	if err := json.Unmarshal(turnData, &turnBack); err != nil {
		t.Fatalf("Unmarshal TurnRequest round-trip: %v", err)
	}
	if turnBack.SkillPrompt != carrier {
		t.Fatalf("TurnRequest SkillPrompt = %q, want %q", turnBack.SkillPrompt, carrier)
	}

	steer := SteerRequest{ThreadID: "t1", SkillPrompt: carrier}
	steerData, err := json.Marshal(steer)
	if err != nil {
		t.Fatalf("Marshal SteerRequest: %v", err)
	}
	var steerWire map[string]any
	if err := json.Unmarshal(steerData, &steerWire); err != nil {
		t.Fatalf("Unmarshal SteerRequest wire: %v", err)
	}
	if steerWire["skillPrompt"] != carrier {
		t.Fatalf("SteerRequest skillPrompt wire = %#v, want %q", steerWire["skillPrompt"], carrier)
	}
	var steerBack SteerRequest
	if err := json.Unmarshal(steerData, &steerBack); err != nil {
		t.Fatalf("Unmarshal SteerRequest round-trip: %v", err)
	}
	if steerBack.SkillPrompt != carrier {
		t.Fatalf("SteerRequest SkillPrompt = %q, want %q", steerBack.SkillPrompt, carrier)
	}

	minimal, err := json.Marshal(TurnRequest{ThreadID: "t1"})
	if err != nil {
		t.Fatalf("Marshal minimal TurnRequest: %v", err)
	}
	if strings.Contains(string(minimal), "skillPrompt") {
		t.Fatalf("empty SkillPrompt should be omitted, got %s", minimal)
	}
}
