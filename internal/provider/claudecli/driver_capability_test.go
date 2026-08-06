package claudecli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

func TestResolveAbsCWDDropsDotCWD(t *testing.T) {
	t.Parallel()

	got := resolveAbsCWD(".")
	if got != "" {
		t.Fatalf("resolveAbsCWD(\".\") = %q, want empty for untrusted dot cwd", got)
	}
}

func TestResolveAbsCWDPreservesAbsolute(t *testing.T) {
	t.Parallel()

	got := resolveAbsCWD("/tmp/demo")
	if got != "/tmp/demo" {
		t.Fatalf("resolveAbsCWD(\"/tmp/demo\") = %q, want /tmp/demo", got)
	}
}

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

func TestNewDriverDefaultsLoggerAndBinaryPath(t *testing.T) {
	t.Parallel()

	got, ok := newDriver(nil, nil, nil, nil, nil, nil, nil, nil, testSkillMetrics(t)).(*driver)
	if !ok {
		t.Fatalf("newDriver() type = %T, want *driver", newDriver(nil, nil, nil, nil, nil, nil, nil, nil, testSkillMetrics(t)))
	}
	if got.logger == nil {
		t.Fatal("newDriver() logger = nil")
	}
	if got.binaryPath == "" {
		t.Fatal("newDriver() binaryPath = empty")
	}
	if got.Name() != "claude" {
		t.Fatalf("Name() = %q, want claude", got.Name())
	}
}

func TestNewDriverFactoryCreateReturnsClaudeDriver(t *testing.T) {
	t.Parallel()

	factory, err := provideDriverFactory(completeClaudeDriverFactoryParamsForTest(t))
	if err != nil {
		t.Fatalf("provideDriverFactory() error = %v", err)
	}
	if factory.Name != "claude" {
		t.Fatalf("factory.Name = %q, want claude", factory.Name)
	}
	first, ok := factory.Create().(*driver)
	if !ok {
		t.Fatalf("factory.Create() type = %T, want *driver", factory.Create())
	}
	second, ok := factory.Create().(*driver)
	if !ok {
		t.Fatalf("factory.Create() second type = %T, want *driver", factory.Create())
	}
	if first == second {
		t.Fatal("factory.Create() returned the same driver instance twice")
	}
}

func TestClaudeDriverFactoryRequiresDependencyProfile(t *testing.T) {
	params := completeClaudeDriverFactoryParamsForTest(t)
	params.Dependency = contract.DependencyConfig{}
	_, err := provideDriverFactory(params)
	if err == nil || !strings.Contains(err.Error(), "dependency profile") {
		t.Fatalf("provideDriverFactory() error = %v, want missing dependency profile", err)
	}
}

func TestClaudeDriverFactoryRequiresSkillMetricsRegistry(t *testing.T) {
	params := completeClaudeDriverFactoryParamsForTest(t)
	params.Metrics = nil
	if _, err := provideDriverFactory(params); err == nil || !strings.Contains(err.Error(), "skill metrics registry") {
		t.Fatalf("provideDriverFactory() error = %v, want missing skill metrics registry", err)
	}
}

func completeClaudeDriverFactoryParamsForTest(t *testing.T) driverFactoryParams {
	return driverFactoryParams{
		Reporter:   &stubRuntimeReporter{},
		Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileTest},
		Metrics:    testSkillMetrics(t),
	}
}

func TestDriverReportRuntimeUsesProviderWithoutPort(t *testing.T) {
	t.Parallel()

	reporter := &stubRuntimeReporter{}
	got := newDriver(nil, nil, reporter, nil, nil, nil, nil, nil, testSkillMetrics(t)).(*driver)
	if err := got.reportRuntime(" agent-1 "); err != nil {
		t.Fatalf("reportRuntime() error = %v", err)
	}
	if reporter.calls != 1 {
		t.Fatalf("ReportRuntime() calls = %d, want 1", reporter.calls)
	}
	if reporter.last.AgentID != "agent-1" {
		t.Fatalf("AgentID = %q, want agent-1", reporter.last.AgentID)
	}
	if reporter.last.Provider != "claude" {
		t.Fatalf("Provider = %q, want claude", reporter.last.Provider)
	}
	if reporter.last.Port != 0 {
		t.Fatalf("Port = %d, want 0 for stdio transport", reporter.last.Port)
	}
}

func TestClaudeDriverReportRuntimeProductionFailsOnDeferredReporter(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileProduction)}
	driver, _ := newClaudeDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileProduction)

	err := driver.reportRuntime("agent-1")
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("reportRuntime() error = %v, want ErrDependencyDeferred", err)
	}
}

func TestClaudeDriverReportRuntimeDesktopRecordsDeferredStatus(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileDesktopHost)}
	driver, modeReporter := newClaudeDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileDesktopHost)

	if err := driver.reportRuntime("agent-1"); err != nil {
		t.Fatalf("reportRuntime() error = %v", err)
	}
	if !modeReporter.runtimeReportDeferredForTest("agent-1") {
		t.Fatal("runtime report deferred status was not recorded")
	}
}

func TestClaudeStartSessionFailsWhenRuntimeReporterReturnsDeferredInProduction(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileProduction)}
	driver, _ := newClaudeDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileProduction)

	_, err := driver.StartSession(context.Background(), claudeStartRequestForRuntimeReportTest(t))
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("StartSession() error = %v, want ErrDependencyDeferred", err)
	}
}

func TestClaudeResumeSessionFailsWhenRuntimeReporterReturnsDeferredInProduction(t *testing.T) {
	reporter := &stubRuntimeReporter{err: contract.NewDependencyModeError(contract.ErrDependencyDeferred, "runtime_reporter.orchestration_service", contract.DependencyProfileProduction)}
	driver, _ := newClaudeDriverWithRuntimeReporterForTest(t, reporter, contract.DependencyProfileProduction)

	_, err := driver.ResumeSession(context.Background(), claudeResumeRequestForRuntimeReportTest(t))
	if !errors.Is(err, contract.ErrDependencyDeferred) {
		t.Fatalf("ResumeSession() error = %v, want ErrDependencyDeferred", err)
	}
}

func TestClaudeStartSessionGenericReporterErrorFailsInEveryProfile(t *testing.T) {
	for _, profile := range []contract.DependencyProfile{contract.DependencyProfileProduction, contract.DependencyProfileDesktopHost, contract.DependencyProfileTest} {
		t.Run(string(profile), func(t *testing.T) {
			reportErr := errors.New("runtime reporter down")
			reporter := &stubRuntimeReporter{err: reportErr}
			driver, _ := newClaudeDriverWithRuntimeReporterForTest(t, reporter, profile)

			_, err := driver.StartSession(context.Background(), claudeStartRequestForRuntimeReportTest(t))
			if !errors.Is(err, reportErr) {
				t.Fatalf("StartSession() error = %v, want %v", err, reportErr)
			}
		})
	}
}

func TestClaudeResumeSessionGenericReporterErrorFailsInEveryProfile(t *testing.T) {
	for _, profile := range []contract.DependencyProfile{contract.DependencyProfileProduction, contract.DependencyProfileDesktopHost, contract.DependencyProfileTest} {
		t.Run(string(profile), func(t *testing.T) {
			reportErr := errors.New("runtime reporter down")
			reporter := &stubRuntimeReporter{err: reportErr}
			driver, _ := newClaudeDriverWithRuntimeReporterForTest(t, reporter, profile)

			_, err := driver.ResumeSession(context.Background(), claudeResumeRequestForRuntimeReportTest(t))
			if !errors.Is(err, reportErr) {
				t.Fatalf("ResumeSession() error = %v, want %v", err, reportErr)
			}
		})
	}
}

func newClaudeDriverWithRuntimeReporterForTest(t *testing.T, reporter contract.RuntimeReporter, profile contract.DependencyProfile) (*driver, *modeAwareRuntimeReporter) {
	t.Helper()
	t.Setenv(providershared.SuperDolphinHomeEnv, filepath.Join(t.TempDir(), "sd-home"))
	modeReporter, err := newModeAwareRuntimeReporter(reporter, contract.DependencyConfig{Profile: profile}, nil, nil, "claude")
	if err != nil {
		t.Fatalf("newModeAwareRuntimeReporter() error = %v", err)
	}
	driver := newTestDriverWithLaunch(t, &recordingMirrorReconciler{}, func(string, string, string, string, cliLaunchConfig, dto.MCPManifest, string) (*transport, func(), error) {
		return closedTransport(), func() {}, nil
	})
	driver.reporter = modeReporter
	return driver, modeReporter
}

func claudeStartRequestForRuntimeReportTest(t *testing.T) dto.StartSessionRequest {
	t.Helper()
	return dto.StartSessionRequest{
		AgentID:       "agent-1",
		CWD:           t.TempDir(),
		StartAssembly: validClaudeStartAssemblyForTest(),
	}
}

func claudeResumeRequestForRuntimeReportTest(t *testing.T) dto.ResumeSessionRequest {
	t.Helper()
	return dto.ResumeSessionRequest{
		AgentID:          "agent-1",
		ThreadID:         "thread-public",
		ProviderThreadID: "provider-thread-1",
		CWD:              t.TempDir(),
		PromptSnapshot:   validResumePromptSnapshotForTest(),
		ClaudeHome:       t.TempDir(),
	}
}

func TestSessionCapabilitiesMatchClaudeDeclaration(t *testing.T) {
	t.Parallel()

	want := claudeCapabilities()
	s := &session{caps: copyCapabilities(want)}
	got := s.Capabilities()
	if len(got) != len(want) {
		t.Fatalf("len(Capabilities()) = %d, want %d", len(got), len(want))
	}
	for cap, enabled := range want {
		if got[cap] != enabled {
			t.Fatalf("Capabilities()[%q] = %v, want %v", cap, got[cap], enabled)
		}
	}
}

func TestSessionCapabilitiesReturnsClone(t *testing.T) {
	t.Parallel()

	s := &session{caps: copyCapabilities(claudeCapabilities())}
	got := s.Capabilities()
	got[dto.CapMessageSend] = false
	if !contract.HasCapability(s.caps, dto.CapMessageSend) {
		t.Fatal("Capabilities() returned aliased map")
	}
}

func TestClaudeDeclarationOmitsContextCompact(t *testing.T) {
	t.Parallel()

	if contract.HasCapability(claudeCapabilities(), dto.CapContextCompact) {
		t.Fatalf("claudeCapabilities unexpectedly declares %q", dto.CapContextCompact)
	}
}

func TestClaudeCapabilitiesReturnsIndependentMaps(t *testing.T) {
	t.Parallel()

	first := claudeCapabilities()
	first[dto.CapMessageSend] = false
	if !contract.HasCapability(claudeCapabilities(), dto.CapMessageSend) {
		t.Fatal("claude capability declaration leaked a mutable shared map")
	}
}
