package app

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	threadmodule "github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/workflowtemplate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
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
	t.Parallel()

	cases := []struct {
		name       string
		path       string
		structName string
		fields     []string
	}{
		{
			name:       "codexapp driver factory params",
			path:       "../provider/codexapp/module.go",
			structName: "DriverFactoryParams",
			fields:     []string{"Reporter", "Dependency", "Manager", "Pool", "Mirror"},
		},
		{
			name:       "claudecli driver factory params",
			path:       "../provider/claudecli/module.go",
			structName: "driverFactoryParams",
			fields:     []string{"Reporter", "Dependency", "Reg", "ProxyAddrFn", "ProxyTokenFn", "Mirror"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fields := parseGraphTestStructFields(t, tc.path, tc.structName)
			for _, field := range tc.fields {
				if !fields[field] {
					t.Fatalf("%s missing production dependency field %s", tc.structName, field)
				}
			}
		})
	}
}

func parseGraphTestStructFields(t *testing.T, path, structName string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			st, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s is %T, want struct", structName, typeSpec.Type)
			}
			return graphTestStructFieldSet(st)
		}
	}
	t.Fatalf("%s not found in %s", structName, path)
	return nil
}

func graphTestStructFieldSet(st *ast.StructType) map[string]bool {
	fields := map[string]bool{}
	for _, field := range st.Fields.List {
		for _, name := range field.Names {
			fields[name.Name] = true
		}
	}
	return fields
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
