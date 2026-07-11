package thread

// name_policy_guard_test.go  — architectural guard that locks the agent naming
// policy:
//
//   Agent name MUST only come from:
//     1. An explicit `name` field in StartRequest (set by main agent or frontend).
//     2. The frontend calling `thread/name/set` RPC to rename.
//
//   At the PARSING / NORMALIZATION / ASSEMBLY layers the `prompt` field
//   MUST NOT leak into the `Name` field.  The service layer must also not
//   invent a default persisted name like "新对话".
//
// If you are reading this because a test broke: the layer-isolation policy is
// intentional. DO NOT reintroduce prompt/default-name derivation.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// ---------------------------------------------------------------------------
// §1  RPC parsing layer: `prompt` must NOT alias `name`
// ---------------------------------------------------------------------------

func TestNamePolicy_PromptFieldNeverBecomesName(t *testing.T) {
	t.Parallel()

	var params startParams
	if err := json.Unmarshal([]byte(`{"prompt":"hello world"}`), &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.Name != "" {
		t.Fatalf("POLICY VIOLATION: prompt leaked into Name: got %q, want empty", params.Name)
	}
	if params.Prompt != "hello world" {
		t.Fatalf("Prompt field should be preserved, got %q", params.Prompt)
	}
}

func TestNamePolicy_NameAndPromptAreIndependent(t *testing.T) {
	t.Parallel()

	var params startParams
	if err := json.Unmarshal([]byte(`{"name":"my agent","prompt":"do something"}`), &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.Name != "my agent" {
		t.Fatalf("Name = %q, want 'my agent'", params.Name)
	}
	if params.Prompt != "do something" {
		t.Fatalf("Prompt = %q, want 'do something'", params.Prompt)
	}
}

func TestNamePolicy_ExplicitNameOnly(t *testing.T) {
	t.Parallel()

	var params startParams
	if err := json.Unmarshal([]byte(`{"name":"explicit name"}`), &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.Name != "explicit name" {
		t.Fatalf("explicit Name = %q, want 'explicit name'", params.Name)
	}
}

func TestNamePolicy_NoNameNoPromptStaysEmpty(t *testing.T) {
	t.Parallel()

	var params startParams
	if err := json.Unmarshal([]byte(`{"cwd":"/tmp"}`), &params); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if params.Name != "" {
		t.Fatalf("POLICY VIOLATION: Name should be empty when not set, got %q", params.Name)
	}
}

// ---------------------------------------------------------------------------
// §2  normalizeStartRequest: prompt must NOT fall back to name
// ---------------------------------------------------------------------------

func TestNamePolicy_NormalizeDoesNotDeriveNameFromPrompt(t *testing.T) {
	t.Parallel()

	req, _, err := normalizeStartRequest(StartRequest{
		Provider: "codex",
		CWD:      wantStartCWD(t),
		Prompt:   "  user message  ",
	})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Name != "" {
		t.Fatalf("POLICY VIOLATION: normalizeStartRequest derived Name from Prompt: got %q, want empty", req.Name)
	}
}

func TestNamePolicy_NormalizePreservesExplicitName(t *testing.T) {
	t.Parallel()

	req, _, err := normalizeStartRequest(StartRequest{
		Provider: "codex",
		CWD:      wantStartCWD(t),
		Name:     "  my agent  ",
		Prompt:   "some prompt",
	})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if req.Name != "my agent" {
		t.Fatalf("normalizeStartRequest().Name = %q, want 'my agent'", req.Name)
	}
}

func TestNamePolicy_NormalizeTruncatesLongName(t *testing.T) {
	t.Parallel()

	longName := strings.Repeat("あ", 200)
	req, _, err := normalizeStartRequest(StartRequest{
		Provider: "codex",
		CWD:      wantStartCWD(t),
		Name:     longName,
	})
	if err != nil {
		t.Fatalf("normalizeStartRequest() error = %v", err)
	}
	if len([]rune(req.Name)) > startDisplayNameMaxRunes {
		t.Fatalf("Name should be truncated to %d runes, got %d", startDisplayNameMaxRunes, len([]rune(req.Name)))
	}
}

// ---------------------------------------------------------------------------
// §3  Service.Start: end-to-end guard on launch request name
// ---------------------------------------------------------------------------

func TestNamePolicy_StartWithoutNameStaysUnnamed(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, _ dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f604"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-noname",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Prompt:            "hello world",
		PromptAssemblyRef: promptAssemblyForTest("test system prompt"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "" {
		t.Fatalf("launch name = %q, want empty", orch.launchReq.Name)
	}
	if threads.upsert.Name != "" || threads.upsert.Prompt != "" {
		t.Fatalf("persisted name/prompt = %q/%q, want empty", threads.upsert.Name, threads.upsert.Prompt)
	}
}

func TestNamePolicy_StartWithExplicitNamePropagatesToLaunch(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, _ dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f605"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-named",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		Name:              "My Custom Agent",
		Prompt:            "do something",
		PromptAssemblyRef: promptAssemblyForTest("test system prompt"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "My Custom Agent" {
		t.Fatalf("launch name = %q, want 'My Custom Agent'", orch.launchReq.Name)
	}
}

// ---------------------------------------------------------------------------
// §4  test start assembly helper: DisplayName must not use Prompt
// ---------------------------------------------------------------------------

func TestNamePolicy_BuildStartAssemblyIgnoresPrompt(t *testing.T) {
	t.Parallel()

	assembly := buildStartAssemblyForTest(StartRequest{
		Name:   "",
		Prompt: "should not appear",
	})
	if assembly.DisplayName != "" {
		t.Fatalf("POLICY VIOLATION: test start assembly DisplayName = %q, want empty", assembly.DisplayName)
	}
}

func TestNamePolicy_BuildStartAssemblyUsesExplicitName(t *testing.T) {
	t.Parallel()

	assembly := buildStartAssemblyForTest(StartRequest{
		Name:   "agent name",
		Prompt: "irrelevant",
	})
	if assembly.DisplayName != "agent name" {
		t.Fatalf("test start assembly DisplayName = %q, want 'agent name'", assembly.DisplayName)
	}
}

// ---------------------------------------------------------------------------
// §5  RPC wire format: JSON round-trip guard
// ---------------------------------------------------------------------------

func TestNamePolicy_JSONRoundTrip_PromptNeverSetsName(t *testing.T) {
	t.Parallel()

	payloads := []string{
		`{"provider":"codex","prompt":"嗨"}`,
		`{"provider":"codex","prompt":"hello","cwd":"/tmp"}`,
		`{"provider":"codex","prompt":"multi word prompt here"}`,
		`{"provider":"codex","prompt":"{ \"agentId\": \"...\", \"uuid\": \"...\" }"}`,
	}

	for _, payload := range payloads {
		var params startParams
		if err := json.Unmarshal([]byte(payload), &params); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", payload, err)
		}
		if params.Name != "" {
			t.Fatalf("POLICY VIOLATION: payload %s → Name = %q, want empty", payload, params.Name)
		}
	}
}
