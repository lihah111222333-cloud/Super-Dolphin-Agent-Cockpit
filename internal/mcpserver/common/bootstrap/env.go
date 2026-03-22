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
)

type bootSnapshot struct {
	InstanceID      string   `json:"instance_id"`
	BootID          string   `json:"boot_id"`
	AgentID         string   `json:"agent_id"`
	ThreadID        string   `json:"thread_id"`
	BinaryName      string   `json:"binary_name"`
	ClientKind      string   `json:"client_kind"`
	CWD             string   `json:"cwd"`
	WorkspaceRunKey string   `json:"workspace_run_key"`
	ConfigVersion   int64    `json:"config_version"`
	Capabilities    []string `json:"capabilities"`
	Subscriptions   []string `json:"subscriptions"`
}

func ReadBootConfig() Config {
	return Config{
		RPCAddr:      firstEnv("RPC_ADDR"),
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

func normalizeConfig(cfg Config) (Config, bootSnapshot) {
	boot := parseBootSnapshot(cfg.BootSnapshot)
	cfg.RPCAddr = strings.TrimSpace(cfg.RPCAddr)
	cfg.InstanceID = firstNonEmpty(strings.TrimSpace(cfg.InstanceID), strings.TrimSpace(boot.InstanceID), generateInstanceID())
	cfg.BootID = firstNonEmpty(strings.TrimSpace(cfg.BootID), strings.TrimSpace(boot.BootID), generateID("boot"))
	cfg.BinaryName = firstNonEmpty(strings.TrimSpace(cfg.BinaryName), strings.TrimSpace(boot.BinaryName), filepath.Base(os.Args[0]))
	cfg.ClientKind = firstNonEmpty(strings.TrimSpace(cfg.ClientKind), strings.TrimSpace(boot.ClientKind), deriveClientKind(cfg.BinaryName))
	cfg.AgentID = firstNonEmpty(strings.TrimSpace(cfg.AgentID), strings.TrimSpace(boot.AgentID))
	cfg.ThreadID = firstNonEmpty(strings.TrimSpace(cfg.ThreadID), strings.TrimSpace(boot.ThreadID))
	cfg.SessionToken = strings.TrimSpace(cfg.SessionToken)
	cfg.Capabilities = cloneStrings(cfg.Capabilities)
	cfg.CapabilitiesOffered = cloneStrings(cfg.CapabilitiesOffered)
	cfg.CapabilitiesRequired = cloneStrings(cfg.CapabilitiesRequired)
	cfg.Subscriptions = cloneStrings(cfg.Subscriptions)
	cfg.BootSnapshot = cloneRaw(cfg.BootSnapshot)
	if len(cfg.CapabilitiesOffered) == 0 {
		if len(cfg.Capabilities) != 0 {
			cfg.CapabilitiesOffered = cloneStrings(cfg.Capabilities)
		} else {
			cfg.CapabilitiesOffered = cloneStrings(boot.Capabilities)
		}
	}
	if len(cfg.Subscriptions) == 0 {
		cfg.Subscriptions = cloneStrings(boot.Subscriptions)
	}
	return cfg, boot
}

func (c *Client) envContext(scope string, keys []string) (*mcp.ContextResponse, error) {
	payload, err := json.Marshal(contextPayloadFromSnapshot(c, scope))
	if err != nil {
		return nil, err
	}
	resp := &mcp.ContextResponse{
		Source:     "boot_snapshot",
		ObservedAt: time.Now().UnixMilli(),
		Scope:      strings.TrimSpace(scope),
		Payload:    payload,
		Data:       cloneRaw(payload),
	}
	if len(keys) == 0 {
		return resp, nil
	}
	filtered, err := json.Marshal(filterKeys(contextPayloadFromSnapshot(c, scope), keys))
	if err != nil {
		return nil, err
	}
	resp.Payload = filtered
	resp.Data = cloneRaw(filtered)
	return resp, nil
}

func contextPayloadFromSnapshot(c *Client, scope string) map[string]any {
	clientKind := firstNonEmpty(c.boot.ClientKind, c.cfg.ClientKind)
	binaryName := firstNonEmpty(c.boot.BinaryName, c.cfg.BinaryName)
	agentID := firstNonEmpty(c.boot.AgentID, c.cfg.AgentID)
	threadID := firstNonEmpty(c.boot.ThreadID, c.cfg.ThreadID)
	switch strings.TrimSpace(scope) {
	case mcp.ScopeAgentRuntime:
		return map[string]any{
			"agent_id":    agentID,
			"binary_name": binaryName,
			"client_kind": clientKind,
			"pid":         os.Getpid(),
			"status":      "boot_snapshot",
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
			"capabilities":      cloneStrings(c.offeredCapabilities()),
			"instance_id":       c.instanceID,
			"subscriptions":     cloneStrings(c.cfg.Subscriptions),
			"cwd":               c.boot.CWD,
			"workspace_run_key": c.boot.WorkspaceRunKey,
		}
	case mcp.ScopeConfigSnapshot:
		return map[string]any{
			"capabilities":   cloneStrings(c.offeredCapabilities()),
			"client_kind":    clientKind,
			"config_version": maxInt64(c.boot.ConfigVersion, c.currentConfigVersion()),
			"subscriptions":  cloneStrings(c.cfg.Subscriptions),
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

func normalizeContextResponse(scope string, resp *mcp.ContextResponse) *mcp.ContextResponse {
	if resp == nil {
		return nil
	}
	out := *resp
	if len(out.Payload) == 0 && len(out.Data) != 0 {
		out.Payload = cloneRaw(out.Data)
	}
	if len(out.Data) == 0 && len(out.Payload) != 0 {
		out.Data = cloneRaw(out.Payload)
	}
	if strings.TrimSpace(out.Scope) == "" {
		out.Scope = strings.TrimSpace(scope)
	}
	if strings.TrimSpace(out.Source) == "" {
		out.Source = "live"
	}
	return &out
}

func normalizeRegisterResponse(resp *mcp.RegisterResponse, instanceID string) (*mcp.RegisterResponse, error) {
	if resp == nil {
		return nil, errors.New("bootstrap: register response is nil")
	}
	out := *resp
	if out.Lease.InstanceID == "" {
		out.Lease.InstanceID = strings.TrimSpace(instanceID)
	}
	if out.Lease.Generation == 0 && out.AcceptedGeneration != 0 {
		out.Lease.Generation = out.AcceptedGeneration
	}
	if out.Lease.InstanceID == "" || out.Lease.Generation == 0 {
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
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func readEnvJSON(keys ...string) json.RawMessage {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return json.RawMessage(value)
		}
	}
	return nil
}

func parseBootSnapshot(raw json.RawMessage) bootSnapshot {
	var snap bootSnapshot
	if len(raw) == 0 {
		return snap
	}
	_ = json.Unmarshal(raw, &snap)
	snap.Capabilities = cloneStrings(snap.Capabilities)
	snap.Subscriptions = cloneStrings(snap.Subscriptions)
	return snap
}

func deriveClientKind(binaryName string) string {
	base := filepath.Base(strings.TrimSpace(binaryName))
	switch {
	case strings.Contains(base, "mcp-lsp"), strings.Contains(base, "lsp"):
		return "lsp"
	case strings.Contains(base, "mcp-orch"), strings.Contains(base, "orch"):
		return "orch"
	case strings.Contains(base, "mcp-ida"), strings.Contains(base, "ida"):
		return "ida"
	default:
		return "custom"
	}
}

func filterKeys(payload map[string]any, keys []string) map[string]any {
	if len(keys) == 0 {
		return payload
	}
	filtered := make(map[string]any, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value, ok := payload[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}

func marshalRaw(payload any) (json.RawMessage, error) {
	switch value := payload.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return cloneRaw(value), nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneRaw(in json.RawMessage) json.RawMessage {
	if len(in) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), in...)
}

func cloneStringMapAny(in map[string]string) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
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
	return context.WithTimeout(ctx, timeout)
}

func normalizeQueueLimit(limit int) int {
	if limit <= 0 {
		return defaultReportQueueLimit
	}
	return limit
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
