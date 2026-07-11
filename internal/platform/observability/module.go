package observability

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

// Module 注册 observability 配置解析和服务构造。
var Module = fx.Module("platform.observability",
	fx.Provide(
		ParseConfigFromEnv,
		NewServiceFromConfig,
	),
)

type serviceFromConfigParams struct {
	fx.In

	Lifecycle fx.Lifecycle
	AppConfig *config.Config
	Config    Config
}

// NewServiceFromConfig 根据运行配置创建 observability 服务并把 sink 关闭挂到 fx 生命周期。
func NewServiceFromConfig(p serviceFromConfigParams) (*Service, error) {
	if !p.Config.Enabled {
		return NewDisabledService(p.Config), nil
	}
	project, err := traceProjectFromAppConfig(p.AppConfig)
	if err != nil {
		return nil, err
	}
	sink, err := NewJSONLSink(project, p.Config)
	if err != nil {
		return nil, err
	}
	dir, err := TraceDirectory(project)
	if err != nil {
		_ = sink.Close()
		return nil, err
	}
	p.Lifecycle.Append(fx.StopHook(sink.Close))
	return NewService(p.Config, WithSink(sink), WithTailReader(NewTailReader(dir, p.Config))), nil
}

// traceProjectFromAppConfig 从项目根目录推导 trace 项目名。
// 配置缺失或根目录无效时直接报错，避免 trace 写入含糊目录。
func traceProjectFromAppConfig(cfg *config.Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("observability tracing requires platform config")
	}
	root := strings.TrimSpace(cfg.ProjectRoot)
	if root == "" {
		return "", fmt.Errorf("observability tracing requires non-empty project root")
	}
	project := filepath.Base(filepath.Clean(root))
	if strings.TrimSpace(project) == "" || project == "." || project == string(filepath.Separator) {
		return "", fmt.Errorf("observability tracing project name derived from %q is invalid", cfg.ProjectRoot)
	}
	return project, nil
}
