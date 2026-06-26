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
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// bootSnapshot 是从 GO_AGENT_CTL_BOOTSTRAP_JSON 解析出的启动上下文快照。
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

// ReadBootConfig 从控制平面环境变量读取启动配置，并兼容旧环境变量名。
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

// SessionTokenFromEnv 读取控制平面 session token，供不需要完整 Config 的入口复用。
func SessionTokenFromEnv() string {
	return firstEnv("GO_AGENT_CTL_SESSION_TOKEN", "GO_AGENT_MCP_SESSION_TOKEN")
}

// normalizeConfig 规范化 Config 字段，合并 bootSnapshot 缺省值并生成 instance_id/boot_id。
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

// envContext 在控制平面不可达时从 bootSnapshot 构造降级的 ContextResponse。
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

// contextPayloadFromSnapshot 基于启动快照合成离线 Context payload。
// 这些值只用于控制平面不可达时的降级观测，不作为新的权威身份来源。
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

// normalizeContextResponse 统一 Payload/Data 双字段兼容，并补齐空 Scope/Source。
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

// normalizeRegisterResponse 校验并规范化注册响应，缺少 lease key 时返回错误。
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

// validateProtocolVersion 校验服务端协议版本是否与客户端期望版本匹配。
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

// approvalUnavailableErr 构造审批不可用的 jrpc2 错误。
func approvalUnavailableErr(reason string) error {
	return jrpc2.Errorf(jrpc2.Code(mcp.ErrCodeApprovalUnavailable), "%s", strings.TrimSpace(reason))
}

// firstEnv 按优先级返回第一个非空环境变量值，次选 key 使用时记录弃用警告。
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

// readEnvJSON 按优先级读取 JSON 格式的环境变量，次选 key 使用时记录弃用警告。
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

// logDeprecatedEnvKey 在使用已废弃环境变量时打印带截止日期的警告。
func logDeprecatedEnvKey(canonical, legacy string) {
	pkglogger.Warn(fmt.Sprintf("bootstrap env %s is deprecated; use %s instead before 2026-06-30", legacy, canonical),
		"legacy_env", legacy,
		"canonical_env", canonical,
		"remove_after", "2026-06-30",
	)
}

// parseBootSnapshot 从原始 JSON 解析 bootSnapshot；解析失败返回零值快照，启动后续校验仍会兜住必填项。
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

// deriveClientKind 从 binary 名称推断 client_kind（lsp/orch/ida/custom）。
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

// marshalRaw 将任意值序列化为 json.RawMessage，已是 RawMessage 时直接克隆。
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

// cloneStringMapAny 将 map[string]string 转换为 map[string]any 并深拷贝。
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

// defaultContext 若 ctx 为 nil 则返回 context.Background()。
func defaultContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// withTimeoutIfNone 在 ctx 尚未设置 deadline 时追加超时，避免 RPC 永久阻塞。
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

// generateInstanceID 生成基于 binary 名称、PID 和时间戳的唯一实例 ID。
func generateInstanceID() string {
	return fmt.Sprintf("%s-%d-%d", filepath.Base(os.Args[0]), os.Getpid(), time.Now().UnixNano())
}

// generateID 生成带前缀的时间戳 ID，用于 boot_id/report_id 等场景。
func generateID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

// durationOrDefault 将毫秒整数转换为 time.Duration，非正值时返回 fallback。
func durationOrDefault(ms int, fallback time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

// maxDuration 返回两个 Duration 中的较大值。
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// maxInt64 返回两个 int64 中的较大值。
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// jitterDuration 在 base 上叠加最多 2s 的随机抖动，避免多实例同步心跳。
func jitterDuration(base time.Duration) time.Duration {
	return base + time.Duration(rand.Intn(2001))*time.Millisecond
}

// isTransportErr 判断错误是否属于 jrpc2 传输层断连（区别于业务层错误）。
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
