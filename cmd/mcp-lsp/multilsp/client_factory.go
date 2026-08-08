package multilsp

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
)

// newClientFromFactory 按 resolved scope 的选项能力选择最精确的客户端工厂接口。
func newClientFromFactory(factory ClientFactory, cfg workspaceConfig, handler protocol.NotificationHandler) (Client, error) {
	if len(cfg.initOptions) > 0 {
		optionsFactory, ok := factory.(ClientFactoryWithOptions)
		if !ok {
			return nil, fmt.Errorf("client factory does not support resolved init options for %s", cfg.key)
		}
		return optionsFactory.NewClientWithOptions(
			cfg.rootPath,
			append([]string(nil), cfg.env...),
			cloneAnyMap(cfg.initOptions),
			handler,
		)
	}
	if len(cfg.env) > 0 {
		envFactory, ok := factory.(ClientFactoryWithEnv)
		if !ok {
			return nil, fmt.Errorf("client factory does not support environment overrides for %s", cfg.key)
		}
		return envFactory.NewClientWithEnv(cfg.rootPath, append([]string(nil), cfg.env...), handler)
	}
	return factory.NewClient(cfg.rootPath, handler)
}
