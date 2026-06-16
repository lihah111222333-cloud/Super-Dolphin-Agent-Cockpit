package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type bootSnapshot struct {
	InstanceID      string   `json:"instance_id"`
	BootID          string   `json:"boot_id"`
	AgentID         string   `json:"agent_id"` // optional shared-service hint
	ThreadID        string   `json:"thread_id"`
	BinaryName      string   `json:"binary_name"`
	ClientKind      string   `json:"client_kind"`
	CWD             string   `json:"cwd"`
	WorkspaceRunKey string   `json:"workspace_run_key"`
	ConfigVersion   int64    `json:"config_version"`
	Capabilities    []string `json:"capabilities"`
	Subscriptions   []string `json:"subscriptions"`
}

// ReadBootConfig 读取boot配置。
func ReadBootConfig() Config {
	return Config{
		RPCAddr:      firstEnv("GO_AGENT_CTL_RPC_ADDR", "RPC_ADDR"),
		InstanceID:   firstEnv("GO_AGENT_CTL_INSTANCE_ID", "GO_AGENT_MCP_INSTANCE_ID"),
		BootID:       firstEnv("GO_AGENT_CTL_BOOT_ID", "GO_AGENT_MCP_BOOT_ID"),
		BinaryName:   firstEnv("GO_AGENT_CTL_BINARY_NAME", "GO_AGENT_MCP_BINARY_NAME"),
		ClientKind:   firstEnv("GO_AGENT_CTL_CLIENT_KIND", "GO_AGENT_MCP_CLIENT_KIND"),
		AgentID:      firstEnv("GO_AGENT_CTL_AGENT_ID", "GO_AGENT_MCP_AGENT_ID"),
		ThreadID:     firstEnv("GO_AGENT_CTL_THREAD_ID", "GO_AGENT_MCP_THREAD_ID"),
		SessionToken: firstEnv("GO_AGENT_CTL_SESSION_TOKEN", "GO_AGENT_MCP_SESSION_TOKEN"),
		BootSnapshot: readEnvJSON("GO_AGENT_CTL_BOOTSTRAP_JSON", "GO_AGENT_MCP_BOOT_CONTEXT"),
	}
}

// SessionTokenFromEnv 从env处理会话令牌。
func SessionTokenFromEnv() string {
	return firstEnv("GO_AGENT_CTL_SESSION_TOKEN", "GO_AGENT_MCP_SESSION_TOKEN")
}

func normalizeConfig(cfg Config) (Config, bootSnapshot) {
	boot := parseBootSnapshot(cfg.BootSnapshot)
	cfg.RPCAddr = strings.TrimSpace(cfg.RPCAddr)
	cfg.InstanceID = shared.FirstNonEmpty(strings.TrimSpace(cfg.InstanceID), strings.TrimSpace(boot.InstanceID), generateInstanceID())
	cfg.BootID = shared.FirstNonEmpty(strings.TrimSpace(cfg.BootID), strings.TrimSpace(boot.BootID), generateID("boot"))
	cfg.BinaryName = shared.FirstNonEmpty(strings.TrimSpace(cfg.BinaryName), strings.TrimSpace(boot.BinaryName), filepath.Base(os.Args[0]))
	cfg.ClientKind = shared.FirstNonEmpty(strings.TrimSpace(cfg.ClientKind), strings.TrimSpace(boot.ClientKind), deriveClientKind(cfg.BinaryName))
	cfg.AgentID = strings.TrimSpace(cfg.AgentID)
	cfg.ThreadID = shared.FirstNonEmpty(strings.TrimSpace(cfg.ThreadID), strings.TrimSpace(boot.ThreadID))
	cfg.SessionToken = strings.TrimSpace(cfg.SessionToken)
	cfg.Capabilities = shared.CloneStrings(cfg.Capabilities)
	cfg.CapabilitiesOffered = shared.CloneStrings(cfg.CapabilitiesOffered)
	cfg.CapabilitiesRequired = shared.CloneStrings(cfg.CapabilitiesRequired)
	cfg.Subscriptions = shared.CloneStrings(cfg.Subscriptions)
	cfg.BootSnapshot = shared.CloneRawMessage(cfg.BootSnapshot)
	if len(cfg.CapabilitiesOffered) == 0 {
		if len(cfg.Capabilities) != 0 {
			cfg.CapabilitiesOffered = shared.CloneStrings(cfg.Capabilities)
		} else {
			cfg.CapabilitiesOffered = shared.CloneStrings(boot.Capabilities)
		}
	}
	if len(cfg.Subscriptions) == 0 {
		cfg.Subscriptions = shared.CloneStrings(boot.Subscriptions)
	}
	return cfg, boot
}

func (c *Client) envContext(scope string, keys []string) (*mcp.ContextResponse, error) {
	payload, err := json.Marshal(contextPayloadFromSnapshot(c, scope))
	if err != nil {
		return nil, err
	}
	resp := &mcp.ContextResponse{
		Source:     mcp.ContextSourceBootSnapshot,
		ObservedAt: time.Now().UnixMilli(),
		Scope:      strings.TrimSpace(scope),
		Payload:    payload,
		Data:       shared.CloneRawMessage(payload),
	}
	if len(keys) == 0 {
		return resp, nil
	}
	filtered, err := json.Marshal(shared.FilterKeys(contextPayloadFromSnapshot(c, scope), keys))
	if err != nil {
		return nil, err
	}
	resp.Payload = filtered
	resp.Data = shared.CloneRawMessage(filtered)
	return resp, nil
}

// contextPayloadFromSnapshot 从快照处理上下文载荷。
func contextPayloadFromSnapshot(c *Client, scope string) map[string]any {
	clientKind := shared.FirstNonEmpty(c.boot.ClientKind, c.cfg.ClientKind)
	binaryName := shared.FirstNonEmpty(c.boot.BinaryName, c.cfg.BinaryName)
	agentID := strings.TrimSpace(c.cfg.AgentID)
	threadID := shared.FirstNonEmpty(c.boot.ThreadID, c.cfg.ThreadID)
	switch strings.TrimSpace(scope) {
	case mcp.ScopeAgentRuntime:
		return map[string]any{
			"agent_id":    agentID,
			"binary_name": binaryName,
			"client_kind": clientKind,
			"pid":         os.Getpid(),
			"status":      mcp.ContextSourceBootSnapshot,
		}
	case mcp.ScopeThreadBinding:
		return map[string]any{
			"agent_id":    agentID,
			"thread_id":   threadID,
			"instance_id": c.instanceID,
			"generation":  c.currentResumeGeneration(),
		}
	case mcp.ScopeWorkspaceRun:
		return map[string]any{
			"binary_name":       binaryName,
			"capabilities":      shared.CloneStrings(c.offeredCapabilities()),
			"instance_id":       c.instanceID,
			"subscriptions":     shared.CloneStrings(c.cfg.Subscriptions),
			"cwd":               c.boot.CWD,
			"workspace_run_key": c.boot.WorkspaceRunKey,
		}
	case mcp.ScopeConfigSnapshot:
		return map[string]any{
			"capabilities":   shared.CloneStrings(c.offeredCapabilities()),
			"client_kind":    clientKind,
			"config_version": maxInt64(c.boot.ConfigVersion, c.currentConfigVersion()),
			"subscriptions":  shared.CloneStrings(c.cfg.Subscriptions),
		}
	default:
		return map[string]any{
			"instance_id": c.instanceID,
			"boot_id":     c.cfg.BootID,
			"agent_id":    agentID,
			"thread_id":   threadID,
		}
	}
}

// normalizeContextResponse 规范化上下文响应。
func normalizeContextResponse(scope string, resp *mcp.ContextResponse) *mcp.ContextResponse {
	if resp == nil {
		return nil
	}
	out := *resp
	if len(out.Payload) == 0 && len(out.Data) != 0 {
		out.Payload = shared.CloneRawMessage(out.Data)
	}
	if len(out.Data) == 0 && len(out.Payload) != 0 {
		out.Data = shared.CloneRawMessage(out.Payload)
	}
	if strings.TrimSpace(out.Scope) == "" {
		out.Scope = strings.TrimSpace(scope)
	}
	if strings.TrimSpace(out.Source) == "" {
		out.Source = mcp.ContextSourceLive
	}
	return &out
}

// normalizeRegisterResponse 规范化register响应。
func normalizeRegisterResponse(resp *mcp.RegisterResponse, instanceID string) (*mcp.RegisterResponse, error) {
	if resp == nil {
		return nil, errors.New("bootstrap: register response is nil")
	}
	out := *resp
	if out.InstanceID == "" {
		out.InstanceID = strings.TrimSpace(instanceID)
	}
	if out.Generation == 0 && out.AcceptedGeneration != 0 {
		out.Generation = out.AcceptedGeneration
	}
	if out.InstanceID == "" || out.Generation == 0 {
		return nil, errors.New("bootstrap: register response missing lease key")
	}
	if err := validateProtocolVersion(out.ServerProtocolVersion); err != nil {
		return nil, err
	}
	return &out, nil
}

func validateProtocolVersion(version string) error {
	version = strings.TrimSpace(version)
	if version == mcp.ProtocolVersion {
		return nil
	}
	if version == "" {
		return errors.New("bootstrap: server_protocol_version is required")
	}
	return fmt.Errorf("bootstrap: incompatible server protocol version %q", version)
}

func approvalUnavailableErr(reason string) error {
	return jrpc2.Errorf(jrpc2.Code(mcp.ErrCodeApprovalUnavailable), "%s", strings.TrimSpace(reason))
}

func firstEnv(keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	if value := strings.TrimSpace(os.Getenv(keys[0])); value != "" {
		return value
	}
	for _, key := range keys[1:] {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			logDeprecatedEnvKey(keys[0], key)
			return value
		}
	}
	return ""
}

func readEnvJSON(keys ...string) json.RawMessage {
	if len(keys) == 0 {
		return nil
	}
	if value := strings.TrimSpace(os.Getenv(keys[0])); value != "" {
		return json.RawMessage(value)
	}
	for _, key := range keys[1:] {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			logDeprecatedEnvKey(keys[0], key)
			return json.RawMessage(value)
		}
	}
	return nil
}

func logDeprecatedEnvKey(canonical, legacy string) {
	pkglogger.Warn(fmt.Sprintf("bootstrap env %s is deprecated; use %s instead before 2026-06-30", legacy, canonical),
		"legacy_env", legacy,
		"canonical_env", canonical,
		"remove_after", "2026-06-30",
	)
}

func parseBootSnapshot(raw json.RawMessage) bootSnapshot {
	var snap bootSnapshot
	if len(raw) == 0 {
		return snap
	}
	_ = json.Unmarshal(raw, &snap)
	snap.Capabilities = shared.CloneStrings(snap.Capabilities)
	snap.Subscriptions = shared.CloneStrings(snap.Subscriptions)
	return snap
}

func deriveClientKind(binaryName string) string {
	base := filepath.Base(strings.TrimSpace(binaryName))
	switch {
	case strings.Contains(base, "mcp-"+mcp.ClientKindLSP), strings.Contains(base, mcp.ClientKindLSP):
		return mcp.ClientKindLSP
	case strings.Contains(base, "mcp-"+mcp.ClientKindOrch), strings.Contains(base, mcp.ClientKindOrch):
		return mcp.ClientKindOrch
	case strings.Contains(base, "mcp-"+mcp.ClientKindIDA), strings.Contains(base, mcp.ClientKindIDA):
		return mcp.ClientKindIDA
	default:
		return mcp.ClientKindCustom
	}
}

func marshalRaw(payload any) (json.RawMessage, error) {
	switch value := payload.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return shared.CloneRawMessage(value), nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func cloneStringMapAny(in map[string]string) map[string]any {
	cloned := shared.CloneStringMap(in)
	if len(cloned) == 0 {
		return nil
	}
	out := make(map[string]any, len(cloned))
	for key, value := range cloned {
		out[key] = value
	}
	return out
}

func defaultContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func withTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx = defaultContext(ctx)
	if timeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return platformconfig.WithPeerTimeout(ctx, timeout)
}

func generateInstanceID() string {
	return fmt.Sprintf("%s-%d-%d", filepath.Base(os.Args[0]), os.Getpid(), time.Now().UnixNano())
}

func generateID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func durationOrDefault(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func jitterDuration(base time.Duration) time.Duration {
	return base + time.Duration(rand.Intn(2001))*time.Millisecond
}

func isTransportErr(err error) bool {
	if err == nil {
		return false
	}
	var rpcErr *jrpc2.Error
	if errors.As(err, &rpcErr) {
		return false
	}
	return errors.Is(err, channel.ErrClosed) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)
}
