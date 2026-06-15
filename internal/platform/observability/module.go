package observability

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

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

// NewServiceFromConfig 从配置创建服务。
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

// traceProjectFromAppConfig 从app配置处理trace项目。
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
