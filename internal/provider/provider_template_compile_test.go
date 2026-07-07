package provider_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderTemplateSnippetsCompile(t *testing.T) {
	dir := renderProviderTemplatePackage(t)
	runTemplateCommand(t, dir, "gofmt", "-w", "module.go", "provider_contract_test.go", "template_stubs.go", "template_omission_test.go")
	runTemplateCommand(t, dir, "go", "mod", "tidy")
	runTemplateCommand(t, dir, "go", "test", "./...", "-run", "^TestRenderedTemplateProductionOmissions$", "-count=1")
}

func renderProviderTemplatePackage(t *testing.T) string {
	t.Helper()

	repoRoot := providerRepoRoot(t)
	dir := t.TempDir()
	writeTemplateFile(t, dir, "go.mod", renderedTemplateGoMod(repoRoot))
	copyRenderedTemplateRepoFile(t, repoRoot, dir, "go.sum")
	copyTemplateSnippet(t, repoRoot, dir, "module.go.txt", "module.go")
	copyTemplateSnippet(t, repoRoot, dir, "provider_contract_test.go.txt", "provider_contract_test.go")
	writeTemplateFile(t, dir, "template_stubs.go", renderedTemplateStubs)
	writeTemplateFile(t, dir, "template_omission_test.go", renderedTemplateOmissionTests)
	return dir
}

func providerRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

func copyTemplateSnippet(t *testing.T, repoRoot, dir, srcName, dstName string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, "internal", "provider", "_template", srcName))
	if err != nil {
		t.Fatalf("read template %s: %v", srcName, err)
	}
	writeTemplateFile(t, dir, dstName, string(raw))
}

func copyRenderedTemplateRepoFile(t *testing.T, repoRoot, dir, name string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot, name))
	if err != nil {
		t.Fatalf("read repo file %s: %v", name, err)
	}
	writeTemplateFile(t, dir, name, string(raw))
}

func writeTemplateFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write rendered template %s: %v", name, err)
	}
}

func renderedTemplateGoMod(repoRoot string) string {
	return fmt.Sprintf(`module github.com/anthropic-ai/super-agent-v3/internal/provider/renderedtemplate

go 1.25.7

require github.com/anthropic-ai/super-agent-v3 v0.0.0

replace github.com/anthropic-ai/super-agent-v3 => %s
`, filepath.ToSlash(repoRoot))
}

func runTemplateCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out.String())
	}
}

const renderedTemplateStubs = `package template

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

type TemplateToolbridgeProxy interface {
	CheckToolbridgeReady(context.Context, string) error
}

type TemplateApprovalBridge interface {
	Approve(context.Context, string) (contract.ApprovalDecision, error)
}

type TemplateProviderMirror interface {
	contract.SkillMirrorReconciler
}

type TemplateSessionRecovery interface {
	contract.SessionRecoveryReporter
}

type TemplateTracer interface {
	TraceProvider(context.Context, string) error
}

type DriverConfig struct {
	Reporter        contract.RuntimeReporter
	ToolbridgeProxy TemplateToolbridgeProxy
	Approvals       TemplateApprovalBridge
	Mirror          TemplateProviderMirror
	Recovery        TemplateSessionRecovery
	Tracer          TemplateTracer
	Dependency      contract.DependencyConfig
}

func NewDriver(cfg DriverConfig) contract.Driver {
	return &renderedTemplateDriver{cfg: cfg}
}

func RegisterEventTranslators() {}

type renderedTemplateDriver struct {
	cfg DriverConfig
}

func (d *renderedTemplateDriver) Name() string { return "template" }

func (d *renderedTemplateDriver) StartSession(_ context.Context, req dto.StartSessionRequest) (contract.Session, error) {
	threadID := strings.TrimSpace(req.AgentID)
	if threadID == "" {
		threadID = "rendered-template-start"
	}
	return newTemplateContractSession(threadID), nil
}

func (d *renderedTemplateDriver) ResumeSession(_ context.Context, req dto.ResumeSessionRequest) (contract.Session, error) {
	threadID := strings.TrimSpace(req.ProviderThreadID)
	if threadID == "" {
		threadID = "rendered-template-resume"
	}
	return newTemplateContractSession(threadID), nil
}
`

const renderedTemplateOmissionTests = `package template

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestRenderedTemplateProductionOmissions(t *testing.T) {
	cases := []struct {
		name         string
		mutate       func(*driverFactoryParams)
		wantDirect   string
		wantFx       string
		wantTypedDep string
	}{
		{
			name: "runtime reporter",
			mutate: func(p *driverFactoryParams) {
				p.Reporter = nil
			},
			wantDirect:   "provider.template.runtime_reporter",
			wantFx:       "provider.template.runtime_reporter",
			wantTypedDep: "provider.template.runtime_reporter",
		},
		{
			name: "toolbridge proxy",
			mutate: func(p *driverFactoryParams) {
				p.ToolbridgeProxy = nil
			},
			wantDirect:   "provider.template.toolbridge_proxy",
			wantFx:       "provider.template.toolbridge_proxy",
			wantTypedDep: "provider.template.toolbridge_proxy",
		},
		{
			name: "provider mirror",
			mutate: func(p *driverFactoryParams) {
				p.Mirror = nil
			},
			wantDirect:   "provider.template.mirror",
			wantFx:       "provider.template.mirror",
			wantTypedDep: "provider.template.mirror",
		},
		{
			name: "session recovery",
			mutate: func(p *driverFactoryParams) {
				p.Recovery = nil
			},
			wantDirect:   "provider.template.session_recovery",
			wantFx:       "provider.template.session_recovery",
			wantTypedDep: "provider.template.session_recovery",
		},
		{
			name: "dependency profile",
			mutate: func(p *driverFactoryParams) {
				p.Dependency = contract.DependencyConfig{}
			},
			wantDirect: "dependency profile required",
			wantFx:     "dependency profile required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := completeRenderedTemplateParams()
			tc.mutate(&params)
			_, err := NewDriverFactory(params)
			assertRenderedTemplateError(t, "direct NewDriverFactory", err, tc.wantDirect, tc.wantTypedDep)

			err = renderedTemplateFxError(params)
			assertRenderedTemplateError(t, "fx graph", err, tc.wantFx, tc.wantTypedDep)
		})
	}
}

func completeRenderedTemplateParams() driverFactoryParams {
	return driverFactoryParams{
		Reporter:        renderedTemplateReporter{},
		ToolbridgeProxy: renderedTemplateToolbridge{},
		Approvals:       renderedTemplateApproval{},
		Mirror:          renderedTemplateMirror{},
		Recovery:        renderedTemplateRecovery{},
		Tracer:          renderedTemplateTracer{},
		Dependency:      contract.DependencyConfig{Profile: contract.DependencyProfileProduction},
	}
}

func renderedTemplateFxError(params driverFactoryParams) error {
	type driverGroup struct {
		fx.In
		Drivers []contract.DriverFactory ` + "`" + `group:"drivers"` + "`" + `
	}
	app := fx.New(
		fx.Provide(fx.Annotate(func() (contract.DriverFactory, error) {
			return NewDriverFactory(params)
		}, fx.ResultTags(` + "`" + `group:"drivers"` + "`" + `))),
		fx.Invoke(func(driverGroup) {}),
		fx.NopLogger,
	)
	return app.Err()
}

func assertRenderedTemplateError(t *testing.T, label string, err error, want, wantTypedDep string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s error = nil, want %q", label, want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s error = %v, want %q", label, err, want)
	}
	if wantTypedDep == "" {
		return
	}
	if !contract.IsDependencyModeError(err, wantTypedDep, contract.DependencyProfileProduction, contract.ErrUnsupportedDependencyMode) {
		t.Fatalf("%s error = %v, want typed dependency %s", label, err, wantTypedDep)
	}
}

type renderedTemplateReporter struct{}

func (renderedTemplateReporter) ReportRuntime(context.Context, contract.RuntimeReport) error { return nil }

type renderedTemplateToolbridge struct{}

func (renderedTemplateToolbridge) CheckToolbridgeReady(context.Context, string) error { return nil }

type renderedTemplateApproval struct{}

func (renderedTemplateApproval) Approve(context.Context, string) (contract.ApprovalDecision, error) {
	return contract.ApprovalDecision{}, nil
}

type renderedTemplateMirror struct{}

func (renderedTemplateMirror) ReconcileProviderMirrors(context.Context, string, []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	return contract.SkillMirrorReport{}, nil
}

type renderedTemplateRecovery struct{}

func (renderedTemplateRecovery) ClearStaleProviderThreadID(context.Context, string) error { return nil }
func (renderedTemplateRecovery) RecordProviderSessionUUID(context.Context, string, string) error {
	return nil
}

type renderedTemplateTracer struct{}

func (renderedTemplateTracer) TraceProvider(context.Context, string) error { return nil }

func TestRenderedTemplateUnsupportedErrorType(t *testing.T) {
	err := contract.NewDependencyModeError(contract.ErrUnsupportedDependencyMode, "provider.template.toolbridge_proxy", contract.DependencyProfileProduction)
	if !errors.Is(err, contract.ErrUnsupportedDependencyMode) {
		t.Fatalf("dependency mode error = %v, want unsupported sentinel", err)
	}
}
`
