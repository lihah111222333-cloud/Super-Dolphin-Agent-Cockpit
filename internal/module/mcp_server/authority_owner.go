package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

type mcpToolAuthorityState struct {
	token      contract.MCPToolAuthority
	quarantine map[string]string
}

type mcpToolAuthorityOwner struct {
	svc     *service
	mu      sync.Mutex
	current map[string]mcpToolAuthorityState
}

// AsMCPToolAuthorityOwner 创建由 config owner 持有的 generation/quarantine 状态端口。
func AsMCPToolAuthorityOwner(svc Service) contract.MCPToolAuthorityOwner {
	owner, _ := svc.(*service)
	return &mcpToolAuthorityOwner{svc: owner, current: make(map[string]mcpToolAuthorityState)}
}

// IssueMCPToolAuthority 复核当前 config 后签发新的单调 generation。
func (o *mcpToolAuthorityOwner) IssueMCPToolAuthority(
	ctx context.Context,
	req contract.MCPToolAuthorityIssueRequest,
) (contract.MCPToolAuthority, error) {
	if o == nil || o.svc == nil {
		return contract.MCPToolAuthority{}, errors.New("mcp_server: authority owner is not configured")
	}
	if strings.TrimSpace(req.CWD) == "" || strings.TrimSpace(req.MembershipDigest) == "" {
		return contract.MCPToolAuthority{}, errors.New("mcp_server: authority cwd and membership digest are required")
	}
	o.svc.configMu.Lock()
	defer o.svc.configMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	token, err := o.resolveMCPToolAuthority(ctx, req.CWD, req.Binary)
	if err != nil {
		return contract.MCPToolAuthority{}, err
	}
	token.MembershipDigest = req.MembershipDigest
	key := mcpToolAuthorityKey(token)
	token.Generation = o.current[key].token.Generation + 1
	o.current[key] = mcpToolAuthorityState{token: token}
	return token, nil
}

// CheckMCPToolAuthority 复核 generation、membership 和 config digest 仍为 owner current。
func (o *mcpToolAuthorityOwner) CheckMCPToolAuthority(
	ctx context.Context,
	token contract.MCPToolAuthority,
) error {
	if o == nil || o.svc == nil {
		return errors.New("mcp_server: authority owner is not configured")
	}
	o.svc.configMu.Lock()
	defer o.svc.configMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.checkMCPToolAuthorityLocked(ctx, token)
}

// WithMCPToolAuthority 在受保护副作用完成前持续持有配置 revision lease。
func (o *mcpToolAuthorityOwner) WithMCPToolAuthority(
	ctx context.Context,
	token contract.MCPToolAuthority,
	call func() error,
) error {
	if o == nil || o.svc == nil {
		return errors.New("mcp_server: authority owner is not configured")
	}
	if call == nil {
		return errors.New("mcp_server: authority call callback is required")
	}
	o.svc.configMu.Lock()
	defer o.svc.configMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.checkMCPToolAuthorityLocked(ctx, token); err != nil {
		return err
	}
	return call()
}

// CompareAndSwapMCPToolQuarantines 在同一 current-CAS 内提交全部 quarantine 与 surface publish。
func (o *mcpToolAuthorityOwner) CompareAndSwapMCPToolQuarantines(
	ctx context.Context,
	commits []contract.MCPToolQuarantineCommit,
	publish func() error,
) error {
	if publish == nil {
		return errors.New("mcp_server: authority publish callback is required")
	}
	if o == nil || o.svc == nil {
		return errors.New("mcp_server: authority owner is not configured")
	}
	o.svc.configMu.Lock()
	defer o.svc.configMu.Unlock()
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, commit := range commits {
		if err := o.checkMCPToolAuthorityLocked(ctx, commit.Authority); err != nil {
			return err
		}
	}
	if err := publish(); err != nil {
		return err
	}
	for _, commit := range commits {
		key := mcpToolAuthorityKey(commit.Authority)
		state := o.current[key]
		state.quarantine = cloneMCPToolQuarantine(commit.Tools)
		o.current[key] = state
	}
	return nil
}

// checkMCPToolAuthorityLocked 在 owner 锁内复核 generation 与当前配置摘要。
func (o *mcpToolAuthorityOwner) checkMCPToolAuthorityLocked(
	ctx context.Context,
	token contract.MCPToolAuthority,
) error {
	current, ok := o.current[mcpToolAuthorityKey(token)]
	if !ok || current.token != token {
		return errors.New("mcp_server: MCP tool authority generation is stale")
	}
	if token.Managed {
		return nil
	}
	if token.ConfigRevision != o.svc.configRevision {
		return errors.New("mcp_server: MCP tool authority config revision is stale")
	}
	result, err := o.svc.ListServersForCWD(ctx, token.CWD)
	if err != nil {
		return fmt.Errorf("mcp_server: refresh authority config: %w", err)
	}
	config, ok := enabledMCPServersToContract(result.MCPServers)[token.ServerID]
	if !ok || config.TrustedServerID != token.ServerID {
		return errors.New("mcp_server: MCP tool authority config is stale")
	}
	digest, err := digestMCPToolAuthority(config)
	if err != nil {
		return err
	}
	if digest != token.ConfigDigest {
		return errors.New("mcp_server: MCP tool authority config is stale")
	}
	return nil
}

// resolveMCPToolAuthority 只从 built-in manifest owner 或配置 owner 签发 authority。
func (o *mcpToolAuthorityOwner) resolveMCPToolAuthority(
	ctx context.Context,
	cwd string,
	binary providerdto.MCPBinary,
) (contract.MCPToolAuthority, error) {
	name := strings.TrimSpace(binary.Name)
	if name == "" || name != binary.Name {
		return contract.MCPToolAuthority{}, errors.New("mcp_server: authority server name is required")
	}
	if contract.IsManagedRuntimeMCPServerName(name) {
		return resolveManagedMCPToolAuthority(cwd, binary)
	}
	if binary.IsManagedMCPBinary() {
		return contract.MCPToolAuthority{}, fmt.Errorf(
			"mcp_server: built-in manifest-owner identity conflicts with MCP server %q",
			name,
		)
	}
	return o.resolveExternalMCPToolAuthority(ctx, cwd, binary)
}

// resolveManagedMCPToolAuthority 校验 built-in owner 标记与受管命令策略。
func resolveManagedMCPToolAuthority(cwd string, binary providerdto.MCPBinary) (contract.MCPToolAuthority, error) {
	if !binary.IsManagedMCPBinary() {
		return contract.MCPToolAuthority{}, fmt.Errorf(
			"mcp_server: reserved MCP server %q lacks built-in manifest-owner identity",
			binary.Name,
		)
	}
	if err := contract.DefaultRuntimeMCPPolicy().ValidateManifestBinary(binary); err != nil {
		return contract.MCPToolAuthority{}, fmt.Errorf("mcp_server: validate managed MCP server %q: %w", binary.Name, err)
	}
	digest, err := digestMCPToolAuthority(binary)
	return contract.MCPToolAuthority{
		CWD:          filepath.Clean(cwd),
		ServerID:     binary.Name,
		ConfigDigest: digest,
		Managed:      true,
	}, err
}

// resolveExternalMCPToolAuthority 从启用配置中证明 trusted external identity。
func (o *mcpToolAuthorityOwner) resolveExternalMCPToolAuthority(
	ctx context.Context,
	cwd string,
	binary providerdto.MCPBinary,
) (contract.MCPToolAuthority, error) {
	trustedID := strings.TrimSpace(binary.TrustedServerID)
	if trustedID == "" || trustedID != binary.Name {
		return contract.MCPToolAuthority{}, fmt.Errorf("mcp_server: MCP server %q lacks config-owner identity", binary.Name)
	}
	result, err := o.svc.ListServersForCWD(ctx, cwd)
	if err != nil {
		return contract.MCPToolAuthority{}, fmt.Errorf("mcp_server: read authority config: %w", err)
	}
	config, ok := enabledMCPServersToContract(result.MCPServers)[trustedID]
	if !ok || config.TrustedServerID != trustedID || !mcpAuthorityConfigMatchesBinary(config, binary) {
		return contract.MCPToolAuthority{}, fmt.Errorf("mcp_server: MCP server %q is not current in config owner", binary.Name)
	}
	digest, err := digestMCPToolAuthority(config)
	return contract.MCPToolAuthority{
		CWD:            filepath.Clean(cwd),
		ServerID:       binary.Name,
		ConfigDigest:   digest,
		ConfigRevision: o.svc.configRevision,
	}, err
}

// mcpAuthorityConfigMatchesBinary 逐 transport 比对配置 owner 与待启动 binary。
func mcpAuthorityConfigMatchesBinary(config contract.MCPServerConfig, binary providerdto.MCPBinary) bool {
	switch strings.ToLower(strings.TrimSpace(config.Transport)) {
	case "http":
		return strings.EqualFold(strings.TrimSpace(binary.Type), "http") &&
			strings.TrimSpace(config.URL) == strings.TrimSpace(binary.URL) &&
			reflect.DeepEqual(config.Headers, binary.Headers)
	case "stdio":
		command := append([]string{strings.TrimSpace(config.Command)}, config.Args...)
		return len(binary.Command) > 0 && reflect.DeepEqual(command, binary.Command) &&
			reflect.DeepEqual(config.Env, binary.Env)
	default:
		return false
	}
}

func digestMCPToolAuthority(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("mcp_server: encode MCP tool authority: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func mcpToolAuthorityKey(token contract.MCPToolAuthority) string {
	return filepath.Clean(token.CWD) + "\x00" + token.ServerID
}

func cloneMCPToolQuarantine(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	maps.Copy(clone, source)
	return clone
}
