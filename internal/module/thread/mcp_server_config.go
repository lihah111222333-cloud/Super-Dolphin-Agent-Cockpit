package thread

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
	for _, name := range names {
		config, err := normalizePromptMCPServerConfig(name, input[rawNames[name]])
		if err != nil {
			return nil, nil, err
		}
		out[name] = config
	}
	return out, names, nil
}

func normalizePromptMCPServerConfig(name string, config contract.MCPServerConfig) (contract.MCPServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server transport is required: %s", name)
	}
	if !strings.EqualFold(transport, "http") {
		return contract.MCPServerConfig{}, fmt.Errorf("configured mcp server transport is unsupported: %s", transport)
	}
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
		if promptMCPServerNameIsActive(active, name) {
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
		headers := make(map[string]string, len(config.Headers))
		for header, value := range config.Headers {
			header = strings.TrimSpace(header)
			value = strings.TrimSpace(value)
			if header != "" && value != "" {
				headers[header] = value
			}
		}
		if len(headers) == 0 {
			headers = nil
		}
		out[name] = contract.MCPServerConfig{
			Transport: strings.TrimSpace(config.Transport),
			URL:       strings.TrimSpace(config.URL),
			Headers:   headers,
		}
	}
}
