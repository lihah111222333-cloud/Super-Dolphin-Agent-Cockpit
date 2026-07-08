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
	dir := renderPreparedProviderTemplatePackage(t)
	runTemplateCommand(t, dir, "go", "test", "./...", "-run", "^TestRenderedTemplate(ProductionOmissions|ModuleGraph|AcceptanceCriteriaDeclared)$", "-count=1")
}

func TestRenderedTemplateAcceptanceCriteriaDeclared(t *testing.T) {
	dir := renderPreparedProviderTemplatePackage(t)
	runTemplateCommand(t, dir, "go", "test", "./...", "-run", "^TestRenderedTemplateAcceptanceCriteriaDeclared$", "-count=1")
}

func TestRenderedTemplateAcceptancePlaceholdersFail(t *testing.T) {
	dir := renderPreparedProviderTemplatePackage(t)
	out := runTemplateCommandExpectFailure(t, dir, "go", "test", "./...", "-run", "^TestTemplateProviderContract$", "-count=1")
	if !strings.Contains(out, "replace templateEventTranslationContractCase") {
		t.Fatalf("rendered provider contract failure missing actionable placeholder message:\n%s", out)
	}
}

func renderPreparedProviderTemplatePackage(t *testing.T) string {
	t.Helper()
	dir := renderProviderTemplatePackage(t)
	runTemplateCommand(t, dir, "gofmt", "-w", "module.go", "provider_contract_test.go", "template_stubs.go", "template_omission_test.go", "template_placeholder_probe_test.go")
	runTemplateCommand(t, dir, "go", "mod", "tidy")
	runTemplateCommand(t, dir, "go", "test", "./...", "-run", "^TestRenderedTemplate(ProductionOmissions|ModuleGraph|AcceptanceCriteriaDeclared)$", "-count=1")
	t.Run("rendered acceptance placeholders fail", assertRenderedTemplateAcceptancePlaceholdersFail)
}

func TestRenderedTemplateAcceptancePlaceholdersFail(t *testing.T) {
	assertRenderedTemplateAcceptancePlaceholdersFail(t)
}

func assertRenderedTemplateAcceptancePlaceholdersFail(t *testing.T) {
	t.Helper()
	dir := renderProviderTemplatePackage(t)
	runTemplateCommand(t, dir, "gofmt", "-w", "module.go", "provider_contract_test.go", "template_stubs.go", "template_omission_test.go", "template_placeholder_probe_test.go")
	runTemplateCommand(t, dir, "go", "mod", "tidy")

	output := runTemplateCommandWantError(t, dir, "go", "test", "./...", "-run", "^TestRenderedTemplatePlaceholderFailures$", "-count=1")
	for _, want := range []string{
		"replace templateEventTranslationContractCase",
		"provider raw-event capture and translator evidence",
		"replace templateEventMatrixContractCase",
		"provider event matrix manifest evidence",
		"replace templatePromptParityContractCase",
		"provider start/resume prompt capture",
		"replace templateApprovalContractCase",
		"provider approval bridge or policy capture",
		"replace templateInterruptContractCase",
		"provider interrupt capture",
		"replace templateForceCompleteContractCase",
		"provider force-complete capture",
		"replace templateResumeIdentityContractCase",
		"provider resume identity capture",
		"replace templateToolbridgeContractCase",
		"provider toolbridge/proxy readiness capture",
		"replace templateRuntimeReportContractCase",
		"provider runtime reporter capture",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("rendered provider contract output missing %q:\n%s", want, output)
		}
	}
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
	writeTemplateFile(t, dir, "template_placeholder_probe_test.go", renderedTemplatePlaceholderProbeTests)
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
	output, err := runTemplateCommandOutput(dir, name, args...)
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
}

func runTemplateCommandWantError(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	output, err := runTemplateCommandOutput(dir, name, args...)
	if err == nil {
		t.Fatalf("%s %s error = nil, want rendered template scaffold failure\n%s", name, strings.Join(args, " "), output)
	}
	return output
}

func runTemplateCommandOutput(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func runTemplateCommandExpectFailure(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err == nil {
		t.Fatalf("%s %s succeeded, want failure\n%s", name, strings.Join(args, " "), out.String())
	}
	return out.String()
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

var renderedTemplateEventTranslatorsRegistered int

func RegisterEventTranslators() {
	renderedTemplateEventTranslatorsRegistered++
}

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
	"os"
	"slices"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest"
)

func TestRenderedTemplateAcceptanceCriteriaDeclared(t *testing.T) {
	spec := CompleteTemplateContractSpec()
	if err := contracttest.ValidateAcceptanceSpec(spec); err != nil {
		t.Fatalf("ValidateAcceptanceSpec() error = %v", err)
	}
	required := []contracttest.CaseKey{
		contracttest.CaseEventMatrix,
		contracttest.CaseApproval,
		contracttest.CaseInterrupt,
		contracttest.CaseForceComplete,
		contracttest.CaseResume,
		contracttest.CaseToolbridge,
		contracttest.CaseRuntimeReport,
	}
	for _, key := range required {
		if _, ok := spec.RequiredCases[key]; !ok {
			t.Fatalf("template contract spec missing required case %s", key)
		}
	}
	if _, ok := spec.RequiredCases[contracttest.CasePromptParity]; ok {
		return
	}
	if _, ok := spec.RequiredCases[contracttest.CasePromptMaterializedCarrier]; ok {
		return
	}
	t.Fatalf("template contract spec missing prompt case: want %s or %s", contracttest.CasePromptParity, contracttest.CasePromptMaterializedCarrier)
}

func TestRenderedTemplateProductionOmissions(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*driverFactoryParams)
		omitModule renderedTemplateModuleOmission
		wantDirect string
		wantFx     string
		wantTyped  string
	}{
		{
			name: "runtime reporter",
			mutate: func(p *driverFactoryParams) {
				p.Reporter = nil
			},
			omitModule: omitRenderedTemplateRuntimeReporter,
			wantDirect: "provider.template.runtime_reporter",
			wantFx:     "contract.RuntimeReporter",
			wantTyped:  "provider.template.runtime_reporter",
		},
		{
			name: "toolbridge proxy",
			mutate: func(p *driverFactoryParams) {
				p.ToolbridgeProxy = nil
			},
			omitModule: omitRenderedTemplateToolbridgeProxy,
			wantDirect: "provider.template.toolbridge_proxy",
			wantFx:     "TemplateToolbridgeProxy",
			wantTyped:  "provider.template.toolbridge_proxy",
		},
		{
			name: "provider mirror",
			mutate: func(p *driverFactoryParams) {
				p.Mirror = nil
			},
			omitModule: omitRenderedTemplateMirror,
			wantDirect: "provider.template.mirror",
			wantFx:     "TemplateProviderMirror",
			wantTyped:  "provider.template.mirror",
		},
		{
			name: "session recovery",
			mutate: func(p *driverFactoryParams) {
				p.Recovery = nil
			},
			omitModule: omitRenderedTemplateRecovery,
			wantDirect: "provider.template.session_recovery",
			wantFx:     "TemplateSessionRecovery",
			wantTyped:  "provider.template.session_recovery",
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
			assertRenderedTemplateError(t, "direct NewDriverFactory", err, tc.wantDirect, tc.wantTyped)

			err = renderedTemplateFxError(params, tc.omitModule)
			assertRenderedTemplateError(t, "fx Module graph", err, tc.wantFx, "")
		})
	}
}

func TestRenderedTemplateModuleGraph(t *testing.T) {
	renderedTemplateEventTranslatorsRegistered = 0
	var drivers []contract.DriverFactory
	app := fx.New(
		renderedTemplateModuleOptions(completeRenderedTemplateParams(), ""),
		fx.Invoke(func(g renderedTemplateDriverGroup) {
			drivers = append(drivers, g.Drivers...)
		}),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("rendered Module graph error = %v", err)
	}
	if renderedTemplateEventTranslatorsRegistered == 0 {
		t.Fatal("rendered Module did not invoke RegisterEventTranslators")
	}
	if len(drivers) != 1 {
		t.Fatalf("rendered Module drivers len = %d, want 1", len(drivers))
	}
	if drivers[0].Name != "template" {
		t.Fatalf("rendered Module driver name = %q, want template", drivers[0].Name)
	}
	if len(drivers[0].NativeTools) == 0 {
		t.Fatal("rendered Module driver did not expose native tools")
	}
	if driver := drivers[0].Create(); driver == nil {
		t.Fatal("rendered Module driver factory Create() = nil")
	}
}

func TestRenderedTemplateAcceptanceCriteriaDeclared(t *testing.T) {
	spec := CompleteTemplateContractSpec()
	if err := contracttest.ValidateAcceptanceSpec(spec); err != nil {
		t.Fatalf("ValidateAcceptanceSpec() error = %v", err)
	}
	got := contracttest.RequiredAcceptanceCriteria(spec)
	want := []contracttest.AcceptanceCriterion{
		contracttest.AcceptanceEventTranslation,
		contracttest.AcceptanceApproval,
		contracttest.AcceptanceInterrupt,
		contracttest.AcceptanceForceComplete,
		contracttest.AcceptanceResume,
		contracttest.AcceptanceToolbridge,
		contracttest.AcceptanceRuntimeReport,
		contracttest.AcceptancePromptSnapshotParity,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("RequiredAcceptanceCriteria() = %v, want %v", got, want)
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

func renderedTemplateFxError(params driverFactoryParams, omission renderedTemplateModuleOmission) error {
	app := fx.New(
		renderedTemplateModuleOptions(params, omission),
		fx.Invoke(func(renderedTemplateDriverGroup) {}),
		fx.NopLogger,
	)
	return app.Err()
}

type renderedTemplateDriverGroup struct {
	fx.In

	Drivers []contract.DriverFactory ` + "`" + `group:"drivers"` + "`" + `
}

type renderedTemplateModuleOmission string

const (
	omitRenderedTemplateRuntimeReporter renderedTemplateModuleOmission = "runtime_reporter"
	omitRenderedTemplateToolbridgeProxy renderedTemplateModuleOmission = "toolbridge_proxy"
	omitRenderedTemplateMirror          renderedTemplateModuleOmission = "mirror"
	omitRenderedTemplateRecovery        renderedTemplateModuleOmission = "recovery"
)

func renderedTemplateModuleOptions(params driverFactoryParams, omission renderedTemplateModuleOmission) fx.Option {
	opts := []fx.Option{Module}
	if omission != omitRenderedTemplateRuntimeReporter {
		opts = append(opts, fx.Provide(func() contract.RuntimeReporter { return params.Reporter }))
	}
	if omission != omitRenderedTemplateToolbridgeProxy {
		opts = append(opts, fx.Provide(func() TemplateToolbridgeProxy { return params.ToolbridgeProxy }))
	}
	opts = append(opts, fx.Provide(func() TemplateApprovalBridge { return params.Approvals }))
	if omission != omitRenderedTemplateMirror {
		opts = append(opts, fx.Provide(func() TemplateProviderMirror { return params.Mirror }))
	}
	if omission != omitRenderedTemplateRecovery {
		opts = append(opts, fx.Provide(func() TemplateSessionRecovery { return params.Recovery }))
	}
	opts = append(opts,
		fx.Provide(func() TemplateTracer { return params.Tracer }),
		fx.Supply(params.Dependency),
	)
	return fx.Options(opts...)
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

func TestRenderedTemplateLSPDiagnosticsEvidenceDesign(t *testing.T) {
	evidence := strings.TrimSpace(os.Getenv("RENDERED_TEMPLATE_LSP_DIAGNOSTICS"))
	if evidence == "" {
		t.Skip("set RENDERED_TEMPLATE_LSP_DIAGNOSTICS to captured MCP LSP file(diagnostics) output for rendered module.go/provider_contract_test.go/template_stubs.go/template_omission_test.go")
	}
	if !strings.Contains(evidence, "diagnostics") {
		t.Fatalf("RENDERED_TEMPLATE_LSP_DIAGNOSTICS missing diagnostics payload: %s", evidence)
	}
	if strings.Contains(evidence, "\"severity\"") {
		t.Fatalf("rendered template LSP diagnostics are not clean: %s", evidence)
	}
}
`

const renderedTemplatePlaceholderProbeTests = `package template

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/provider/contracttest"
)

func TestRenderedTemplatePlaceholderFailures(t *testing.T) {
	cases := []struct {
		name string
		c    contracttest.Case
	}{
		{name: "event translation", c: templateEventTranslationContractCase()},
		{name: "event matrix", c: templateEventMatrixContractCase()},
		{name: "prompt", c: templatePromptParityContractCase()},
		{name: "approval", c: templateApprovalContractCase()},
		{name: "interrupt", c: templateInterruptContractCase()},
		{name: "force complete", c: templateForceCompleteContractCase()},
		{name: "resume", c: templateResumeIdentityContractCase()},
		{name: "toolbridge", c: templateToolbridgeContractCase()},
		{name: "runtime report", c: templateRuntimeReportContractCase()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.c.Run(t, contracttest.NewEvidence())
		})
	}
}
`
