package thread

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// TestCloneProviderSkillRefsStripsLegacyPrompt 验证 thread/start 只跨边界传递 skill 元数据。
func TestCloneProviderSkillRefsStripsLegacyPrompt(t *testing.T) {
	input := []dto.SkillRef{{
		Key:     "project:review",
		Name:    "review",
		Scope:   "project",
		Path:    "/repo/.agents/skills/review",
		Version: "v1",
		Prompt:  "secret legacy skill body",
		Summary: "review summary",
		Source:  dto.SkillSourceManual,
	}}

	got := cloneProviderSkillRefs(input)
	if len(got) != 1 {
		t.Fatalf("cloneProviderSkillRefs() len = %d, want 1", len(got))
	}
	if got[0].Prompt != "" {
		t.Fatalf("cloned skill prompt = %q, want body stripped", got[0].Prompt)
	}
	if got[0].Name != input[0].Name || got[0].Key != input[0].Key || got[0].Summary != input[0].Summary || got[0].Source != input[0].Source {
		t.Fatalf("cloned metadata = %#v, want metadata preserved from %#v", got[0], input[0])
	}
	if input[0].Prompt != "secret legacy skill body" {
		t.Fatalf("source skill prompt mutated to %q", input[0].Prompt)
	}

	got[0].Name = "mutated"
	if input[0].Name == got[0].Name {
		t.Fatal("clone aliases source skill ref")
	}
}

// TestStartRequestFingerprintStripsLegacyPrompt 验证幂等快照也不会保存旧 skill 正文。
func TestStartRequestFingerprintStripsLegacyPrompt(t *testing.T) {
	input := StartRequest{
		LaunchSkillRefs: []dto.SkillRef{{Name: "review", Prompt: "secret legacy body"}},
	}

	got := startRequestFingerprint(input)
	if len(got.LaunchSkillRefs) != 1 || got.LaunchSkillRefs[0].Prompt != "" {
		t.Fatalf("fingerprint launch refs = %#v, want metadata-only ref", got.LaunchSkillRefs)
	}
	if input.LaunchSkillRefs[0].Prompt != "secret legacy body" {
		t.Fatalf("fingerprint mutated source prompt = %q", input.LaunchSkillRefs[0].Prompt)
	}
}

// TestStartRequestFromSessionStripsLegacyPromptAtPortBoundary 验证 session port
// 直连调用也不会把旧 skill 正文带入 thread 内部请求。
func TestStartRequestFromSessionStripsLegacyPromptAtPortBoundary(t *testing.T) {
	input := contract.SessionStartRequest{
		Provider:        "codex",
		LaunchSkillRefs: []dto.SkillRef{{Name: "review", Prompt: "legacy body"}},
	}

	got := startRequestFromSession(input)
	if len(got.LaunchSkillRefs) != 1 || got.LaunchSkillRefs[0].Prompt != "" {
		t.Fatalf("mapped launch refs = %#v, want metadata-only ref", got.LaunchSkillRefs)
	}
	if input.LaunchSkillRefs[0].Prompt != "legacy body" {
		t.Fatalf("session port input mutated to %q", input.LaunchSkillRefs[0].Prompt)
	}
}
