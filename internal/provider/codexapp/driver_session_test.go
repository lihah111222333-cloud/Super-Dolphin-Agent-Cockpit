package codexapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	codexprotocol "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/protocol"
)

type stubRuntimeReporter struct {
	last  contract.RuntimeReport
	calls int
	err   error
}

func testApprovalManager() *rpc.ApprovalManager {
	return rpc.NewApprovalManager(nil, nil)
}

func (s *stubRuntimeReporter) ReportRuntime(_ context.Context, report contract.RuntimeReport) error {
	s.calls++
	s.last = report
	return s.err
}

func TestNewDriverUsesEnvServerURLAndName(t *testing.T) {
	t.Setenv("CODEX_APP_SERVER_URL", " ws://127.0.0.1:9123 ")
	got, ok := newDriver(nil, nil, testApprovalManager(), nil, nil, nil, nil, nil, nil).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, testApprovalManager(), nil, nil, nil, nil, nil, nil))
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
	d := requireToolBridgeDriver(t, newDriver(nil, nil, testApprovalManager(), nil, manager, newSingleURLPoolForTest(t, serverURL), &recordingSkillMirrorReconciler{}, nil, nil))
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
		AgentID:       "agent-1",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
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

func mustCodexNativeToolPolicyFromConfig(t *testing.T, cfg map[string]any) codexNativeToolPolicy {
	t.Helper()
	policy, err := codexNativeToolPolicyFromConfig(cfg)
	if err != nil {
		t.Fatalf("codexNativeToolPolicyFromConfig() error = %v", err)
	}
	return policy
}

func TestCodexNativeToolPolicyMapsDisabledToolsToProcessFlags(t *testing.T) {
	policy := mustCodexNativeToolPolicyFromConfig(t, map[string]any{
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

func TestCodexNativeToolPolicyOmitsRemovedChildAgentsFeatureFlag(t *testing.T) {
	policy := mustCodexNativeToolPolicyFromConfig(t, map[string]any{
		codexDisabledNativeToolsConfigKey: []string{"spawn_agent"},
	})
	wantArgs := []string{
		"--disable", "enable_fanout",
		"--disable", "multi_agent",
		"--disable", "multi_agent_v2",
	}
	if got := policy.AppServerArgs(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("AppServerArgs = %#v, want %#v", got, wantArgs)
	}
	if strings.Contains(policy.ProcessSignature(), contract.CodexFeatureChildAgentsMD) {
		t.Fatalf("ProcessSignature contains removed feature flag %q: %q", contract.CodexFeatureChildAgentsMD, policy.ProcessSignature())
	}
}

func TestCodexNativeToolPolicyUsesReadOnlySandboxForPartialWriteDisable(t *testing.T) {
	params := threadStartParams{}
	mustCodexNativeToolPolicyFromConfig(t, map[string]any{
		codexDisabledNativeToolsConfigKey: []string{"apply_patch"},
	}).ApplyThreadStartParams(&params)
	if params.ApprovalPolicy != "never" {
		t.Fatalf("ApprovalPolicy = %q, want never", params.ApprovalPolicy)
	}
	if string(params.Sandbox) != `"read-only"` {
		t.Fatalf("Sandbox = %s, want read-only mode string", string(params.Sandbox))
	}
	assertJSONEqual(t, params.SandboxPolicy, `{"type":"readOnly"}`)
}

func TestCodexNativeToolPolicyDoesNotOverrideSandboxForNativelyDisabledWriteTools(t *testing.T) {
	params := threadStartParams{}
	policy := mustCodexNativeToolPolicyFromConfig(t, map[string]any{
		codexDisabledNativeToolsConfigKey: []string{
			contract.CodexNativeToolShell,
			contract.CodexNativeToolApplyPatch,
			contract.CodexNativeToolWriteNewFile,
			contract.CodexNativeToolSpawnAgent,
		},
	})

	policy.ApplyThreadStartParams(&params)
	if params.ApprovalPolicy != "" {
		t.Fatalf("ApprovalPolicy = %q, want unchanged", params.ApprovalPolicy)
	}
	if len(params.Sandbox) != 0 {
		t.Fatalf("Sandbox = %s, want unchanged", string(params.Sandbox))
	}
	if len(params.SandboxPolicy) != 0 {
		t.Fatalf("SandboxPolicy = %s, want unchanged", string(params.SandboxPolicy))
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
	got := newDriver(nil, nil, testApprovalManager(), reporter, nil, nil, nil, nil, nil).(*driver)
	if err := got.reportRuntime(" agent-1 ", got.serverURL); err != nil {
		t.Fatalf("reportRuntime() error = %v", err)
	}
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

func TestCodexDriverFactoryRequiresDependencyProfile(t *testing.T) {
	params := completeCodexDriverFactoryParamsForTest()
	params.Dependency = contract.DependencyConfig{}
	_, err := provideDriverFactory(params)
	if err == nil || !strings.Contains(err.Error(), "dependency profile") {
		t.Fatalf("provideDriverFactory() error = %v, want missing dependency profile", err)
	}
}

func TestCodexDriverFactoryRequiresApprovalManager(t *testing.T) {
	params := completeCodexDriverFactoryParamsForTest()
	params.Approvals = nil

	_, err := provideDriverFactory(params)
	if !errors.Is(err, errApprovalManagerRequired) {
		t.Fatalf("provideDriverFactory() error = %v, want %v", err, errApprovalManagerRequired)
	}
}

func TestDriverStartAndResumeRequireApprovalManagerBeforeRequestPreparation(t *testing.T) {
	d := &driver{}

	if _, err := d.StartSession(context.Background(), dto.StartSessionRequest{}); !errors.Is(err, errApprovalManagerRequired) {
		t.Fatalf("StartSession() error = %v, want %v", err, errApprovalManagerRequired)
	}
	if _, err := d.ResumeSession(context.Background(), dto.ResumeSessionRequest{}); !errors.Is(err, errApprovalManagerRequired) {
		t.Fatalf("ResumeSession() error = %v, want %v", err, errApprovalManagerRequired)
	}
}

func completeCodexDriverFactoryParamsForTest() DriverFactoryParams {
	return DriverFactoryParams{
		Approvals:  rpc.NewApprovalManager(nil, nil),
		Reporter:   &stubRuntimeReporter{},
		Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileTest},
	}
}

func TestCodexDriverReportRuntimeProductionFailsOnDeferredReporter(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileProduction)}
	driver, _ := newCodexDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileProduction)

	err := driver.reportRuntime("agent-1", driver.serverURL)
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("reportRuntime() error = %v, want ErrDependencyDeferred", err)
	}
}

func TestCodexDriverReportRuntimeDesktopRecordsDeferredStatus(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileDesktopHost)}
	driver, modeReporter := newCodexDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileDesktopHost)

	if err := driver.reportRuntime("agent-1", driver.serverURL); err != nil {
		t.Fatalf("reportRuntime() error = %v", err)
	}
	if !modeReporter.runtimeReportDeferredForTest("agent-1") {
		t.Fatal("runtime report deferred status was not recorded")
	}
}

func TestCodexStartSessionFailsWhenRuntimeReporterReturnsDeferredInProduction(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileProduction)}
	driver, _ := newCodexDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileProduction)

	_, err := driver.StartSession(context.Background(), codexStartRequestForRuntimeReportTest(t))
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("StartSession() error = %v, want ErrDependencyDeferred", err)
	}
}

func TestCodexResumeSessionFailsWhenRuntimeReporterReturnsDeferredInProduction(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileProduction)}
	driver, _ := newCodexDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileProduction)

	_, err := driver.ResumeSession(context.Background(), codexResumeRequestForRuntimeReportTest(t))
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("ResumeSession() error = %v, want ErrDependencyDeferred", err)
	}
}

func TestCodexStartSessionGenericReporterErrorFailsInEveryProfile(t *testing.T) {
	for _, profile := range []contract.DependencyProfile{contract.DependencyProfileProduction, contract.DependencyProfileDesktopHost, contract.DependencyProfileTest} {
		t.Run(string(profile), func(t *testing.T) {
			reportErr := errors.New("runtime reporter down")
			reporter := &stubRuntimeReporter{err: reportErr}
			driver, _ := newCodexDriverWithRuntimeReporterForTest(t, reporter, profile)

			_, err := driver.StartSession(context.Background(), codexStartRequestForRuntimeReportTest(t))
			if !errors.Is(err, reportErr) {
				t.Fatalf("StartSession() error = %v, want %v", err, reportErr)
			}
		})
	}
}

func TestCodexResumeSessionGenericReporterErrorFailsInEveryProfile(t *testing.T) {
	for _, profile := range []contract.DependencyProfile{contract.DependencyProfileProduction, contract.DependencyProfileDesktopHost, contract.DependencyProfileTest} {
		t.Run(string(profile), func(t *testing.T) {
			reportErr := errors.New("runtime reporter down")
			reporter := &stubRuntimeReporter{err: reportErr}
			driver, _ := newCodexDriverWithRuntimeReporterForTest(t, reporter, profile)

			_, err := driver.ResumeSession(context.Background(), codexResumeRequestForRuntimeReportTest(t))
			if !errors.Is(err, reportErr) {
				t.Fatalf("ResumeSession() error = %v, want %v", err, reportErr)
			}
		})
	}
}

func newCodexDriverWithRuntimeReporterForTest(t *testing.T, reporter contract.RuntimeReporter, profile contract.DependencyProfile) (*driver, *modeAwareRuntimeReporter) {
	t.Helper()
	setDefaultCodexHomeEnvForTest(t)
	modeReporter, err := newModeAwareRuntimeReporter(reporter, contract.DependencyConfig{Profile: profile}, nil, nil, "codex")
	if err != nil {
		t.Fatalf("newModeAwareRuntimeReporter() error = %v", err)
	}
	serverURL := startCodexRPCServer(t, runtimeReportCodexRPCResult)
	driver := &driver{
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		reporter:     modeReporter,
		listTools:    noopCodexToolLister,
	}
	return driver, modeReporter
}

func runtimeReportCodexRPCResult(method string) json.RawMessage {
	switch method {
	case "thread/start":
		return mustJSON(map[string]any{"thread": map[string]any{"id": "provider-thread-1"}, "model": "gpt-5"})
	case "thread/resume":
		return mustJSON(map[string]any{
			"thread":         map[string]any{"id": "provider-thread-1"},
			"approvalPolicy": "on-request",
		})
	default:
		return mustJSON(map[string]any{"ok": true})
	}
}

func codexStartRequestForRuntimeReportTest(t *testing.T) dto.StartSessionRequest {
	t.Helper()
	return dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
	}
}

func codexResumeRequestForRuntimeReportTest(t *testing.T) dto.ResumeSessionRequest {
	t.Helper()
	codexHome := setDefaultCodexHomeEnvForTest(t)
	return dto.ResumeSessionRequest{
		Provider:           "codex",
		AgentID:            "agent-1",
		ThreadID:           "thread-public",
		ProviderThreadID:   "provider-thread-1",
		CWD:                t.TempDir(),
		PromptSnapshot:     validResumePromptSnapshotForTest(),
		CodexHome:          codexHome,
		CodexInstanceKey:   "default",
		CodexModelProvider: "openai",
	}
}

func TestNewSessionInitializesStateAndCapabilities(t *testing.T) {
	t.Parallel()

	s, err := newSession(context.Background(), nil, startCodexTestServer(t), " agent-1 ", nil, testApprovalManager(), nil, testSkillMetrics(t))
	if err != nil {
		t.Fatalf("newSession() error = %v", err)
	}
	defer closeCodexTestSession(t, s)

	assertNewCodexSessionState(t, s)
	assertCodexSessionCapabilities(t, s)
}

func TestGenerateApprovalSessionScopeFormatsRFC4122Version4(t *testing.T) {
	entropy := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	scope, err := generateApprovalSessionScope(bytes.NewReader(entropy))
	if err != nil {
		t.Fatalf("generateApprovalSessionScope() error = %v", err)
	}
	if scope != "00010203-0405-4607-8809-0a0b0c0d0e0f" {
		t.Fatalf("approval session scope = %q, want RFC 4122 version 4 UUID", scope)
	}
}

func assertNewCodexSessionState(t *testing.T, s *session) {
	t.Helper()
	if s.agentID != "agent-1" {
		t.Fatalf("agentID = %q, want agent-1", s.agentID)
	}
	if s.transport == nil || !s.transport.Running() {
		t.Fatal("newSession() transport is not running")
	}
	// newSession 只负责建好 session context、cancel 和 runtime 句柄。
	// runtime 必须等 StartSession/ResumeSession 的生产入口显式启动，避免构造期隐式拉起后台循环。
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
	for cap, want := range codexCapabilities() {
		if s.caps[cap] != want {
			t.Fatalf("caps[%q] = %v, want %v", cap, s.caps[cap], want)
		}
	}
}

func TestSessionCapabilitiesReturnsClone(t *testing.T) {
	first, second := codexCapabilities(), codexCapabilities()
	first[dto.CapMessageSend] = false
	if !second[dto.CapMessageSend] {
		t.Fatal("independent capability set changed after mutating another call")
	}
	s := &session{caps: second}
	got := s.Capabilities()
	got[dto.CapThreadList] = false
	if !contract.HasCapability(s.caps, dto.CapThreadList) {
		t.Fatal("Capabilities() returned aliased map")
	}
}

func mustBuildThreadStartParams(t *testing.T, req dto.StartSessionRequest) threadStartParams {
	t.Helper()
	params, err := (&driver{}).buildThreadStartParams(req)
	if err != nil {
		t.Fatalf("buildThreadStartParams() error = %v", err)
	}
	return params
}

func mustBuildThreadResumeParams(t *testing.T, req dto.ResumeSessionRequest) threadResumeParams {
	t.Helper()
	params, err := buildThreadResumeParams(req)
	if err != nil {
		t.Fatalf("buildThreadResumeParams() error = %v", err)
	}
	return params
}

func TestBuildThreadStartParamsUsesStartAssemblyInstructions(t *testing.T) {
	t.Parallel()

	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
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

func TestBuildThreadStartParamsRejectsEmptyPromptAssembly(t *testing.T) {
	t.Parallel()

	_, err := (&driver{}).buildThreadStartParams(dto.StartSessionRequest{})
	if err == nil || !strings.Contains(err.Error(), "start prompt assembly is empty") {
		t.Fatalf("buildThreadStartParams() error = %v, want empty start prompt assembly error", err)
	}
}

func TestBuildThreadStartParamsIgnoresPrefixShapeContent(t *testing.T) {
	t.Parallel()

	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
		StartAssembly: dto.StartAssembly{
			BaseInstructions:      "assembled base",
			DeveloperInstructions: "assembled dev",
			PrefixShape: dto.PrefixShape{
				Hash:                "shape-hash",
				StaticSectionNames:  []string{"identity"},
				DynamicSectionNames: []string{"memory"},
				CachedPrefixBytes:   12,
				UncachedTailBytes:   8,
			},
		},
	})

	if params.BaseInstructions != "assembled base" {
		t.Fatalf("BaseInstructions = %q, want assembled base", params.BaseInstructions)
	}
	if params.DeveloperInstructions != "assembled dev" {
		t.Fatalf("DeveloperInstructions = %q, want assembled dev", params.DeveloperInstructions)
	}
}

func TestBuildThreadStartParamsNormalizesMinimalEffortToLow(t *testing.T) {
	t.Parallel()

	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
		StartAssembly: dto.StartAssembly{BaseInstructions: "test base"},
		Config:        map[string]any{"effort": " minimal "},
	})
	if params.Effort != "low" {
		t.Fatalf("Effort = %q, want low", params.Effort)
	}
}

func TestBuildThreadStartParamsPrefersCanonicalCodexModelProvider(t *testing.T) {
	t.Parallel()

	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
		StartAssembly: dto.StartAssembly{BaseInstructions: "test base"},
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
			params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
				StartAssembly: dto.StartAssembly{BaseInstructions: "test base"},
				Config:        tt.config,
			})
			if params.ModelProvider != tt.want {
				t.Fatalf("ModelProvider = %q, want %q", params.ModelProvider, tt.want)
			}
		})
	}
}

func TestBuildThreadStartParamsUsesCodexModelProviderForCLI(t *testing.T) {
	t.Parallel()
	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
		Provider: "codex",
		CWD:      "/repo",
		Model:    "gpt-5.5",
		StartAssembly: dto.StartAssembly{
			BaseInstructions: "test base",
		},
		Config: map[string]any{
			"provider":           "codex",
			"modelProvider":      "codex",
			"codexModelProvider": "openai",
			"mcpConfig":          map[string]any{"mcpServers": map[string]any{"my-search": map[string]any{"trustedServerId": "my-search", "transport": "http", "url": "https://your-domain.com/mcp"}}},
		},
	})

	if params.ModelProvider != "openai" {
		t.Fatalf("ModelProvider = %q, want codexModelProvider value openai", params.ModelProvider)
	}
	if !strings.Contains(string(params.MCPConfig), `"my-search"`) {
		t.Fatalf("MCPConfig = %s, want my-search server", string(params.MCPConfig))
	}
}

func TestBuildThreadStartParamsIncludesStartRuntimeContext(t *testing.T) {
	t.Parallel()
	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
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

	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
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

	params := mustBuildThreadStartParams(t, dto.StartSessionRequest{
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

	params := mustBuildThreadResumeParams(t, dto.ResumeSessionRequest{
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

func TestBuildThreadResumeParamsRejectsEmptyPromptSnapshot(t *testing.T) {
	t.Parallel()

	_, err := buildThreadResumeParams(dto.ResumeSessionRequest{})
	if err == nil || !strings.Contains(err.Error(), "resume prompt snapshot has empty base instructions") {
		t.Fatalf("buildThreadResumeParams() error = %v, want empty resume prompt snapshot error", err)
	}
}

func TestBuildThreadResumeParamsUsesProviderEffectiveBoundarySnapshot(t *testing.T) {
	t.Parallel()

	params := mustBuildThreadResumeParams(t, dto.ResumeSessionRequest{
		PromptSnapshot: dto.PromptAssemblySnapshot{
			BaseInstructions: "stale base must not win",
			Boundary: &dto.PromptAssemblyBoundary{
				CachedPrefix: "assembled base",
				UncachedTail: strings.Join([]string{
					"Today's date is 2026-05-22.",
					"可用专家: main/expert/prompt",
					"# System Context\n## main\n M prompt.go",
				}, "\n\n"),
			},
		},
	})

	for _, want := range []string{
		"assembled base",
		"Today's date is 2026-05-22.",
		"可用专家: main/expert/prompt",
		"# System Context",
	} {
		if !strings.Contains(params.BaseInstructions, want) {
			t.Fatalf("BaseInstructions = %q, want substring %q", params.BaseInstructions, want)
		}
	}
	if strings.Contains(params.BaseInstructions, "stale base must not win") {
		t.Fatalf("BaseInstructions = %q, want boundary prompt to override stale base", params.BaseInstructions)
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
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}
	workDir := t.TempDir()
	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-1",
		CWD:           workDir,
		StartAssembly: validStartAssemblyForTest(),
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
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}
	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-canonical",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
		Config:        config,
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)
	assertRuntimeConfigValue(t, s, "codexHome", wantHome)
	assertCodexHomeConfigUnchanged(t, config)
}

// TestDriverStartSessionSendsRestrictedSandboxPolicyOnWire 确认受限沙箱策略会进入 thread/start wire payload。
func TestDriverStartSessionSendsRestrictedSandboxPolicyOnWire(t *testing.T) {
	t.Parallel()

	startParams := make(chan map[string]any, 1)
	serverURL := startCodexRPCServerWithHandler(t, func(msg jsonRPCMessage) json.RawMessage {
		if msg.Method == "thread/start" {
			var params map[string]any
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				t.Fatalf("decode thread/start params: %v", err)
			}
			startParams <- params
			return mustJSON(map[string]any{
				"thread": map[string]any{"id": "provider-thread-sandbox", "cwd": "/repo"},
				"model":  "gpt-5.5",
			})
		}
		return mustJSON(map[string]any{"ok": true})
	})
	d := &driver{
		approvals:    testApprovalManager(),
		skillMetrics: testSkillMetrics(t),
		pool:         newSingleURLPoolForTest(t, serverURL),
		mirror:       &recordingSkillMirrorReconciler{},
		listTools:    noopCodexToolLister,
	}

	got, err := d.StartSession(context.Background(), dto.StartSessionRequest{
		Provider:      "codex",
		AgentID:       "agent-sandbox",
		CWD:           t.TempDir(),
		StartAssembly: validStartAssemblyForTest(),
		Config: map[string]any{
			"sandbox": map[string]any{
				"type": "readOnly",
				"access": map[string]any{
					"type":                    "restricted",
					"readableRoots":           []string{"/repo/app", "/repo/docs"},
					"includePlatformDefaults": true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("StartSession() error = %v", err)
	}
	s := mustCodexSession(t, got, "StartSession")
	defer closeCodexTestSession(t, s)

	var params map[string]any
	select {
	case params = <-startParams:
	default:
		t.Fatal("thread/start params were not captured")
	}
	rawPolicy, err := json.Marshal(params["sandboxPolicy"])
	if err != nil {
		t.Fatalf("marshal wire sandboxPolicy: %v", err)
	}
	assertJSONEqual(t, rawPolicy, `{
		"type":"readOnly",
		"access":{
			"type":"restricted",
			"readableRoots":["/repo/app","/repo/docs"],
			"includePlatformDefaults":true
		}
	}`)
}
