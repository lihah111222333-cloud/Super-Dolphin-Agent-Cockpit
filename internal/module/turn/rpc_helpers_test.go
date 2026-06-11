package turn

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func TestBuildPrepareInputSupportsExpandedFields(t *testing.T) {
	t.Parallel()

	items, inputSkills := buildTurnStartInputs([]turnInputItemParams{
		{Type: "text", Text: "typed text"},
		{Type: "skill", Name: "debug"},
		{Type: "mention", Path: "doc.md"},
	})
	input := buildPrepareInput(expandedPrepareInputSpec(items), prepareSkillSpec{
		Selected:     []string{"review", "debug"},
		SelectedRefs: []skillRefParams{{Name: "project-review", Scope: "project"}},
		Derived:      inputSkills,
	}, expandedPrepareInputSession())

	assertExpandedPrepareInputItems(t, input)
	assertExpandedPrepareInputContext(t, input)
	assertExpandedPrepareInputRuntimeFallbacks(t, input)
}

func TestTurnStartParamsAcceptsSelectedSkillRefsCamelCase(t *testing.T) {
	t.Parallel()

	var params turnStartParams
	input := []byte(`{
		"threadId":"thread-1",
		"selectedSkills":["docs"],
		"selectedSkillRefs":[{"key":"project::docs:/repo/.agent/skills/docs","name":"docs","scope":"project","path":"/repo/.agent/skills/docs"}],
		"manualSkillSelection":true
	}`)
	if err := json.Unmarshal(input, &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(params.SelectedSkillRefs) != 1 ||
		params.SelectedSkillRefs[0].Name != "docs" ||
		params.SelectedSkillRefs[0].Scope != "project" ||
		params.SelectedSkillRefs[0].Key == "" ||
		params.SelectedSkillRefs[0].Path != "/repo/.agent/skills/docs" {
		t.Fatalf("SelectedSkillRefs = %#v", params.SelectedSkillRefs)
	}
	if len(params.SelectedSkills) != 1 || params.SelectedSkills[0] != "docs" || !params.ManualSkillSelection {
		t.Fatalf("selected skill compatibility fields = %#v manual=%v", params.SelectedSkills, params.ManualSkillSelection)
	}
}

func TestTurnStartResultPromptKeyStaleSurfacedToWire(t *testing.T) {
	t.Parallel()

	stale := true
	payload, err := json.Marshal(turnStartResult{
		TurnID:              "turn-1",
		PromptKey:           "main/missing",
		PromptKeyStale:      &stale,
		PromptKeyStaleCamel: &stale,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	body := string(payload)
	if !strings.Contains(body, `"prompt_key_stale":true`) {
		t.Fatalf("turnStartResult JSON missing prompt_key_stale: %s", body)
	}
	if !strings.Contains(body, `"promptKeyStale":true`) {
		t.Fatalf("turnStartResult JSON missing promptKeyStale: %s", body)
	}
}

func expandedPrepareInputSession() *rpcHelperSession {
	return &rpcHelperSession{
		caps: dto.CapabilitySet{dto.CapMessageSend: true},
		runtimeConfig: map[string]any{
			"provider":                     "codex-runtime",
			"cwd":                          "/runtime/work",
			"model":                        "runtime-model",
			"gitRoot":                      "/runtime-repo",
			"language":                     "Chinese",
			"enabledTools":                 []string{"lsp_file", "spawn_agent"},
			"additionalWorkingDirectories": []string{"/repo/runtime-extra"},
			"mcpTools":                     []string{"mcp__orch__orchestration_send_message"},
			"mcpInstructions":              map[string]any{"orch": "Use orchestration runtime fallback."},
			"sessionFlags":                 map[string]any{"runtime_only": true},
		},
	}
}

func expandedPrepareInputSpec(items []InputItem) prepareInputSpec {
	return prepareInputSpec{
		Prompt:               "flat prompt",
		Images:               []string{"img-1"},
		Files:                []string{"file-1"},
		Inputs:               items,
		ManualSkillSelection: true,
		Provider:             "claude",
		CWD:                  "/tmp/work",
		Model:                "gpt-5",
		GitRoot:              "/override-repo",
		Language:             "Japanese",
		EnabledTools:         []string{"exec_command"},
		ThreadRuntimeConfig: map[string]any{
			"provider":                     "claude-thread",
			"gitRoot":                      "/thread-repo",
			"isWorktree":                   true,
			"language":                     "German",
			"additionalWorkingDirectories": []string{"/repo/thread-extra"},
			"mcpTools":                     []string{"mcp__lsp__lsp_grep"},
			"mcpInstructions":              map[string]any{"lsp": "Use the LSP thread fallback."},
			"sessionFlags":                 map[string]any{"verification_required": true},
		},
		Effort:       "high",
		OutputSchema: []byte(`{"type":"object"}`),
	}
}

func assertExpandedPrepareInputItems(t *testing.T, input PrepareInput) {
	t.Helper()
	if len(input.Inputs) != 2 {
		t.Fatalf("len(input.Inputs) = %d, want 2", len(input.Inputs))
	}
	if input.Inputs[0].Type != "text" || input.Inputs[0].Content != "typed text" {
		t.Fatalf("first input = %#v, want typed text input", input.Inputs[0])
	}
	if input.Inputs[1].Type != "mention" || input.Inputs[1].Path != "doc.md" {
		t.Fatalf("second input = %#v, want mention input", input.Inputs[1])
	}
	if got := skillNames(input.Skills); len(got) != 3 || got[0] != "project-review" || got[1] != "review" || got[2] != "debug" {
		t.Fatalf("skill names = %#v, want [project-review review debug]", got)
	}
}

func assertExpandedPrepareInputContext(t *testing.T, input PrepareInput) {
	t.Helper()
	assertExpandedPrepareInputOverrides(t, input)
	assertExpandedPrepareInputTools(t, input)
}

func assertExpandedPrepareInputOverrides(t *testing.T, input PrepareInput) {
	t.Helper()
	if !input.ManualSkillSelection || input.CWD != "/tmp/work" || string(input.OutputSchema) != `{"type":"object"}` {
		t.Fatalf("prepare input = %#v", input)
	}
	if input.Provider != "claude" || input.Model != "gpt-5" || input.GitRoot != "/override-repo" || input.Language != "Japanese" {
		t.Fatalf("prepare input context = %#v", input)
	}
	if !input.IsWorktree {
		t.Fatal("IsWorktree = false, want true from thread-state fallback")
	}
}

func assertExpandedPrepareInputTools(t *testing.T, input PrepareInput) {
	t.Helper()
	if got := input.EnabledTools; len(got) != 1 || got[0] != "exec_command" {
		t.Fatalf("EnabledTools = %#v, want request override", got)
	}
}

func assertExpandedPrepareInputRuntimeFallbacks(t *testing.T, input PrepareInput) {
	t.Helper()
	if got := input.AdditionalWorkingDirectories; len(got) != 1 || got[0] != "/repo/thread-extra" {
		t.Fatalf("AdditionalWorkingDirectories = %#v, want thread-state fallback", got)
	}
	if got := input.MCPSnapshot.Tools; !slices.Contains(got, "mcp__lsp__lsp_grep") {
		t.Fatalf("MCPSnapshot.Tools = %#v, want thread-state tool present", got)
	}
	if input.MCPSnapshot.Instructions["lsp"] != "Use the LSP thread fallback." {
		t.Fatalf("MCPSnapshot.Instructions = %#v", input.MCPSnapshot.Instructions)
	}
	if !input.SessionFlags["verification_required"] {
		t.Fatalf("SessionFlags = %#v, want thread-state fallback", input.SessionFlags)
	}
	if input.SessionFlags["runtime_only"] {
		t.Fatalf("SessionFlags = %#v, want thread-state to win over runtime", input.SessionFlags)
	}
}

func TestBuildPrepareInputFiltersSpawnAgentWhenPersistentManagedLaunchEnabled(t *testing.T) {
	t.Parallel()

	input := buildPrepareInput(prepareInputSpec{
		ThreadRuntimeConfig: map[string]any{
			"enabledTools": []string{"spawn_agent", "orchestration_launch_agent", "request_user_input"},
			"sessionFlags": map[string]any{"persistent_subagent_default": true},
		},
	}, prepareSkillSpec{}, nil)

	if got := input.EnabledTools; len(got) != 2 || got[0] != "orchestration_launch_agent" || got[1] != "request_user_input" {
		t.Fatalf("EnabledTools = %#v, want managed-only child-agent tools", input.EnabledTools)
	}
}

func TestResolveTurnSessionRejectsNilSession(t *testing.T) {
	t.Parallel()

	_, err := resolveTurnSession(context.Background(), rpcHelperResolver{})
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("resolveTurnSession() error = %T, want *jrpc2.Error", err)
	}
	if rpcErr.Message != "thread session is not available; start or resume the thread first" {
		t.Fatalf("rpcErr.Message = %q", rpcErr.Message)
	}
}

func TestApplyTurnStartConfigUsesApprovalPolicy(t *testing.T) {
	t.Parallel()

	session := &rpcHelperSession{}
	err := applyTurnStartConfig(context.Background(), session, turnStartParams{ApprovalPolicy: "on-request"})
	if err != nil {
		t.Fatalf("applyTurnStartConfig() error = %v", err)
	}
	if session.lastPatch.Approvals == nil || *session.lastPatch.Approvals != "on-request" {
		t.Fatalf("last approvals patch = %#v, want on-request", session.lastPatch.Approvals)
	}
}

func TestResolveTurnRPCCWDRejectsRequestMismatch(t *testing.T) {
	t.Parallel()

	threadRuntimeConfig := map[string]any{"cwd": "/thread/worktree"}

	_, err := resolveTurnRPCCWD("/active/project", threadRuntimeConfig)
	if err == nil {
		t.Fatal("resolveTurnRPCCWD() error = nil, want cwd mismatch error")
	}
	if !strings.Contains(err.Error(), "cwd mismatch") || !strings.Contains(err.Error(), "/thread/worktree") || !strings.Contains(err.Error(), "/active/project") {
		t.Fatalf("resolveTurnRPCCWD() error = %v, want mismatch with both cwd values", err)
	}
}

func TestResolveTurnRPCCWDAcceptsEquivalentWindowsSeparators(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("Windows drive separator equivalence is Windows-specific")
	}

	threadRuntimeConfig := map[string]any{"cwd": `D:\project\Super-Dolphin`}

	got, err := resolveTurnRPCCWD("D:/project/Super-Dolphin", threadRuntimeConfig)
	if err != nil {
		t.Fatalf("resolveTurnRPCCWD() error = %v", err)
	}
	if got != `D:\project\Super-Dolphin` {
		t.Fatalf("resolveTurnRPCCWD() = %q, want authoritative thread runtime cwd", got)
	}
}

func TestResolveTurnRPCCWDFillsAuthoritativeCWDWhenRequestOmitsIt(t *testing.T) {
	t.Parallel()

	threadRuntimeConfig := map[string]any{"cwd": "/thread/worktree"}

	got, err := resolveTurnRPCCWD("", threadRuntimeConfig)
	if err != nil {
		t.Fatalf("resolveTurnRPCCWD() error = %v", err)
	}
	if got != "/thread/worktree" {
		t.Fatalf("resolveTurnRPCCWD() = %q, want thread runtime cwd", got)
	}
}

func TestResolveTurnRPCCWDRejectsMissingAuthoritativeCWD(t *testing.T) {
	t.Parallel()

	_, err := resolveTurnRPCCWD("/active/project", nil)
	if err == nil {
		t.Fatal("resolveTurnRPCCWD() error = nil, want missing authoritative cwd error")
	}
	if !strings.Contains(err.Error(), "turn cwd missing") {
		t.Fatalf("resolveTurnRPCCWD() error = %v, want missing authoritative cwd error", err)
	}
}

func TestResolveTurnRPCCWDRejectsMissingPersistedCWD(t *testing.T) {
	t.Parallel()

	_, err := resolveTurnRPCCWD("", nil)
	if err == nil {
		t.Fatal("resolveTurnRPCCWD() error = nil, want missing persisted cwd error")
	}
	if !strings.Contains(err.Error(), "turn cwd missing") {
		t.Fatalf("resolveTurnRPCCWD() error = %v, want missing persisted cwd error", err)
	}
}

func TestResolveTurnRPCCWDRejectsMalformedRuntimeCWD(t *testing.T) {
	t.Parallel()

	_, err := resolveTurnRPCCWD("/active/project", map[string]any{"cwd": 123})
	if err == nil || !strings.Contains(err.Error(), `thread runtime config "cwd" must be a string`) {
		t.Fatalf("resolveTurnRPCCWD() error = %v, want malformed thread runtime cwd", err)
	}
}

type rpcHelperResolver struct {
	session contract.Session
	err     error
}

func (r rpcHelperResolver) ResolveSession(context.Context, string) (contract.Session, error) {
	return r.session, r.err
}

type rpcHelperSession struct {
	contract.Session
	caps          dto.CapabilitySet
	runtimeConfig map[string]any
	lastPatch     dto.ThreadConfigPatch
}

func (s rpcHelperSession) ThreadID() string { return "thread-1" }

func (s rpcHelperSession) RolloutPath() string { return "" }

func (s rpcHelperSession) Capabilities() dto.CapabilitySet { return s.caps }

func (s rpcHelperSession) RuntimeConfigSnapshot() map[string]any { return s.runtimeConfig }

func (s *rpcHelperSession) Configure(_ context.Context, patch dto.ThreadConfigPatch) error {
	s.lastPatch = patch
	return nil
}
