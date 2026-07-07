package thread

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestThreadLifecycleSkipsOnlyTypedBindSessionGenerationUnsupported(t *testing.T) {
	svc, logs := lifecycleBindServiceForTest(contract.DependencyProfileDesktopHost, contract.MissingDependencyModeError(
		bindSessionGenerationDependency,
		contract.DependencyProfileDesktopHost,
	))

	if err := svc.bindSessionGeneration(context.Background(), " agent-1 "); err != nil {
		t.Fatalf("bindSessionGeneration() error = %v", err)
	}
	requireBindSessionGenerationLog(t, logs.String(), "agent-1", contract.DependencyProfileDesktopHost)
}

func TestThreadLifecycleSkipsOnlyTypedBindSessionGenerationUnsupportedOnResumePath(t *testing.T) {
	svc, logs := lifecycleBindServiceForTest(contract.DependencyProfileTest, contract.MissingDependencyModeError(
		bindSessionGenerationDependency,
		contract.DependencyProfileTest,
	))

	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err != nil {
		t.Fatalf("bindSessionGeneration() error = %v", err)
	}
	requireBindSessionGenerationLog(t, logs.String(), "agent-1", contract.DependencyProfileTest)
}

func TestThreadLifecycleFailsProductionMissingBindSessionGeneration(t *testing.T) {
	svc, _ := lifecycleBindServiceForTest(
		contract.DependencyProfileProduction,
		errors.New("thread.bind_session_generation is required in production profile"),
	)

	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil {
		t.Fatal("bindSessionGeneration() error = nil, want production bind failure")
	}
}

func TestThreadLifecycleFailsGenericBindSessionGenerationError(t *testing.T) {
	svc, _ := lifecycleBindServiceForTest(contract.DependencyProfileDesktopHost, errors.New("store down"))

	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("bindSessionGeneration() error = %v, want store down", err)
	}
}

func TestThreadLifecycleRejectsMismatchedTypedBindSessionGenerationUnsupported(t *testing.T) {
	for _, tc := range []struct {
		name       string
		service    contract.DependencyProfile
		downstream contract.DependencyProfile
	}{
		{
			name:       "production_rejects_desktop",
			service:    contract.DependencyProfileProduction,
			downstream: contract.DependencyProfileDesktopHost,
		},
		{
			name:       "production_rejects_test",
			service:    contract.DependencyProfileProduction,
			downstream: contract.DependencyProfileTest,
		},
		{
			name:       "desktop_rejects_test",
			service:    contract.DependencyProfileDesktopHost,
			downstream: contract.DependencyProfileTest,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, logs := lifecycleBindServiceForTest(tc.service, contract.NewDependencyModeError(
				contract.ErrUnsupportedDependencyMode,
				bindSessionGenerationDependency,
				tc.downstream,
			))

			err := svc.bindSessionGeneration(context.Background(), "agent-1")
			if !contract.IsDependencyModeError(
				err,
				bindSessionGenerationDependency,
				tc.downstream,
				contract.ErrUnsupportedDependencyMode,
			) {
				t.Fatalf("bindSessionGeneration() error = %v, want downstream typed unsupported", err)
			}
			if strings.Contains(logs.String(), "thread bind-session-generation skipped") {
				t.Fatalf("bindSessionGeneration() log = %q, mismatched typed unsupported must not record skipped", logs.String())
			}
		})
	}
}

func TestThreadLifecycleProductionRequiresOrchestrationAndSessionGenerationProvider(t *testing.T) {
	svc := &service{
		cfg:    &contract.Config{Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction}},
		logger: lifecycleBindLogger(new(bytes.Buffer)),
	}

	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err == nil {
		t.Fatal("bindSessionGeneration() error = nil, want missing orchestration/session generation failure")
	}
}

func TestThreadLifecycleProductionRequiresSessionGenerationProvider(t *testing.T) {
	svc := &service{
		cfg:           &contract.Config{Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileProduction}},
		logger:        lifecycleBindLogger(new(bytes.Buffer)),
		sessions:      bindGenerationSessionProviderWithoutGeneration{},
		orchestration: bindGenerationOrchestration{},
	}

	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err == nil {
		t.Fatal("bindSessionGeneration() error = nil, want missing session generation provider failure")
	}
}

func TestThreadLifecycleFailsEmptyDependencyProfile(t *testing.T) {
	svc := &service{
		cfg:           &contract.Config{Dependency: contract.DependencyConfig{}},
		logger:        lifecycleBindLogger(new(bytes.Buffer)),
		sessions:      bindGenerationSessionProvider{generation: 7},
		orchestration: bindGenerationOrchestration{},
	}

	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "dependency profile is required") {
		t.Fatalf("bindSessionGeneration() error = %v, want missing dependency profile failure", err)
	}
}

func TestThreadLifecycleFailsEmptyDependencyProfileBeforeDependencyAvailability(t *testing.T) {
	logs := new(bytes.Buffer)
	svc := &service{
		cfg:    &contract.Config{Dependency: contract.DependencyConfig{}},
		logger: lifecycleBindLogger(logs),
	}

	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "dependency profile is required") {
		t.Fatalf("bindSessionGeneration() error = %v, want missing dependency profile failure", err)
	}
	if strings.Contains(logs.String(), "thread bind-session-generation skipped") {
		t.Fatalf("bindSessionGeneration() log = %q, empty profile must not record unsupported", logs.String())
	}
}

func TestThreadLifecycleProductionRequiresOrchestrationWhenConfigNil(t *testing.T) {
	logs := new(bytes.Buffer)
	svc := &service{logger: lifecycleBindLogger(logs)}

	err := svc.bindSessionGeneration(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "production profile") {
		t.Fatalf("bindSessionGeneration() error = %v, want nil config production missing dependency failure", err)
	}
	if strings.Contains(logs.String(), "thread bind-session-generation skipped") {
		t.Fatalf("bindSessionGeneration() log = %q, nil config production path must not record unsupported", logs.String())
	}
}

func TestThreadLifecycleDesktopMissingSessionGenerationProviderRecordsTypedUnsupported(t *testing.T) {
	logs := new(bytes.Buffer)
	svc := &service{
		cfg:           &contract.Config{Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileDesktopHost}},
		logger:        lifecycleBindLogger(logs),
		sessions:      bindGenerationSessionProviderWithoutGeneration{},
		orchestration: bindGenerationOrchestration{},
	}

	if err := svc.bindSessionGeneration(context.Background(), "agent-1"); err != nil {
		t.Fatalf("bindSessionGeneration() error = %v, want nil after recorded typed unsupported", err)
	}
	requireBindSessionGenerationLog(t, logs.String(), "agent-1", contract.DependencyProfileDesktopHost)
}

func TestBindSessionGenerationStatusRecorderRequiresCompleteRecord(t *testing.T) {
	recorder, err := newBindSessionGenerationStatusRecorder(lifecycleBindLogger(new(bytes.Buffer)))
	if err != nil {
		t.Fatalf("newBindSessionGenerationStatusRecorder() error = %v", err)
	}

	err = recorder.RecordBindSessionGenerationSkipped(context.Background(), bindGenerationStatusRecord{
		AgentID:    "agent-1",
		Dependency: bindSessionGenerationDependency,
		Profile:    contract.DependencyProfileDesktopHost,
		Status:     bindSessionGenerationStatusUnsupported,
	})
	if err == nil {
		t.Fatal("RecordBindSessionGenerationSkipped() error = nil, want incomplete record failure")
	}
}

func lifecycleBindServiceForTest(profile contract.DependencyProfile, bindErr error) (*service, *bytes.Buffer) {
	logs := new(bytes.Buffer)
	return &service{
		cfg:           &contract.Config{Dependency: contract.DependencyConfig{Profile: profile}},
		logger:        lifecycleBindLogger(logs),
		sessions:      bindGenerationSessionProvider{generation: 7},
		orchestration: bindGenerationOrchestration{err: bindErr},
	}, logs
}

func lifecycleBindLogger(logs *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func testThreadDependencyConfig() *contract.Config {
	return &contract.Config{Dependency: contract.DependencyConfig{Profile: contract.DependencyProfileTest}}
}

func testThreadDependencyConfigWithProjectRoot(projectRoot string) *contract.Config {
	cfg := testThreadDependencyConfig()
	cfg.ProjectRoot = projectRoot
	return cfg
}

func requireBindSessionGenerationLog(
	t *testing.T,
	logLine string,
	agentID string,
	profile contract.DependencyProfile,
) {
	t.Helper()
	for _, want := range []string{
		"thread bind-session-generation skipped",
		"agent_id=" + agentID,
		"dependency=" + bindSessionGenerationDependency,
		"profile=" + string(profile),
		"status=" + bindSessionGenerationStatusUnsupported,
		"reason=",
	} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("bind session generation log = %q, want %q", logLine, want)
		}
	}
}

type bindGenerationSessionProvider struct {
	generation uint64
}

func (p bindGenerationSessionProvider) GetSession(string) (contract.Session, error) {
	return nil, nil
}

func (p bindGenerationSessionProvider) RemoveSession(agentID string) {
	_ = agentID
}

func (p bindGenerationSessionProvider) SessionGeneration(string) uint64 {
	return p.generation
}

type bindGenerationSessionProviderWithoutGeneration struct{}

func (bindGenerationSessionProviderWithoutGeneration) GetSession(string) (contract.Session, error) {
	return nil, nil
}

func (bindGenerationSessionProviderWithoutGeneration) RemoveSession(agentID string) {
	_ = agentID
}

type bindGenerationOrchestration struct {
	err error
}

func (o bindGenerationOrchestration) LaunchAgent(context.Context, LaunchAgentRequest) error {
	return nil
}

func (o bindGenerationOrchestration) StopAgent(context.Context, string) error {
	return nil
}

func (o bindGenerationOrchestration) Recover(context.Context, string) error {
	return nil
}

func (o bindGenerationOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return o.err
}
