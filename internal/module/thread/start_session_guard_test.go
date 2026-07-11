package thread

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestResolveStartConfigAppliesDefaultsAndDangerPolicy(t *testing.T) {
	t.Parallel()

	cwd := wantStartCWD(t)
	req, err := resolveStartConfig(StartRequest{
		Provider: " Codex ",
		CWD:      cwd,
		Sandbox:  json.RawMessage(`{"type":"danger-full-access"}`),
	})
	if err != nil {
		t.Fatalf("resolveStartConfig() error = %v", err)
	}
	if req.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", req.Provider)
	}
	if req.CWD != cwd {
		t.Fatalf("cwd = %q, want %q", req.CWD, cwd)
	}
	if req.ApprovalPolicy != "never" {
		t.Fatalf("approvalPolicy = %q, want never", req.ApprovalPolicy)
	}
}

func TestResolveStartConfigRejectsMissingCWD(t *testing.T) {
	t.Parallel()

	_, err := resolveStartConfig(StartRequest{Provider: "codex"})
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("resolveStartConfig() error = %v, want cwd required", err)
	}
}

func TestResolveStartConfigRejectsInvalidProvider(t *testing.T) {
	t.Parallel()

	_, err := resolveStartConfig(StartRequest{Provider: "other"})
	if err == nil || !strings.Contains(err.Error(), "invalid provider") {
		t.Fatalf("resolveStartConfig() error = %v, want invalid provider", err)
	}
}

func TestResolveStartConfigRejectsDotCWD(t *testing.T) {
	t.Parallel()

	_, err := resolveStartConfig(StartRequest{Provider: "codex", CWD: "."})
	if err == nil || !strings.Contains(err.Error(), "cwd must be explicit") {
		t.Fatalf("resolveStartConfig() error = %v, want explicit cwd error", err)
	}
}

func TestResolveStartConfigRejectsInvalidApprovalPolicy(t *testing.T) {
	t.Parallel()

	_, err := resolveStartConfig(StartRequest{Provider: "codex", CWD: wantStartCWD(t), ApprovalPolicy: "later"})
	if err == nil || !strings.Contains(err.Error(), "invalid approval policy") {
		t.Fatalf("resolveStartConfig() error = %v, want invalid approval policy", err)
	}
}

func TestResolveStartConfigRejectsMalformedSandbox(t *testing.T) {
	t.Parallel()

	_, err := resolveStartConfig(StartRequest{Provider: "codex", CWD: wantStartCWD(t), Sandbox: json.RawMessage("{")})
	if err == nil || !strings.Contains(err.Error(), "invalid sandbox") {
		t.Fatalf("resolveStartConfig() error = %v, want invalid sandbox", err)
	}
}

func TestResolveStartConfigRejectsMalformedSandboxShape(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		sandbox json.RawMessage
	}{
		{name: "object missing type", sandbox: json.RawMessage(`{}`)},
		{name: "object non-string type", sandbox: json.RawMessage(`{"type":123}`)},
		{name: "array", sandbox: json.RawMessage(`[]`)},
		{name: "number", sandbox: json.RawMessage(`123`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveStartConfig(StartRequest{Provider: "codex", CWD: wantStartCWD(t), Sandbox: tc.sandbox})
			if err == nil || !strings.Contains(err.Error(), "invalid sandbox") {
				t.Fatalf("resolveStartConfig() error = %v, want invalid sandbox shape", err)
			}
		})
	}
}

func TestResolveStartConfigRejectsUnknownSandboxEvenWithExplicitApproval(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		sandbox json.RawMessage
	}{
		{name: "object mode alias", sandbox: json.RawMessage(`{"mode":"danger-full-access"}`)},
		{name: "unknown object type", sandbox: json.RawMessage(`{"type":"network-open"}`)},
		{name: "unknown string type", sandbox: json.RawMessage(`"network-open"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveStartConfig(StartRequest{
				Provider:       "codex",
				CWD:            wantStartCWD(t),
				ApprovalPolicy: "never",
				Sandbox:        tc.sandbox,
			})
			if err == nil || !strings.Contains(err.Error(), "invalid sandbox") {
				t.Fatalf("resolveStartConfig() error = %v, want invalid sandbox", err)
			}
		})
	}
}

func TestResolveStartConfigAcceptsLegacyApprovalPolicies(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		policy string
	}{
		{name: "on-request", policy: "on-request"},
		{name: "on-failure", policy: "on-failure"},
		{name: "untrusted", policy: "untrusted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := resolveStartConfig(StartRequest{Provider: "codex", CWD: wantStartCWD(t), ApprovalPolicy: tc.policy})
			if err != nil {
				t.Fatalf("resolveStartConfig() error = %v", err)
			}
			if req.ApprovalPolicy != tc.policy {
				t.Fatalf("approvalPolicy = %q, want %q", req.ApprovalPolicy, tc.policy)
			}
		})
	}
}

func TestServiceStartUsesResolvedStartConfig(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{}
	rolloutPath := writeExistingProviderHistoryFile(t)
	starter := startConfigAssertingStarter(t, sessions, rolloutPath)
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-start",
		Provider:          " Codex ",
		CWD:               wantStartCWD(t),
		BaseInstructions:  "  launch me  ",
		Sandbox:           json.RawMessage(`{"type":"danger-full-access"}`),
		PromptAssemblyRef: promptAssemblyForTest("launch me"),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assertResolvedStartResult(t, result)
	assertResolvedStartLaunch(t, orch)
	assertResolvedStartPersistence(t, threads, bindings)
}

func startConfigAssertingStarter(t *testing.T, sessions *stubSessionProvider, rolloutPath string) *startOnlySessionStarter {
	t.Helper()
	return &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			assertResolvedStartRequest(t, req)
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b3", rolloutPath: rolloutPath}
			sessions.session = session
			return session, nil
		},
	}
}

func assertResolvedStartRequest(t *testing.T, req dto.StartSessionRequest) {
	t.Helper()
	if req.Provider != "codex" {
		t.Fatalf("provider = %q, want codex", req.Provider)
	}
	if req.CWD != wantStartCWD(t) {
		t.Fatalf("cwd = %q, want %q", req.CWD, wantStartCWD(t))
	}
	if req.Instructions != "launch me" {
		t.Fatalf("instructions = %q, want launch me", req.Instructions)
	}
	if got := req.Config["approvalPolicy"]; got != "never" {
		t.Fatalf("approvalPolicy = %#v, want never", got)
	}
	sandbox, ok := req.Config["sandbox"].(map[string]any)
	if !ok || sandbox["type"] != "danger-full-access" {
		t.Fatalf("sandbox = %#v, want danger-full-access", req.Config["sandbox"])
	}
}

func assertResolvedStartResult(t *testing.T, result StartResult) {
	t.Helper()
	if result.ThreadID != "agent-start" || result.SessionID != "019d5f6b-fb3c-7760-9d6f-54005553f5b3" || result.AgentID != "agent-start" {
		t.Fatalf("result = %#v", result)
	}
	if result.Provider != "codex" || result.CWD != wantStartCWD(t) || result.ApprovalPolicy != "never" {
		t.Fatalf("effective start result = %#v", result)
	}
}

func assertResolvedStartLaunch(t *testing.T, orch *stubThreadOrchestration) {
	t.Helper()
	if orch.launchReq.Cwd != wantStartCWD(t) {
		t.Fatalf("launch cwd = %q, want %q", orch.launchReq.Cwd, wantStartCWD(t))
	}
	if orch.launchReq.Name != "" {
		t.Fatalf("launch name = %q, want empty", orch.launchReq.Name)
	}
}

func assertResolvedStartPersistence(t *testing.T, threads *stubThreadStore, bindings *stubBindingStore) {
	t.Helper()
	if threads.upsert.Name != "" || threads.upsert.Prompt != "" {
		t.Fatalf("persisted name/prompt = %q/%q, want empty", threads.upsert.Name, threads.upsert.Prompt)
	}
	if threads.upsert.Cwd != wantStartCWD(t) || bindings.upsert.Cwd != wantStartCWD(t) {
		t.Fatalf("persisted cwd = %q/%q, want %q", threads.upsert.Cwd, bindings.upsert.Cwd, wantStartCWD(t))
	}
	if bindings.upsert.Provider != "codex" {
		t.Fatalf("binding provider = %q, want codex", bindings.upsert.Provider)
	}
	if bindings.upsert.ProviderThreadID != "019d5f6b-fb3c-7760-9d6f-54005553f5b3" || bindings.upsert.CodexThreadID != "agent-start" {
		t.Fatalf("binding upsert = %#v", bindings.upsert)
	}
}

func TestServiceStartRequiresProviderUUID(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "agent-start"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-start",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		PromptAssemblyRef: promptAssemblyForTest("test system prompt"),
	})
	if err == nil || !strings.Contains(err.Error(), "provider session UUID required") {
		t.Fatalf("Start() error = %v, want provider session UUID required", err)
	}
	if orch.stoppedAgentID != "agent-start" {
		t.Fatalf("stopped agent = %q, want agent-start", orch.stoppedAgentID)
	}
	if bindings.upsert.AgentID != "" {
		t.Fatalf("binding upsert = %#v, want none", bindings.upsert)
	}
}

func TestServiceStartAllowsDeferredClaudeProviderUUID(t *testing.T) {
	t.Skip("Claude is disabled")
	t.Parallel()

	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: "agent-start-claude"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Start(context.Background(), StartRequest{
		AgentID:  "agent-start-claude",
		Provider: "claude",
		CWD:      wantStartCWD(t),
		Name:     "worker-agent",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.ThreadID != "agent-start-claude" || result.SessionID != "agent-start-claude" {
		t.Fatalf("result = %#v, want public thread fallback while Claude UUID is deferred", result)
	}
	if bindings.upsert.ProviderThreadID != "" || bindings.upsert.SessionUUID != "" {
		t.Fatalf("binding upsert provider identifiers = %q/%q, want empty until system:init", bindings.upsert.ProviderThreadID, bindings.upsert.SessionUUID)
	}
	if bindings.upsert.CodexThreadID != "agent-start-claude" {
		t.Fatalf("binding CodexThreadID = %q, want public thread id", bindings.upsert.CodexThreadID)
	}
	if orch.stoppedAgentID != "" {
		t.Fatalf("stopped agent = %q, want no stop on deferred Claude UUID", orch.stoppedAgentID)
	}
}

func TestServiceStartDoesNotPersistProviderThreadIDWithoutHistoryFile(t *testing.T) {
	t.Parallel()

	const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b8"
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{threadID: providerUUID}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	result, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-start-no-history",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		PromptAssemblyRef: promptAssemblyForTest("test system prompt"),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.SessionID != providerUUID {
		t.Fatalf("SessionID = %q, want %s", result.SessionID, providerUUID)
	}
	if bindings.upsert.ProviderThreadID != "" {
		t.Fatalf("binding provider_thread_id = %q, want empty without history file", bindings.upsert.ProviderThreadID)
	}
	if bindings.upsert.SessionUUID != providerUUID {
		t.Fatalf("binding session_uuid = %q, want %s", bindings.upsert.SessionUUID, providerUUID)
	}
}

func TestServiceStartPersistsCodexIdentityFromSessionRuntimeConfig(t *testing.T) {
	// This scenario verifies identity reported by the launched session itself;
	// keep legacy default-home injection out of the process-wide test env.
	t.Setenv(legacyDefaultCodexHomeEnvVar, "")

	const providerUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5c1"
	threads := &stubThreadStore{}
	bindings := &stubBindingStore{}
	sessions := &stubSessionProvider{}
	codexHome := t.TempDir()
	wantCodexHome := canonicalCodexHomeForTest(t, codexHome)
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			session := &stubSession{
				threadID: providerUUID,
				runtimeConfig: map[string]any{
					"codexHome":          codexHome,
					"codexInstanceKey":   "default",
					"codexModelProvider": "openai",
				},
			}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, bindings, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-runtime-identity",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		PromptAssemblyRef: promptAssemblyForTest("test system prompt"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if bindings.upsert.CodexHome != wantCodexHome ||
		bindings.upsert.CodexInstanceKey != "default" ||
		bindings.upsert.CodexModelProvider != "openai" {
		t.Fatalf("binding codex identity = %q/%q/%q, want runtime identity", bindings.upsert.CodexHome, bindings.upsert.CodexInstanceKey, bindings.upsert.CodexModelProvider)
	}
	storedConfig, err := decodeStoredThreadConfig(threads.upsert.ConfigOverride)
	if err != nil {
		t.Fatalf("decodeStoredThreadConfig() error = %v", err)
	}
	storedRuntime := storedConfig.Runtime
	if storedRuntime["codexHome"] != wantCodexHome ||
		storedRuntime["codexInstanceKey"] != "default" ||
		storedRuntime["codexModelProvider"] != "openai" {
		t.Fatalf("stored runtime codex identity = %#v, want session runtime identity", storedRuntime)
	}
}

func TestServiceStartPrefersExplicitNameForLaunchAndPersist(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Instructions != "system prompt" {
				t.Fatalf("instructions = %q, want system prompt", req.Instructions)
			}
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b4"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:          "agent-name",
		Provider:         "codex",
		CWD:              wantStartCWD(t),
		Name:             "display name",
		Prompt:           "legacy prompt",
		BaseInstructions: "system prompt",
		PromptAssemblyRef: promptAssemblyStub{
			startAssembly: contract.StartAssembly{BaseInstructions: "system prompt"},
		},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "display name" {
		t.Fatalf("launch name = %q, want display name", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "display name" {
		t.Fatalf("persisted prompt = %q, want display name", threads.upsert.Prompt)
	}
}

func TestServiceStartRejectsMissingPromptAssemblyRef(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			t.Fatal("StartSession must not run without prompt assembly")
			return nil, nil
		},
	}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	_, err := svc.Start(context.Background(), StartRequest{
		AgentID:          "agent-no-assembly",
		Provider:         "codex",
		CWD:              wantStartCWD(t),
		BaseInstructions: "legacy system prompt",
	})
	if err == nil || !strings.Contains(err.Error(), "prompt assembly service is not configured") {
		t.Fatalf("Start() error = %v, want missing prompt assembly service error", err)
	}
}

func TestServiceStartUsesPromptAssemblyRef(t *testing.T) {
	t.Parallel()

	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			if req.Instructions != "assembled system" {
				t.Fatalf("instructions = %q, want assembled system", req.Instructions)
			}
			if got := req.Config["developerInstructions"]; got != "assembled dev" {
				t.Fatalf("developerInstructions = %#v, want assembled dev", got)
			}
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b5"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:          "agent-assembly",
		Provider:         "codex",
		CWD:              wantStartCWD(t),
		Name:             "display name",
		BaseInstructions: "system prompt",
		PromptAssemblyRef: promptAssemblyStub{
			startAssembly: contract.StartAssembly{
				DisplayName:           "assembled name",
				BaseInstructions:      "assembled system",
				DeveloperInstructions: "assembled dev",
			},
		},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if orch.launchReq.Name != "assembled name" {
		t.Fatalf("launch name = %q, want assembled name", orch.launchReq.Name)
	}
	if threads.upsert.Prompt != "assembled name" {
		t.Fatalf("persisted prompt = %q, want assembled name", threads.upsert.Prompt)
	}
}

func TestServiceStartCapturesPromptBoundaryProviderDTO(t *testing.T) {
	t.Parallel()

	boundary := &contract.PromptAssemblyBoundary{
		CachedPrefix: "cached provider prefix",
		UncachedTail: "dynamic provider tail",
	}
	sections := map[string]string{
		"developer": "developer section",
		"system":    "system section",
	}
	assembly := contract.StartAssembly{
		DisplayName:           "boundary display",
		BaseInstructions:      "boundary base",
		Boundary:              boundary,
		DeveloperInstructions: "boundary developer",
		Snapshot: contract.PromptAssemblySnapshot{
			DisplayName:           "boundary display",
			BaseInstructions:      "boundary base",
			Boundary:              boundary,
			DeveloperInstructions: "boundary developer",
			Provider:              "codex",
			Version:               contract.PromptAssemblySnapshotVersion,
			Hash:                  "boundary-hash",
			SectionSnapshot:       sections,
		},
		PrefixShape: contract.PrefixShape{Hash: "prefix-shape-hash"},
	}
	var got dto.StartSessionRequest
	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			got = req
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b8"}
			sessions.session = session
			return session, nil
		},
	}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, &stubThreadOrchestration{}, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-boundary-dto",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		PromptAssemblyRef: promptAssemblyStub{startAssembly: assembly},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	boundary.CachedPrefix = "mutated direct boundary"
	sections["system"] = "mutated section"

	if got.StartAssembly.Boundary == nil || got.StartAssembly.Boundary.CachedPrefix != "cached provider prefix" {
		t.Fatalf("StartAssembly.Boundary = %#v, want cloned provider DTO boundary", got.StartAssembly.Boundary)
	}
	if got.StartAssembly.Snapshot.Boundary == nil || got.StartAssembly.Snapshot.Boundary.UncachedTail != "dynamic provider tail" {
		t.Fatalf("StartAssembly.Snapshot.Boundary = %#v, want cloned provider DTO boundary", got.StartAssembly.Snapshot.Boundary)
	}
	if got.StartAssembly.Snapshot.SectionSnapshot["system"] != "system section" ||
		got.StartAssembly.Snapshot.SectionSnapshot["developer"] != "developer section" {
		t.Fatalf("StartAssembly.Snapshot.SectionSnapshot = %#v, want cloned provider DTO sections", got.StartAssembly.Snapshot.SectionSnapshot)
	}
	if got.StartAssembly.PrefixShape.Hash != "prefix-shape-hash" {
		t.Fatalf("StartAssembly.PrefixShape.Hash = %q, want prefix-shape-hash", got.StartAssembly.PrefixShape.Hash)
	}
}

func TestNewThreadHandlersDispatchStartRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	server := newThreadTestServer(NewService(silentLogger(), nil, nil, nil, nil, nil, nil, nil))
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{name: "provider", raw: `{"provider":"other","cwd":"/tmp/project","prompt":"hello"}`, want: "invalid provider"},
		{name: "approval", raw: `{"provider":"codex","approval_policy":"later","cwd":"/tmp/project","prompt":"hello"}`, want: "invalid approval policy"},
		{name: "sandbox", raw: `{"sandbox":{`, want: "unexpected end of JSON input"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := server.Dispatch(context.Background(), "thread/start", json.RawMessage(tc.raw))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Dispatch(thread/start) error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestServiceStartForwardsLaunchSkills 验证启动请求会原样转发显式指定的 launch skill 字段。
// 这些字段属于 provider 启动边界，不能在 thread service 内被重排或丢弃。
func TestServiceStartForwardsLaunchSkills(t *testing.T) {
	t.Parallel()

	var got dto.StartSessionRequest
	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			got = req
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b6"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-launch-skills",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		PromptAssemblyRef: promptAssemblyForTest("test system prompt"),
		LaunchSkillNames:  []string{"planner", "reviewer"},
		LaunchSkillRefs:   []dto.SkillRef{{Key: "project::planner:/repo/.agent/skills/planner", Name: "planner", Scope: "project", Path: "/repo/.agent/skills/planner", Source: dto.SkillSourceManual}},
		ForceLaunchSkills: true,
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(got.LaunchSkillNames) != 2 || got.LaunchSkillNames[0] != "planner" || got.LaunchSkillNames[1] != "reviewer" {
		t.Fatalf("LaunchSkillNames = %#v", got.LaunchSkillNames)
	}
	if !got.ForceLaunchSkills {
		t.Fatalf("ForceLaunchSkills should be true")
	}
	if len(got.LaunchSkillRefs) != 1 || got.LaunchSkillRefs[0].Key == "" || got.LaunchSkillRefs[0].Scope != "project" {
		t.Fatalf("LaunchSkillRefs = %#v", got.LaunchSkillRefs)
	}
}

// TestServiceStartLeavesLaunchSkillsEmptyByDefault 验证未显式指定 launch skill 时保持零值。
// 默认路径不能隐式下发 skill 字段，避免旧调用方被新字段改变启动行为。
func TestServiceStartLeavesLaunchSkillsEmptyByDefault(t *testing.T) {
	t.Parallel()

	var got dto.StartSessionRequest
	threads := &stubThreadStore{}
	sessions := &stubSessionProvider{}
	starter := &startOnlySessionStarter{
		onStart: func(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
			got = req
			session := &stubSession{threadID: "019d5f6b-fb3c-7760-9d6f-54005553f5b7"}
			sessions.session = session
			return session, nil
		},
	}
	orch := &stubThreadOrchestration{}
	svc := NewService(silentLogger(), threads, nil, sessions, starter, nil, orch, nil).(*service)

	if _, err := svc.Start(context.Background(), StartRequest{
		AgentID:           "agent-legacy",
		Provider:          "codex",
		CWD:               wantStartCWD(t),
		PromptAssemblyRef: promptAssemblyForTest("test system prompt"),
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got.LaunchSkillNames != nil {
		t.Fatalf("LaunchSkillNames should be nil by default, got %#v", got.LaunchSkillNames)
	}
	if got.LaunchSkillRefs != nil {
		t.Fatalf("LaunchSkillRefs should be nil by default, got %#v", got.LaunchSkillRefs)
	}
	if got.ForceLaunchSkills {
		t.Fatalf("ForceLaunchSkills should default false")
	}
}

func TestStartSessionRejectsMissingCWD(t *testing.T) {
	t.Parallel()

	called := false
	svc := &service{starter: &startOnlySessionStarter{
		onStart: func(context.Context, dto.StartSessionRequest) (contract.Session, error) {
			called = true
			return &stubSession{threadID: "provider-thread"}, nil
		},
	}}

	_, err := svc.startSession(context.Background(), StartRequest{Provider: "codex"}, contract.StartInput{}, contract.StartAssembly{}, "agent-1")
	if err == nil || !strings.Contains(err.Error(), "cwd is required") {
		t.Fatalf("startSession() error = %v, want cwd required", err)
	}
	if called {
		t.Fatal("StartSession was called despite missing cwd")
	}
}

type startOnlySessionStarter struct {
	onStart func(context.Context, dto.StartSessionRequest) (contract.Session, error)
}

func (s *startOnlySessionStarter) StartSession(ctx context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	if s.onStart == nil {
		return nil, errors.New("unexpected start session")
	}
	session, err := s.onStart(ctx, req)
	if err != nil {
		return nil, err
	}
	return attachStartedCodexRuntimeIdentityForTest(req, session), nil
}

func (s *startOnlySessionStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	return nil, errors.New("unexpected resume session")
}

type promptAssemblyStub struct {
	startAssembly contract.StartAssembly
}

func promptAssemblyForTest(base string) promptAssemblyStub {
	return promptAssemblyStub{startAssembly: contract.StartAssembly{BaseInstructions: base}}
}

func (p promptAssemblyStub) AssembleStart(context.Context, contract.StartInput) (contract.StartAssembly, error) {
	return p.startAssembly, nil
}

func (promptAssemblyStub) AssembleTurn(context.Context, contract.TurnInput) (contract.TurnAssembly, error) {
	return contract.TurnAssembly{}, nil
}

func (promptAssemblyStub) AssembleAgent(context.Context, contract.AgentInput) (contract.StartAssembly, error) {
	return contract.StartAssembly{}, nil
}

func (promptAssemblyStub) Invalidate(context.Context, contract.InvalidateReason) error {
	return nil
}

func wantStartCWD(t *testing.T) string {
	t.Helper()
	return "/tmp/super-agent-thread-start-test"
}
