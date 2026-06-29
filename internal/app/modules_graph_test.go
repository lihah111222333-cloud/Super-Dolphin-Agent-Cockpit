package app

import (
	"io/fs"
	"testing"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	datasourcev2 "github.com/anthropic-ai/super-agent-v3/internal/module/datasource_v2"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/workflowtemplate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/toolbridge"
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
