package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	errMissingMCPServers      = errors.New("mcp_server: mcpServers is required")
	errMissingServerName      = errors.New("mcp_server: server name is required")
	errDuplicateServerName    = errors.New("mcp_server: duplicate server name")
	errMissingServerTransport = errors.New("mcp_server: transport is required")
	errUnsupportedTransport   = errors.New("mcp_server: unsupported transport")
	errMissingServerURL       = errors.New("mcp_server: url is required")
	errInvalidServerURL       = errors.New("mcp_server: invalid url")
	errMissingHeaderName      = errors.New("mcp_server: header name is required")
	errMissingHeaderValue     = errors.New("mcp_server: header value is required")
	errInvalidConfigDocument  = errors.New("mcp_server: invalid config document")
	errServerAlreadyExists    = errors.New("mcp_server: server already exists")
)

type Service interface {
	AddServers(context.Context, AddServersRequest) (AddServersResult, error)
	ListServers(context.Context) (ListServersResult, error)
}

type ConfigDocument struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type AddServersRequest struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type ServerConfig struct {
	Transport string            `json:"transport"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
}

type AddServersResult struct {
	ConfigPath  string   `json:"configPath"`
	ServerNames []string `json:"serverNames"`
}

type ListServersResult struct {
	ConfigPath string                  `json:"configPath"`
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type service struct{}

func NewService() Service {
	return &service{}
}

func (s *service) AddServers(ctx context.Context, req AddServersRequest) (AddServersResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AddServersResult{}, err
	}
	additions, names, err := normalizeMCPServers(req.MCPServers)
	if err != nil {
		return AddServersResult{}, err
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return AddServersResult{}, fmt.Errorf("get working directory: %w", err)
	}
	configPath := mcpServerConfigPath(workingDir)
	doc, err := readMCPServerConfig(configPath)
	if err != nil {
		return AddServersResult{}, err
	}
	for _, name := range names {
		if _, ok := doc.MCPServers[name]; ok {
			return AddServersResult{}, fmt.Errorf("%w: %s", errServerAlreadyExists, name)
		}
	}
	for _, name := range names {
		doc.MCPServers[name] = additions[name]
	}
	if err := writeMCPServerConfig(configPath, doc); err != nil {
		return AddServersResult{}, err
	}

	return AddServersResult{ConfigPath: configPath, ServerNames: names}, nil
}

func (s *service) ListServers(ctx context.Context) (ListServersResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ListServersResult{}, err
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return ListServersResult{}, fmt.Errorf("get working directory: %w", err)
	}
	configPath := mcpServerConfigPath(workingDir)
	doc, err := readMCPServerConfig(configPath)
	if err != nil {
		return ListServersResult{}, err
	}
	return ListServersResult{
		ConfigPath: configPath,
		MCPServers: cloneMCPServers(doc.MCPServers),
	}, nil
}

func normalizeMCPServers(input map[string]ServerConfig) (map[string]ServerConfig, []string, error) {
	if len(input) == 0 {
		return nil, nil, errMissingMCPServers
	}
	servers := make(map[string]ServerConfig, len(input))
	names := make([]string, 0, len(input))
	for rawName, rawConfig := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, nil, errMissingServerName
		}
		if name != rawName {
			return nil, nil, fmt.Errorf("%w: %q", errMissingServerName, rawName)
		}
		if _, ok := servers[name]; ok {
			return nil, nil, fmt.Errorf("%w: %s", errDuplicateServerName, name)
		}
		config, err := normalizeServerConfig(name, rawConfig)
		if err != nil {
			return nil, nil, err
		}
		servers[name] = config
		names = append(names, name)
	}
	sort.Strings(names)
	return servers, names, nil
}

func normalizeServerConfig(name string, config ServerConfig) (ServerConfig, error) {
	transport := strings.TrimSpace(config.Transport)
	if transport == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerTransport, name)
	}
	if transport != "http" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errUnsupportedTransport, transport)
	}
	rawURL := strings.TrimSpace(config.URL)
	if rawURL == "" {
		return ServerConfig{}, fmt.Errorf("%w: %s", errMissingServerURL, name)
	}
	if err := validateHTTPURL(rawURL); err != nil {
		return ServerConfig{}, fmt.Errorf("%w: %s", err, rawURL)
	}

	headers, err := normalizeHeaders(name, config.Headers)
	if err != nil {
		return ServerConfig{}, err
	}
	return ServerConfig{
		Transport: transport,
		URL:       rawURL,
		Headers:   headers,
	}, nil
}

func normalizeHeaders(serverName string, input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	headers := make(map[string]string, len(input))
	for rawName, rawValue := range input {
		name := strings.TrimSpace(rawName)
		if name == "" {
			return nil, fmt.Errorf("%w: %s", errMissingHeaderName, serverName)
		}
		value := strings.TrimSpace(rawValue)
		if value == "" {
			return nil, fmt.Errorf("%w: %s.%s", errMissingHeaderValue, serverName, name)
		}
		headers[name] = value
	}
	return headers, nil
}

func validateHTTPURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errInvalidServerURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errInvalidServerURL
	}
	if parsed.Host == "" {
		return errInvalidServerURL
	}
	return nil
}

func readMCPServerConfig(configPath string) (ConfigDocument, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ConfigDocument{MCPServers: map[string]ServerConfig{}}, nil
		}
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
		normalized, err := normalizeServerConfig(name, config)
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
		headers := make(map[string]string, len(config.Headers))
		for header, value := range config.Headers {
			headers[header] = value
		}
		if len(headers) == 0 {
			headers = nil
		}
		config.Headers = headers
		out[name] = config
	}
	return out
}

func mcpServerConfigPath(workingDir string) string {
	return filepath.Join(workingDir, ".agent", "mcp_server", "config.json")
}
