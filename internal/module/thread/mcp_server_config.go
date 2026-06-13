package thread

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

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
	if conflict := firstMCPServerNameConflict(snapshot.Servers, names); conflict != "" {
		return contract.MCPSnapshot{}, fmt.Errorf("configured mcp server conflicts with active server: %s", conflict)
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

func firstMCPServerNameConflict(existing, additions []string) string {
	if len(existing) == 0 || len(additions) == 0 {
		return ""
	}
	seen := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		name = strings.TrimSpace(name)
		if name != "" {
			seen[name] = struct{}{}
		}
	}
	for _, name := range additions {
		if _, ok := seen[strings.TrimSpace(name)]; ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
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
