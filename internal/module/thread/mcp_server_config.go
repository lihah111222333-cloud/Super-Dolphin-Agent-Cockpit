package thread

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// mergeConfiguredMCPServers 把项目持久化 MCP server 配置合并进 prompt 快照。
func mergeConfiguredMCPServers(ctx context.Context, snapshot contract.MCPSnapshot, provider contract.MCPServerConfigProvider, cwd string) (contract.MCPSnapshot, error) {
	if provider == nil {
		return snapshot, nil
	}
	configs, err := provider.ListMCPServerConfigs(ctx, cwd)
	if err != nil {
		return contract.MCPSnapshot{}, fmt.Errorf("list configured mcp servers: %w", err)
	}
	configs, names, err := normalizePromptMCPServerConfigs(configs)
	if err != nil {
		return contract.MCPSnapshot{}, err
	}
	if len(configs) == 0 {
		return snapshot, nil
	}
	configs, names = skipPromptConfiguredMCPServersWithActiveNames(snapshot.Servers, configs, names)
	if len(configs) == 0 {
		return snapshot, nil
	}
	snapshot.Servers = uniquePromptStrings(snapshot.Servers, names)
	snapshot.ServerConfigs = mergeMCPServerConfigMaps(snapshot.ServerConfigs, configs)
	return snapshot, nil
}

func mcpServerConfigLookupRoot(buildCtx contract.BuildCtx) string {
	if root := strings.TrimSpace(buildCtx.GitRoot); root != "" {
		return root
	}
	return strings.TrimSpace(buildCtx.CWD)
}

// normalizePromptMCPServerConfigs 规范化 prompt MCP server 配置并返回稳定排序后的名称。
func normalizePromptMCPServerConfigs(input map[string]contract.MCPServerConfig) (map[string]contract.MCPServerConfig, []string, error) {
	if len(input) == 0 {
		return nil, nil, nil
	}
	names := make([]string, 0, len(input))
	rawNames := make(map[string]string, len(input))
	for rawName := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, nil, errors.New("configured mcp server name is required")
		}
		if _, exists := rawNames[name]; exists {
			return nil, nil, fmt.Errorf("configured mcp server name is duplicated after trimming: %s", name)
		}
		names = append(names, name)
		rawNames[name] = rawName
	}
	sort.Strings(names)
	out := make(map[string]contract.MCPServerConfig, len(names))
	enabledNames := make([]string, 0, len(names))
	for _, name := range names {
		config, err := normalizePromptMCPServerConfig(name, input[rawNames[name]])
		if err != nil {
			return nil, nil, err
		}
		if config.Enabled != nil && !*config.Enabled {
			continue
		}
		out[name] = config
		enabledNames = append(enabledNames, name)
	}
	return out, enabledNames, nil
}

func normalizePromptMCPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server transport is required: %s", name)
	}
	switch strings.ToLower(transport) {
	case "http":
		return normalizePromptHTTPMCPServerConfig(name, config)
	case "stdio":
		return normalizePromptStdioMCPServerConfig(name, config)
	default:
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server transport is unsupported: %s", transport)
	}
}

func normalizePromptHTTPMCPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server url is required: %s", name)
	}
	headers, err := normalizePromptMCPHeaders(name, config.Headers)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	return contract.MCPServerConfig{
		Transport: "http",
		URL:       rawURL,
		Headers:   headers,
		Enabled:   config.Enabled,
	}, nil
}

func normalizePromptStdioMCPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	command := strings.TrimSpace(config.Command)
	if command == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server command is required: %s", name)
	}
	args, err := normalizePromptMCPArgs(name, config.Args)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	env, err := normalizePromptMCPEnv(name, config.Env)
	if err != nil {
		return contract.MCPServerConfig{}, err
	}
	return contract.MCPServerConfig{
		Transport: "stdio",
		Command:   command,
		Args:      args,
		Env:       env,
		Enabled:   config.Enabled,
	}, nil
}

func normalizePromptMCPHeaders(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("configured mcp server header name is required: %s", serverName)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("configured mcp server header value is required: %s.%s", serverName, name)
		}
		out[name] = value
	}
	return out, nil
}

func normalizePromptMCPArgs(serverName string, input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(input))
	for _, rawValue := range input {
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("configured mcp server arg is required: %s", serverName)
		}
		out = append(out, value)
	}
	return out, nil
}

func normalizePromptMCPEnv(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("configured mcp server env name is required: %s", serverName)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("configured mcp server env value is required: %s.%s", serverName, name)
		}
		out[name] = value
	}
	return out, nil
}

// skipPromptConfiguredMCPServersWithActiveNames 跳过已经在线的 MCP server。
// 在线实例由运行态注册表负责，项目配置只补充尚未在线的 HTTP server。
func skipPromptConfiguredMCPServersWithActiveNames(existing []string, configs map[string]contract.MCPServerConfig, names []string) (map[string]contract.MCPServerConfig, []string) {
	if len(configs) == 0 || len(names) == 0 {
		return configs, names
	}
	active := promptMCPServerNameSet(existing)
	if len(active) == 0 {
		return configs, names
	}
	filteredConfigs := make(map[string]contract.MCPServerConfig, len(configs))
	filteredNames := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if promptMCPServerNameIsActive(active, name) && strings.EqualFold(strings.TrimSpace(configs[name].Transport), "http") {
			continue
		}
		filteredConfigs[name] = configs[name]
		filteredNames = append(filteredNames, name)
	}
	return promptConfiguredMCPFilterResult(filteredConfigs, filteredNames)
}

func promptMCPServerNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func promptMCPServerNameIsActive(active map[string]struct{}, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	_, ok := active[name]
	return ok
}

func promptConfiguredMCPFilterResult(configs map[string]contract.MCPServerConfig, names []string) (map[string]contract.MCPServerConfig, []string) {
	if len(configs) == 0 {
		return nil, nil
	}
	return configs, names
}

func mergeMCPServerConfigMaps(base, extra map[string]contract.MCPServerConfig) map[string]contract.MCPServerConfig {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]contract.MCPServerConfig, len(base)+len(extra))
	copyMCPServerConfigs(out, base)
	copyMCPServerConfigs(out, extra)
	if len(out) == 0 {
		return nil
	}
	return out
}

// copyMCPServerConfigs 复制 MCP server 配置，顺手清理空白名称和空 header。
func copyMCPServerConfigs(out map[string]contract.MCPServerConfig, input map[string]contract.MCPServerConfig) {
	for name, config := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out[name] = contract.MCPServerConfig{
			Transport: strings.TrimSpace(config.Transport),
			URL:       strings.TrimSpace(config.URL),
			Headers:   clonePromptMCPStringMap(config.Headers),
			Command:   strings.TrimSpace(config.Command),
			Args:      clonePromptMCPStringList(config.Args),
			Env:       clonePromptMCPStringMap(config.Env),
			Enabled:   config.Enabled,
		}
	}
}

func clonePromptMCPStringList(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	for _, value := range input {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// clonePromptMCPStringMap 复制并清理 prompt 快照里的 MCP 字符串 map。
// 空 key/value 会被丢弃，避免 provider 侧收到不可用配置。
func clonePromptMCPStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
