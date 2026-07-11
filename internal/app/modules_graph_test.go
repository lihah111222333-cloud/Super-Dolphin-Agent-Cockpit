package app

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	promptmodule "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	threadmodule "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/workflowtemplate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
)

// TestAppModuleGraphIsClosed 验证核心 app Module 的 fx.Provide 都能满足声明的 fx.In 依赖。
// fx.ValidateApp 只做 DAG dry-run，不执行构造函数，因此不会触发 db、pgxpool 或 toolbridge 副作用。
//
// 失败时 fx 会报告缺失类型和消费方，便于定位新增模块漏 provide 的问题。
func TestAppModuleGraphIsClosed(t *testing.T) {
	t.Parallel()

	if err := fx.ValidateApp(appGraphValidationOptions()...); err != nil {
		t.Fatalf("fx.ValidateApp failed: %v", err)
	}
}

func TestAppModuleGraphProvidesTurnThreadStateConfigReader(t *testing.T) {
	t.Parallel()

	var reader turn.ThreadStateConfigReader
	opts := append(appGraphValidationOptions(), fx.Populate(&reader))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing turn thread runtime reader: %v", err)
	}
}

// TestAppModuleGraphProvidesPromptStorePorts 验证 App graph 拥有 prompt 的三个持久化端口适配器。
func TestAppModuleGraphProvidesPromptStorePorts(t *testing.T) {
	t.Parallel()

	var store promptmodule.Store
	var preferences promptmodule.PreferenceReader
	var sharedFiles promptmodule.SharedFileReader
	opts := append(appGraphValidationOptions(), fx.Populate(&store, &preferences, &sharedFiles))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing prompt Store ports: %v", err)
	}
}

func TestAppModuleGraphProvidesMCPServerConfigProvider(t *testing.T) {
	t.Parallel()

	var provider contract.MCPServerConfigProvider
	opts := append(appGraphValidationOptions(), fx.Populate(&provider))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing MCP server config provider: %v", err)
	}
}

func TestAppModuleGraphProvidesToolbridgeMCPToolLifecycleBackfiller(t *testing.T) {
	t.Parallel()

	var backfiller toolbridge.MCPToolLifecycleBackfiller
	opts := append(appGraphValidationOptions(), fx.Populate(&backfiller))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing MCP tool lifecycle backfiller: %v", err)
	}
}

func TestAppModuleGraphProvidesToolbridgeMCPToolLifecyclePolicyReader(t *testing.T) {
	t.Parallel()

	var reader toolbridge.MCPToolLifecyclePolicyReader
	opts := append(appGraphValidationOptions(), fx.Populate(&reader))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing MCP tool lifecycle policy reader: %v", err)
	}
}

func TestAppModuleGraphProvidesDatasourceV2Service(t *testing.T) {
	t.Parallel()

	var svc datasourcev2.Service
	opts := append(appGraphValidationOptions(), fx.Populate(&svc))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing datasource_v2 service: %v", err)
	}
}

func TestAppModuleGraphProvidesWorkflowTemplateService(t *testing.T) {
	t.Parallel()

	var svc workflowtemplate.Service
	opts := append(appGraphValidationOptions(), fx.Populate(&svc))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing workflow template service: %v", err)
	}
}

func TestAppModuleGraphProvidesMCPControlSystemLogSink(t *testing.T) {
	t.Parallel()

	var sink mcpcontrol.SystemLogSink
	opts := append(appGraphValidationOptions(), fx.Populate(&sink))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing MCP control system log sink: %v", err)
	}
}

func TestAppModuleGraphProvidesThreadOrchestrationFacade(t *testing.T) {
	t.Parallel()

	var facade threadmodule.OrchestrationFacade
	opts := append(appGraphValidationOptions(), fx.Populate(&facade))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing thread orchestration facade: %v", err)
	}
}

func TestAppModuleGraphProvidesMemoryExtractionDrainer(t *testing.T) {
	t.Parallel()

	var drainer contract.MemoryExtractionDrainer
	opts := append(appGraphValidationOptions(), fx.Populate(&drainer))
	if err := fx.ValidateApp(opts...); err != nil {
		t.Fatalf("fx.ValidateApp missing memory extraction drainer: %v", err)
	}
}

func TestToolbridgeCodexProductionBindingRequiresCriticalDependencies(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts []fx.Option
		want string
	}{
		{
			name: "missing ServerManager",
			opts: []fx.Option{
				fx.Supply(newGraphTestCodexDriverFactory()),
				fx.Supply(toolbridge.NewHandlerForTesting(nil, nil)),
			},
			want: "*codexapp.ServerManager",
		},
		{
			name: "missing DriverFactory",
			opts: []fx.Option{
				fx.Supply(&codexapp.ServerManager{}),
				fx.Supply(toolbridge.NewHandlerForTesting(nil, nil)),
			},
			want: "*codexapp.DriverFactory",
		},
		{
			name: "missing toolbridge Handler",
			opts: []fx.Option{
				fx.Supply(&codexapp.ServerManager{}),
				fx.Supply(newGraphTestCodexDriverFactory()),
			},
			want: "*toolbridge.Handler",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opts := append([]fx.Option{
				toolbridgeCodexBindingModule(),
				fx.Provide(func() contract.SessionStarter { return graphTestSessionStarter{} }),
			}, tc.opts...)
			err := fx.ValidateApp(opts...)
			if err == nil {
				t.Fatalf("fx.ValidateApp() error = nil, want missing %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("fx.ValidateApp() error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestToolbridgeCodexProductionBindingWrapsSessionStarter(t *testing.T) {
	t.Parallel()

	inner := graphTestSessionStarter{}
	var starter contract.SessionStarter
	app := fx.New(
		toolbridgeCodexBindingModule(),
		fx.Provide(func() contract.SessionStarter { return inner }),
		fx.Supply(&codexapp.ServerManager{}),
		fx.Supply(newGraphTestCodexDriverFactory()),
		fx.Supply(toolbridge.NewHandlerForTesting(nil, nil)),
		fx.Populate(&starter),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	if starter == nil {
		t.Fatal("contract.SessionStarter = nil, want readiness wrapped starter")
	}
	if starter == contract.SessionStarter(inner) {
		t.Fatal("contract.SessionStarter was not wrapped with toolbridge readiness")
	}
}

func TestProviderScaffoldProductionGraphRequiresCriticalDependencies(t *testing.T) {
	cases := []struct {
		name      string
		module    fx.Option
		omissions []providerGraphOmission
		want      string
	}{
		{
			name:      "codexapp missing runtime reporter",
			module:    codexapp.Module,
			omissions: []providerGraphOmission{omitRuntimeReporter},
			want:      "contract.RuntimeReporter",
		},
		{
			name:      "codexapp missing provider mirror",
			module:    codexapp.Module,
			omissions: []providerGraphOmission{omitProviderMirror},
			want:      "contract.SkillMirrorReconciler",
		},
		{
			name:      "codexapp missing dependency profile",
			module:    codexapp.Module,
			omissions: []providerGraphOmission{omitDependencyConfig, omitContractConfig},
			want:      "codex dependency profile is required",
		},
		{
			name:      "claudecli missing runtime reporter",
			module:    claudecli.Module,
			omissions: []providerGraphOmission{omitRuntimeReporter},
			want:      "contract.RuntimeReporter",
		},
		{
			name:      "claudecli missing toolbridge proxy address",
			module:    claudecli.Module,
			omissions: []providerGraphOmission{omitProxyAddr},
			want:      "name=\"proxy_addr_fn\"",
		},
		{
			name:      "claudecli missing toolbridge proxy token",
			module:    claudecli.Module,
			omissions: []providerGraphOmission{omitProxyToken},
			want:      "name=\"proxy_token_fn\"",
		},
		{
			name:      "claudecli missing provider mirror",
			module:    claudecli.Module,
			omissions: []providerGraphOmission{omitProviderMirror},
			want:      "contract.SkillMirrorReconciler",
		},
		{
			name:      "claudecli missing dependency profile",
			module:    claudecli.Module,
			omissions: []providerGraphOmission{omitDependencyConfig, omitContractConfig},
			want:      "claude dependency profile is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := providerProductionGraphError(tc.module, tc.omissions...)
			if err == nil {
				t.Fatalf("provider graph error = nil, want missing %s", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("provider graph error = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestProviderScaffoldProductionGraphRequiresCriticalDependenciesCodexProvidesDriverAndAppServerPool(t *testing.T) {
	var drivers []contract.DriverFactory
	var manager *codexapp.ServerManager
	var pool *codexapp.ServerPool
	app := fx.New(
		providerProductionGraphOptions(codexapp.Module),
		fx.Populate(&manager, &pool),
		fx.Invoke(func(g providerGraphDriverGroup) {
			drivers = append(drivers, g.Drivers...)
		}),
		fx.NopLogger,
	)
	if err := app.Err(); err != nil {
		t.Fatalf("codexapp production graph error = %v", err)
	}
	if manager == nil {
		t.Fatal("codexapp production graph did not provide ServerManager")
	}
	if pool == nil {
		t.Fatal("codexapp production graph did not provide ServerPool")
	}
	if !providerGraphHasDriver(drivers, "codex") {
		t.Fatalf("codexapp production graph drivers = %#v, want codex", drivers)
	}
}

func TestProviderScaffoldProductionGraphRequiresCriticalDependenciesCodexRecoveryIsOptionalBoundary(t *testing.T) {
	drivers, err := providerProductionGraphDrivers(codexapp.Module, omitSessionRecovery)
	if err != nil {
		t.Fatalf("codexapp graph without recovery error = %v; Recovery is currently optional and must not be reported as a hard graph dependency without changing provider code", err)
	}
	if !providerGraphHasDriver(drivers, "codex") {
		t.Fatalf("codexapp production graph drivers = %#v, want codex", drivers)
	}
}

func TestProviderScaffoldProductionGraphRequiresCriticalDependenciesClaudeProvidesDriver(t *testing.T) {
	drivers, err := providerProductionGraphDrivers(claudecli.Module)
	if err != nil {
		t.Fatalf("claudecli production graph error = %v", err)
	}
	if !providerGraphHasDriver(drivers, "claude") {
		t.Fatalf("claudecli production graph drivers = %#v, want claude", drivers)
	}
}

func TestProviderScaffoldProductionGraphRequiresCriticalDependenciesClaudeRecoveryIsOptionalBoundary(t *testing.T) {
	drivers, err := providerProductionGraphDrivers(claudecli.Module, omitSessionRecovery)
	if err != nil {
		t.Fatalf("claudecli graph without recovery error = %v; Recovery is currently optional and must not be reported as a hard graph dependency without changing provider code", err)
	}
	if !providerGraphHasDriver(drivers, "claude") {
		t.Fatalf("claudecli production graph drivers = %#v, want claude", drivers)
	}
}

func providerProductionGraphError(module fx.Option, omissions ...providerGraphOmission) error {
	_, err := providerProductionGraphDrivers(module, omissions...)
	return err
}

func providerProductionGraphDrivers(module fx.Option, omissions ...providerGraphOmission) ([]contract.DriverFactory, error) {
	var drivers []contract.DriverFactory
	app := fx.New(
		providerProductionGraphOptions(module, omissions...),
		fx.Invoke(func(g providerGraphDriverGroup) {
			drivers = append(drivers, g.Drivers...)
		}),
		fx.NopLogger,
	)
	return drivers, app.Err()
}

type providerGraphDriverGroup struct {
	fx.In

	Drivers []contract.DriverFactory `group:"drivers"`
}

func providerGraphHasDriver(drivers []contract.DriverFactory, name string) bool {
	for _, driver := range drivers {
		if driver.Name == name {
			return true
		}
	}
	return false
}

type providerGraphOmission string

const (
	omitRuntimeReporter  providerGraphOmission = "runtime_reporter"
	omitProviderMirror   providerGraphOmission = "provider_mirror"
	omitSessionRecovery  providerGraphOmission = "session_recovery"
	omitDependencyConfig providerGraphOmission = "dependency_config"
	omitContractConfig   providerGraphOmission = "contract_config"
	omitProxyAddr        providerGraphOmission = "proxy_addr"
	omitProxyToken       providerGraphOmission = "proxy_token"
)

func providerProductionGraphOptions(module fx.Option, omissions ...providerGraphOmission) fx.Option {
	omitted := providerGraphOmissionSet(omissions...)
	opts := []fx.Option{
		module,
		fx.Supply(slog.Default()),
		fx.Provide(func() *event.Dispatcher { return event.NewDispatcher() }),
		fx.Provide(unified.NewEventDispatcher),
		fx.Provide(rpc.NewApprovalManager),
		fx.Provide(pidregistry.New),
	}
	opts = appendProviderGraphCoreDependencies(opts, omitted)
	opts = appendProviderGraphProxyDependencies(opts, omitted)
	return fx.Options(opts...)
}

func appendProviderGraphCoreDependencies(opts []fx.Option, omitted map[providerGraphOmission]bool) []fx.Option {
	if !omitted[omitRuntimeReporter] {
		opts = append(opts, fx.Provide(func() contract.RuntimeReporter { return graphTestRuntimeReporter{} }))
	}
	if !omitted[omitProviderMirror] {
		opts = append(opts, fx.Provide(func() contract.SkillMirrorReconciler { return graphTestSkillMirror{} }))
	}
	if !omitted[omitSessionRecovery] {
		opts = append(opts, fx.Provide(func() contract.SessionRecoveryReporter { return graphTestSessionRecovery{} }))
	}
	if !omitted[omitDependencyConfig] {
		opts = append(opts, fx.Supply(graphTestProductionDependencyConfig()))
	}
	if !omitted[omitContractConfig] {
		opts = append(opts, fx.Supply(&contract.Config{
			ProjectRoot: os.TempDir(),
			Dependency:  graphTestProductionDependencyConfig(),
		}))
	}
	return opts
}

func appendProviderGraphProxyDependencies(opts []fx.Option, omitted map[providerGraphOmission]bool) []fx.Option {
	if !omitted[omitProxyAddr] {
		opts = append(opts, fx.Provide(fx.Annotate(func() func() string {
			return func() string { return "127.0.0.1:0" }
		}, fx.ResultTags(`name:"proxy_addr_fn"`))))
	}
	if !omitted[omitProxyToken] {
		opts = append(opts, fx.Provide(fx.Annotate(func() func() string {
			return func() string { return "graph-test-token" }
		}, fx.ResultTags(`name:"proxy_token_fn"`))))
	}
	return opts
}

func providerGraphOmissionSet(omissions ...providerGraphOmission) map[providerGraphOmission]bool {
	omitted := make(map[providerGraphOmission]bool, len(omissions))
	for _, omission := range omissions {
		omitted[omission] = true
	}
	return omitted
}

func graphTestProductionDependencyConfig() contract.DependencyConfig {
	return contract.DependencyConfig{Profile: contract.DependencyProfileProduction}
}

type graphTestRuntimeReporter struct{}

func (graphTestRuntimeReporter) ReportRuntime(context.Context, contract.RuntimeReport) error {
	return nil
}

type graphTestSkillMirror struct{}

func (graphTestSkillMirror) ReconcileProviderMirrors(context.Context, string, []contract.SkillProviderMirrorTarget) (contract.SkillMirrorReport, error) {
	return contract.SkillMirrorReport{}, nil
}

type graphTestSessionRecovery struct{}

func (graphTestSessionRecovery) ClearStaleProviderThreadID(context.Context, string) error {
	return nil
}

func (graphTestSessionRecovery) RecordProviderSessionUUID(context.Context, string, string) error {
	return nil
}

func appGraphValidationOptions() []fx.Option {
	// RunDesktop 正常会注入前端文件系统；这里用空 fs 满足 uiwails.Module 依赖，避免启动 Wails。
	frontend := fx.Supply(uiwails.FrontendFS{FS: emptyFS{}})

	var mcpProvider contract.MCPServerConfigProvider
	var threadReader contract.ThreadStateConfigReader

	return []fx.Option{
		Module,
		uiwails.Module,
		frontend,
		fx.Populate(&mcpProvider, &threadReader),
		fx.Invoke(BindRuntime),
	}
}

// emptyFS 是测试用空前端文件系统，任何文件请求都按不存在处理。
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

type graphTestSessionStarter struct{}

func (graphTestSessionStarter) StartSession(context.Context, dto.StartSessionRequest) (contract.Session, error) {
	return nil, nil
}

func (graphTestSessionStarter) ResumeSession(context.Context, dto.ResumeSessionRequest) (contract.Session, error) {
	return nil, nil
}

func newGraphTestCodexDriverFactory() *codexapp.DriverFactory {
	return codexapp.NewDriverFactory(nil, nil, nil, nil, nil, nil, nil, nil)
}
