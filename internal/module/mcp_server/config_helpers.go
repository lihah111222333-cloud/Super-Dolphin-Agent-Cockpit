package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// readMCPServerConfig 读取本地 MCP server 配置文件，文件不存在时返回空配置。
func readMCPServerConfig(configPath string) (ConfigDocument, error) {
	raw, err := os.ReadFile(configPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return ConfigDocument{MCPServers: map[string]ServerConfig{}}, nil
	case err != nil:
		return ConfigDocument{}, fmt.Errorf("read mcp server config: %w", err)
	}
	var doc ConfigDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ConfigDocument{}, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
	}
	if doc.MCPServers == nil {
		return ConfigDocument{}, errInvalidConfigDocument
	}
	for name, config := range doc.MCPServers {
		normalized, err := normalizeServerConfig(name, config, "")
		if err != nil {
			return ConfigDocument{}, fmt.Errorf("%w: %v", errInvalidConfigDocument, err)
		}
		doc.MCPServers[name] = normalized
	}
	return doc, nil
}

func writeMCPServerConfig(configPath string, doc ConfigDocument) error {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal mcp server config: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create mcp server config dir: %w", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		return fmt.Errorf("write mcp server config: %w", err)
	}
	return nil
}

func cloneMCPServers(input map[string]ServerConfig) map[string]ServerConfig {
	out := make(map[string]ServerConfig, len(input))
	for name, config := range input {
		config.Headers = cloneStringMap(config.Headers)
		config.Args = cloneStringList(config.Args)
		config.Env = cloneStringMap(config.Env)
		config.Enabled = cloneBoolPtr(config.Enabled)
		out[name] = config
	}
	return out
}

func cloneStringList(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	return append([]string(nil), input...)
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneBoolPtr(input *bool) *bool {
	if input == nil {
		return nil
	}
	return boolPtr(*input)
}

// mcpServersToContract 把内部 server 配置复制成跨模块契约，避免调用方修改内部缓存。
func mcpServersToContract(input map[string]ServerConfig) map[string]contract.MCPServerConfig {
	out := make(map[string]contract.MCPServerConfig, len(input))
	for name, config := range input {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		config = migrateLegacySQLiteServerConfigForContract(name, config)
		out[name] = contract.MCPServerConfig{
			Transport: strings.TrimSpace(config.Transport),
			URL:       strings.TrimSpace(config.URL),
			Headers:   cloneStringMap(config.Headers),
			Command:   strings.TrimSpace(config.Command),
			Args:      cloneStringList(config.Args),
			Env:       cloneStringMap(config.Env),
			Enabled:   cloneBoolPtr(config.Enabled),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func enabledMCPServersToContract(input map[string]ServerConfig) map[string]contract.MCPServerConfig {
	if len(input) == 0 {
		return nil
	}
	filtered := make(map[string]ServerConfig, len(input))
	for name, config := range input {
		if !mcpServerConfigEnabled(config) {
			continue
		}
		filtered[name] = config
	}
	return mcpServersToContract(filtered)
}

func migrateLegacySQLiteServerConfigForContract(name string, config ServerConfig) ServerConfig {
	if name != DefaultSQLiteServerName || !isLegacyDefaultSQLiteServerConfig(config) {
		return config
	}
	databasePath := legacySQLiteDatabasePath(config.Args)
	if databasePath == "" {
		return config
	}
	return defaultSQLiteServerConfig(databasePath)
}

// resolveMCPServerConfigBaseDir 从当前目录向上查找已有配置，找不到时使用传入工作目录。
func resolveMCPServerConfigBaseDir(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return os.Getwd()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	for dir := filepath.Clean(cwd); dir != ""; dir = filepath.Dir(dir) {
		configPath := mcpServerConfigPath(dir)
		if _, err := os.Stat(configPath); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat mcp server config: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return filepath.Clean(cwd), nil
}

func mcpServerConfigPath(workingDir string) string {
	return filepath.Join(workingDir, ".agent", "mcp_server", "config.json")
}

func currentMCPServerWorkspaceRoot() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return filepath.Clean(workingDir), nil
}

func normalizeMCPServerWorkspaceRoot(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return currentMCPServerWorkspaceRoot()
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return filepath.Clean(cwd), nil
}

func mcpServerWorkspaceCandidates(workspaceRoot string) []string {
	workspaceRoot = filepath.Clean(workspaceRoot)
	candidates := make([]string, 0, 4)
	for dir := workspaceRoot; dir != ""; dir = filepath.Dir(dir) {
		candidates = append(candidates, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return candidates
}
