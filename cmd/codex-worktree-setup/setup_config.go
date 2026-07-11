package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	managedBegin = "# BEGIN SUPER-DOLPHIN MANAGED LSP"
	managedEnd   = "# END SUPER-DOLPHIN MANAGED LSP"
)

type managedDocument struct {
	MCPServers managedServers `toml:"mcp_servers"`
}

type managedServers struct {
	LSP managedLSP `toml:"lsp"`
}

type managedLSP struct {
	Type     string       `toml:"type"`
	Command  string       `toml:"command"`
	CWD      string       `toml:"cwd"`
	Required bool         `toml:"required"`
	Env      managedEnv   `toml:"env"`
	Tools    managedTools `toml:"tools"`
}

type managedEnv struct {
	ProjectRoot       string `toml:"PROJECT_ROOT"`
	RuntimeMode       string `toml:"SUPER_DOLPHIN_RUNTIME_MODE"`
	RuntimeResources  string `toml:"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"`
	DependencyProfile string `toml:"SUPER_DOLPHIN_DEPENDENCY_PROFILE"`
	LSPRoot           string `toml:"GO_AGENT_LSP_ROOT"`
	LSPRoots          string `toml:"GO_AGENT_LSP_ROOTS"`
	Path              string `toml:"PATH"`
}

type managedTools struct {
	File       toolApproval `toml:"file"`
	Inspect    toolApproval `toml:"inspect"`
	XRef       toolApproval `toml:"xref"`
	Grep       toolApproval `toml:"grep"`
	Structure  toolApproval `toml:"structure"`
	PatchEdit  toolApproval `toml:"patch_edit"`
	Completion toolApproval `toml:"completion"`
}

type toolApproval struct {
	ApprovalMode string `toml:"approval_mode"`
}

type decodedDocument struct {
	MCPServers map[string]decodedServer `toml:"mcp_servers"`
}

type decodedServer struct {
	Type     string                    `toml:"type"`
	Command  string                    `toml:"command"`
	CWD      string                    `toml:"cwd"`
	Required bool                      `toml:"required"`
	Env      map[string]string         `toml:"env"`
	Tools    map[string]decodedToolRef `toml:"tools"`
}

type decodedToolRef struct {
	ApprovalMode string `toml:"approval_mode"`
}

// configureProject 渲染并原子替换 project-local Codex 配置。
func configureProject(paths setupPaths, pathEnv string) error {
	existing, err := os.ReadFile(paths.Config)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read Codex config: %w", err)
	}
	rendered, err := renderConfig(existing, paths, pathEnv)
	if err != nil {
		return err
	}
	if bytes.Equal(existing, rendered) {
		return nil
	}
	return writeConfig(paths.Config, rendered)
}

// renderConfig 保留未受管字节，只替换文件末尾唯一的受管 LSP block。
func renderConfig(existing []byte, paths setupPaths, pathEnv string) ([]byte, error) {
	unmanaged, err := stripManagedBlock(existing)
	if err != nil {
		return nil, err
	}
	if err := validateUnmanagedConfig(unmanaged); err != nil {
		return nil, err
	}
	managed, err := encodeManagedConfig(paths, pathEnv)
	if err != nil {
		return nil, err
	}
	result := append([]byte(nil), unmanaged...)
	if len(result) > 0 && result[len(result)-1] != '\n' {
		result = append(result, '\n')
	}
	result = append(result, managedBegin...)
	result = append(result, '\n')
	result = append(result, managed...)
	result = append(result, managedEnd...)
	result = append(result, '\n')
	if _, err := toml.Decode(string(result), &decodedDocument{}); err != nil {
		return nil, fmt.Errorf("generated Codex config is invalid: %w", err)
	}
	return result, nil
}

// encodeManagedConfig 生成当前 worktree 独占的 LSP server 配置块。
func encodeManagedConfig(paths setupPaths, pathEnv string) ([]byte, error) {
	roots, err := json.Marshal([]string{paths.Worktree})
	if err != nil {
		return nil, fmt.Errorf("encode GO_AGENT_LSP_ROOTS: %w", err)
	}
	approve := toolApproval{ApprovalMode: "approve"}
	doc := managedDocument{MCPServers: managedServers{LSP: managedLSP{
		Type:     "stdio",
		Command:  paths.Binary,
		CWD:      paths.Worktree,
		Required: true,
		Env: managedEnv{
			ProjectRoot:       paths.Worktree,
			RuntimeMode:       "dev",
			RuntimeResources:  paths.Worktree,
			DependencyProfile: "production",
			LSPRoot:           paths.Worktree,
			LSPRoots:          string(roots),
			Path:              pathEnv,
		},
		Tools: managedTools{
			File: approve, Inspect: approve, XRef: approve, Grep: approve,
			Structure: approve, PatchEdit: approve, Completion: approve,
		},
	}}}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(doc); err != nil {
		return nil, fmt.Errorf("encode managed LSP config: %w", err)
	}
	return encoded.Bytes(), nil
}

// stripManagedBlock 只移除文件末尾成对且唯一的受管块，拒绝模糊所有权。
func stripManagedBlock(existing []byte) ([]byte, error) {
	text := string(existing)
	beginCount := strings.Count(text, managedBegin)
	endCount := strings.Count(text, managedEnd)
	if beginCount == 0 && endCount == 0 {
		return append([]byte(nil), existing...), nil
	}
	if beginCount != 1 || endCount != 1 {
		return nil, errors.New("managed LSP markers must be unique and paired")
	}
	begin := strings.Index(text, managedBegin)
	end := strings.Index(text, managedEnd)
	if begin < 0 || end < begin {
		return nil, errors.New("managed LSP markers are out of order")
	}
	after := end + len(managedEnd)
	if strings.TrimSpace(text[after:]) != "" {
		return nil, errors.New("managed LSP block must be at end of file")
	}
	if begin > 0 && text[begin-1] != '\n' {
		return nil, errors.New("managed LSP begin marker must start a line")
	}
	return append([]byte(nil), existing[:begin]...), nil
}

func validateUnmanagedConfig(raw []byte) error {
	if strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	var decoded map[string]any
	if _, err := toml.Decode(string(raw), &decoded); err != nil {
		return fmt.Errorf("existing Codex config is invalid: %w", err)
	}
	servers, ok := decoded["mcp_servers"].(map[string]any)
	if !ok {
		return nil
	}
	if _, exists := servers["lsp"]; exists {
		return errors.New("unmanaged mcp_servers.lsp configuration conflicts with managed LSP block")
	}
	return nil
}

// writeConfig 使用同目录临时文件、fsync 与平台原子替换写入配置。
func writeConfig(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Codex config directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config.toml.tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary Codex config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary config permissions: %w", err)
	}
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary Codex config: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary Codex config: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary Codex config: %w", err)
	}
	if err = atomicReplace(tmpPath, path); err != nil {
		return fmt.Errorf("replace Codex config: %w", err)
	}
	return nil
}

// validateDecodedConfig 校验受管配置的唯一所有权、worktree 绑定、环境与工具审批。
func validateDecodedConfig(raw []byte, paths setupPaths) (decodedServer, error) {
	server, err := decodeManagedServer(raw)
	if err != nil {
		return decodedServer{}, err
	}
	if err := validateServerIdentity(server, paths); err != nil {
		return decodedServer{}, err
	}
	if err := validateServerEnvironment(server, paths); err != nil {
		return decodedServer{}, err
	}
	if err := validateServerTools(server); err != nil {
		return decodedServer{}, err
	}
	return server, nil
}

func decodeManagedServer(raw []byte) (decodedServer, error) {
	if strings.Count(string(raw), managedBegin) != 1 || strings.Count(string(raw), managedEnd) != 1 {
		return decodedServer{}, errors.New("Codex config must contain exactly one managed LSP block")
	}
	var doc decodedDocument
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return decodedServer{}, fmt.Errorf("decode Codex config: %w", err)
	}
	server, ok := doc.MCPServers["lsp"]
	if !ok {
		return decodedServer{}, errors.New("Codex config is missing mcp_servers.lsp")
	}
	return server, nil
}

func validateServerIdentity(server decodedServer, paths setupPaths) error {
	if server.Type != "stdio" || server.Command != paths.Binary || server.CWD != paths.Worktree || !server.Required {
		return errors.New("Codex LSP server path, transport, cwd, or required flag does not match this worktree")
	}
	return nil
}

func validateServerEnvironment(server decodedServer, paths setupPaths) error {
	roots, err := json.Marshal([]string{paths.Worktree})
	if err != nil {
		return fmt.Errorf("encode expected GO_AGENT_LSP_ROOTS: %w", err)
	}
	expected := map[string]string{
		"PROJECT_ROOT":                        paths.Worktree,
		"SUPER_DOLPHIN_RUNTIME_MODE":          "dev",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR": paths.Worktree,
		"SUPER_DOLPHIN_DEPENDENCY_PROFILE":    "production",
		"GO_AGENT_LSP_ROOT":                   paths.Worktree,
		"GO_AGENT_LSP_ROOTS":                  string(roots),
	}
	for key, want := range expected {
		if server.Env[key] != want {
			return fmt.Errorf("Codex LSP env %s = %q, want %q", key, server.Env[key], want)
		}
	}
	if strings.TrimSpace(server.Env["PATH"]) == "" {
		return errors.New("Codex LSP env PATH is required")
	}
	return nil
}

func validateServerTools(server decodedServer) error {
	for _, name := range requiredLSPTools {
		if server.Tools[name].ApprovalMode != "approve" {
			return fmt.Errorf("Codex LSP tool %s approval_mode must be approve", name)
		}
	}
	return nil
}
