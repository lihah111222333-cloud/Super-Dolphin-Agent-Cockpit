package codexapp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
)

type stubRuntimeReporter struct {
	last  contract.RuntimeReport
	calls int
	err   error
}

func (s *stubRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	s.calls++
	s.last = report
	return s.err
}

func TestNewDriverUsesEnvServerURLAndName(t *testing.T) {
	t.Setenv("CODEX_APP_SERVER_URL", " ws://127.0.0.1:9123 ")
	got, ok := newDriver(nil, nil, nil, nil, nil, nil, nil, nil, nil).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, nil, nil, nil, nil, nil, nil, nil))
	}
	if got.logger == nil {
		t.Fatal("newDriver() logger = nil")
	}
	if got.serverURL != "ws://127.0.0.1:9123" {
		t.Fatalf("serverURL = %q, want ws://127.0.0.1:9123", got.serverURL)
	}
	if got.Name() != "codex" {
		t.Fatalf("Name() = %q, want codex", got.Name())
	}
}

func TestCloseSessionReleasesCodexToolSurface(t *testing.T) {
	recorder := &toolBridgeRPCRecorder{}
	serverURL := startToolBridgeRPCServer(t, recorder)
	manager := &ServerManager{}
	d := requireToolBridgeDriver(t, newDriver(nil, nil, nil, nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, nil))
	var prepared contract.CodexToolSurfaceScope
	var bound contract.CodexToolSurfaceScope
	d.prepareTools = func(_ context.Context, scope contract.CodexToolSurfaceScope) ([]codexprotocol.DynamicToolSchema, error) {
		prepared = scope
		return []codexprotocol.DynamicToolSchema{{Name: "grep", InputSchema: json.RawMessage(`{"type":"object"}`)}}, nil
	}
	d.bindTools = func(scope contract.CodexToolSurfaceScope) error {
		bound = scope
		return nil
	}
	var released []contract.CodexToolSurfaceScope
	d.releaseTools = func(scope contract.CodexToolSurfaceScope) error {
		released = append(released, scope)
		return nil
	}
	workDir := t.TempDir()

	sessionAny, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		AgentID: "agent-1",
		CWD:     workDir,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := requireCodexSession(t, sessionAny, "StartSession")
	if err := s.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if len(released) != 1 {
		t.Fatalf("release calls = %d, want 1", len(released))
	}
	if prepared.SurfaceID == "" {
		t.Fatalf("prepared surface scope = %#v, want surface id", prepared)
	}
	if bound.SurfaceID != prepared.SurfaceID || bound.ProviderThreadID != "provider-thread-1" {
		t.Fatalf("bind scope = %#v, want same surface id and provider thread", bound)
	}
	if released[0].SurfaceID != prepared.SurfaceID || released[0].AgentID != "" || released[0].ProviderThreadID != "" {
		t.Fatalf("release scope = %#v, want surface id only", released[0])
	}
}

func TestCodexNativeToolPolicyMapsDisabledToolsToProcessFlags(t *testing.T) {
	policy := codexNativeToolPolicyFromConfig(map[string]any{
		codexDisabledNativeToolsConfigKey: []any{"write_new_file", "shell", "apply_patch"},
	})
	wantArgs := []string{"--disable", "shell_tool", "--disable", "unified_exec"}
	if got := policy.AppServerArgs(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("AppServerArgs = %#v, want %#v", got, wantArgs)
	}
	if tier := policy.Tier(contract.CodexNativeToolApplyPatch); tier != contract.NativeToolEnforcementNativeHard {
		t.Fatalf("apply_patch tier = %q, want native hard", tier)
	}
}

func TestCodexNativeToolPolicyUsesReadOnlySandboxForPartialWriteDisable(t *testing.T) {
	params := threadStartParams{}
	codexNativeToolPolicyFromConfig(map[string]any{
		codexDisabledNativeToolsConfigKey: []string{"apply_patch"},
	}).ApplyThreadStartParams(&params)
	if params.ApprovalPolicy != "never" {
		t.Fatalf("ApprovalPolicy = %q, want never", params.ApprovalPolicy)
	}
	if string(params.Sandbox) != `{"read-only":null}` {
		t.Fatalf("Sandbox = %s, want read-only object", string(params.Sandbox))
	}
}

func TestNewDriverFactoryCreateReturnsCodexDriver(t *testing.T) {
	t.Parallel()

	factory := NewDriverFactory(nil, nil, nil, nil, nil, nil, nil, nil)
	if factory.Name != "codex" {
		t.Fatalf("factory.Name = %q, want codex", factory.Name)
	}
	got, ok := factory.Create().(*driver)
	if !ok {
		t.Fatalf("factory.Create() type = %T, want *driver", factory.Create())
	}
	if got.Name() != "codex" {
		t.Fatalf("created driver Name() = %q, want codex", got.Name())
	}
}

func TestDriverReportRuntimeUsesParsedServerURLPort(t *testing.T) {
	reporter := &stubRuntimeReporter{}
	t.Setenv("CODEX_APP_SERVER_URL", " ws://127.0.0.1:9123/ws ")
	got := newDriver(nil, nil, nil, reporter, nil, nil, nil, nil, nil).(*driver)
	got.reportRuntime(" agent-1 ")
	if reporter.calls != 1 {
		t.Fatalf("ReportRuntime() calls = %d, want 1", reporter.calls)
	}
	if reporter.last.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", reporter.last.AgentID)
	}
	if reporter.last.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", reporter.last.Provider)
	}
	if reporter.last.Port != 9123 {
		t.Fatalf("Port = %d, want 9123", reporter.last.Port)
	}
}

func TestNewSessionInitializesStateAndCapabilities(t *testing.T) {
	t.Parallel()

	s, err := newSession(context.Background(), nil, startCodexTestServer(t), " agent-1 ", nil, nil, nil)
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)

	assertNewCodexSessionState(t, s)
	assertCodexSessionCapabilities(t, s)
}

func assertNewCodexSessionState(t *testing.T, s *session) {
	t.Helper()
	if s.agentID != "agent-1" {
		t.Fatalf("agentID = %q, want agent-1", s.agentID)
	}
	if s.transport == nil || !s.transport.Running() {
		t.Fatal("newSession() transport is not running")
	}
	// P22 P1c: newSession must build the session ctx, cancel and runtime
	// handle, but must NOT have started the runtime yet — Start() is an
	// explicit production call site inside StartSession / ResumeSession.
	if s.ctx == nil || s.cancel == nil {
		t.Fatal("newSession() did not initialize session ctx / cancel")
	}
	if s.runtime == nil {
		t.Fatal("newSession() did not build SessionRuntime handle")
	}
	if s.runtime.Started() {
		t.Fatal("newSession() must not implicitly Start() the runtime")
	}
}

func assertCodexSessionCapabilities(t *testing.T, s *session) {
	t.Helper()
	for cap, want := range codexCapabilities {
		if s.caps[cap] != want {
			t.Fatalf("caps[%q] = %v, want %v", cap, s.caps[cap], want)
		}
	}
}

func TestSessionCapabilitiesReturnsClone(t *testing.T) {
	t.Parallel()

	s := &session{caps: cloneCaps(codexCapabilities)}
	got := s.Capabilities()
	got[dto.CapThreadList] = false
	if !contract.HasCapability(s.caps, dto.CapThreadList) {
		t.Fatal("Capabilities() returned aliased map")
	}
}

func TestBuildThreadStartParamsUsesStartAssemblyInstructions(t *testing.T) {
	t.Parallel()

	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		CWD:          " /repo ",
		Model:        " gpt-5.5 ",
		Instructions: "legacy instructions",
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "assembled base",
			DeveloperInstructions: "assembled dev",
		},
		Config: map[string]any{"modelProvider": "openai"},
	})
	if params.BaseInstructions != "assembled base" {
		t.Fatalf("BaseInstructions = %q, want assembled base", params.BaseInstructions)
	}
	if params.DeveloperInstructions != "assembled dev" {
		t.Fatalf("DeveloperInstructions = %q, want assembled dev", params.DeveloperInstructions)
	}
	if params.Cwd != "/repo" || params.Model != "gpt-5.5" || params.ModelProvider != "openai" {
		t.Fatalf("unexpected params = %#v", params)
	}
}

func TestBuildThreadStartParamsNormalizesMinimalEffortToLow(t *testing.T) {
	t.Parallel()

	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		Config: map[string]any{"effort": " minimal "},
	})
	if params.Effort != "low" {
		t.Fatalf("Effort = %q, want low", params.Effort)
	}
}

func TestBuildThreadStartParamsPrefersCanonicalCodexModelProvider(t *testing.T) {
	t.Parallel()

	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		Config: map[string]any{
			contract.CodexModelProviderKey: " canonical-relay ",
			"modelProvider":                "legacy-camel",
			"model_provider":               "legacy-snake",
		},
	})
	if params.ModelProvider != "canonical-relay" {
		t.Fatalf("ModelProvider = %q, want canonical-relay", params.ModelProvider)
	}
}

func TestBuildThreadStartParamsKeepsLegacyModelProviderKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config map[string]any
		want   string
	}{
		{name: "camel", config: map[string]any{"modelProvider": " legacy-camel "}, want: "legacy-camel"},
		{name: "snake", config: map[string]any{"model_provider": " legacy-snake "}, want: "legacy-snake"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{Config: tt.config})
			if params.ModelProvider != tt.want {
				t.Fatalf("ModelProvider = %q, want %q", params.ModelProvider, tt.want)
			}
		})
	}
}

func TestBuildThreadStartParamsUsesCodexModelProviderForCLI(t *testing.T) {
	t.Parallel()

	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		Provider: "codex",
		CWD:      "/repo",
		Model:    "gpt-5.5",
		Config: map[string]any{
			"provider":           "codex",
			"modelProvider":      "codex",
			"codexModelProvider": "openai",
		},
	})

	if params.ModelProvider != "openai" {
		t.Fatalf("ModelProvider = %q, want codexModelProvider value openai", params.ModelProvider)
	}
}

func TestBuildThreadStartParamsIncludesStartRuntimeContext(t *testing.T) {
	t.Parallel()

	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "assembled base",
			UserContext: map[string]string{
				"runtimeExtras": "可用专家: main/expert/prompt",
			},
			SystemContext: dto.SystemContext{"gitStatus": "## main\n M prompt.go"},
		},
	})

	for _, want := range []string{"assembled base", "可用专家: main/expert/prompt", "# System Context"} {
		if !strings.Contains(params.BaseInstructions, want) {
			t.Fatalf("BaseInstructions = %q, want substring %q", params.BaseInstructions, want)
		}
	}
}

func TestBuildThreadStartParamsDoesNotDuplicateBoundaryRuntimeExtras(t *testing.T) {
	t.Parallel()

	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "assembled base\n\n可用专家: main/expert/prompt",
			Boundary: &dto.PromptAssemblyBoundary{
				CachedPrefix: "assembled base",
				UncachedTail: "可用专家: main/expert/prompt",
			},
			UserContext: map[string]string{
				"currentDate":   "Today's date is 2026-05-22.",
				"runtimeExtras": "可用专家: main/expert/prompt",
			},
			SystemContext: dto.SystemContext{"gitStatus": "## main\n M prompt.go"},
		},
	})

	if strings.Count(params.BaseInstructions, "可用专家: main/expert/prompt") != 1 {
		t.Fatalf("BaseInstructions = %q, want available experts exactly once", params.BaseInstructions)
	}
	for _, want := range []string{"assembled base", "Today's date is 2026-05-22.", "# System Context"} {
		if !strings.Contains(params.BaseInstructions, want) {
			t.Fatalf("BaseInstructions = %q, want substring %q", params.BaseInstructions, want)
		}
	}
}

func TestBuildThreadStartParamsDoesNotPrependLegacySkillManifest(t *testing.T) {
	t.Parallel()

	params := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{
		StartAssembly: dto.StartAssembly{BaseInstructions: "assembled base"},
	})

	if strings.Contains(params.BaseInstructions, "可用 skills") || strings.Contains(params.BaseInstructions, "skill_read_section") {
		t.Fatalf("BaseInstructions = %q, must not include legacy skill manifest", params.BaseInstructions)
	}
	if params.BaseInstructions != "assembled base" {
		t.Fatalf("BaseInstructions = %q, want assembled base", params.BaseInstructions)
	}
}

func TestBuildThreadResumeParamsUsesPromptSnapshotInstructions(t *testing.T) {
	t.Parallel()

	params := buildThreadResumeParams(dto.ResumeSessionRequest{
		CWD:    " /repo ",
		Model:  " gpt-5.5 ",
		Effort: " high ",
		PromptSnapshot: dto.PromptAssemblySnapshot{
			BaseInstructions:      "snapshot base",
			DeveloperInstructions: "snapshot dev",
		},
	})
	if params.BaseInstructions != "snapshot base" {
		t.Fatalf("BaseInstructions = %q, want snapshot base", params.BaseInstructions)
	}
	if params.DeveloperInstructions != "snapshot dev" {
		t.Fatalf("DeveloperInstructions = %q, want snapshot dev", params.DeveloperInstructions)
	}
	if params.Cwd != "/repo" || params.Model != "gpt-5.5" || params.Effort != "high" {
		t.Fatalf("unexpected params = %#v", params)
	}
}

func TestSessionRuntimeConfigSnapshotIncludesPromptInstructions(t *testing.T) {
	t.Parallel()

	s := &session{}
	s.setRuntimeConfig(map[string]any{"developer_instructions": "legacy dev"})
	s.setRuntimeConfigValue("baseInstructions", "base")
	got := s.RuntimeConfigSnapshot()
	if got["baseInstructions"] != "base" {
		t.Fatalf("baseInstructions = %#v, want base", got["baseInstructions"])
	}
	if got["developerInstructions"] != "legacy dev" {
		t.Fatalf("developerInstructions = %#v, want legacy dev", got["developerInstructions"])
	}
}

func TestDriverStartSessionUsesAppManagedCodexHomeWhenConfigMissing(t *testing.T) {
	userHome := t.TempDir()
	t.Setenv("SUPER_DOLPHIN_RUNTIME_MODE", "packaged")
	t.Setenv("SUPER_DOLPHIN_HOME", filepath.Join(userHome, ".super-dolphin"))
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	wantHome := mustCanonicalAppManagedCodexHome(t, userHome)
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		return startSessionInjectResult(method, wantHome)
	})
	d := &driver{
		pool:      newSingleURLPoolForTest(t, serverURL),
		mirror:    &recordingSkillMirrorReconciler{},
		listTools: noopCodexToolLister,
	}
	workDir := t.TempDir()
	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "codex",
		AgentID:  "agent-1",
		CWD:      workDir,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)
	assertRuntimeConfigValue(t, s, "codexHome", wantHome)
	assertRuntimeConfigValue(t, s, "cwd", workDir)
}

func startSessionInjectResult(method, home string) json.RawMessage {
	switch method {
	case "initialize":
		return mustJSON(map[string]any{"codexHome": home})
	case "thread/start":
		return mustJSON(map[string]any{
			"thread": map[string]any{"id": "provider-thread-1", "cwd": "/app-server/startup"},
			"model":  "gpt-5.5",
		})
	default:
		return mustJSON(map[string]any{"ok": true})
	}
}

func TestDriverStartSessionCanonicalizesRuntimeCodexHome(t *testing.T) {
	home := t.TempDir()
	wantHome := mustCanonicalCodexHome(t, home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config := map[string]any{
		"codexHome":          "~/.codex",
		"codexInstanceKey":   "default",
		"codexModelProvider": "openai",
	}
	serverURL := startCodexRPCServer(t, func(method string) json.RawMessage {
		return canonicalCodexHomeResult(method, wantHome)
	})
	d := &driver{
		pool:      newSingleURLPoolForTest(t, serverURL),
		mirror:    &recordingSkillMirrorReconciler{},
		listTools: noopCodexToolLister,
	}
	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider: "codex",
		AgentID:  "agent-canonical",
		CWD:      t.TempDir(),
		Config:   config,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)
	assertRuntimeConfigValue(t, s, "codexHome", wantHome)
	assertCodexHomeConfigUnchanged(t, config)
}
