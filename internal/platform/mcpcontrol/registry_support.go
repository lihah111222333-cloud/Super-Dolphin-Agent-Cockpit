package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// reportReceipt 记录 report_id 的幂等状态；pending 用来拒绝同一报告的并发重复提交。
type reportReceipt struct {
	fingerprint string
	response    dto.ReportResponse
	pending     bool
}

// normalizeRegisterRequest 校验注册请求并标准化 peer/client 类型，主机托管服务会被提升为 shared-service。
func normalizeRegisterRequest(req dto.RegisterRequest) (dto.RegisterRequest, error) {
	return normalizeRegisterRequestWithRolePolicy(req, true)
}

// normalizeManagedRegisterRequest 只做公共载荷规范化和能力校验，不改写保留角色身份。
func normalizeManagedRegisterRequest(req dto.RegisterRequest) (dto.RegisterRequest, error) {
	if req.ManagedAuthority != nil {
		proof := *req.ManagedAuthority
		proof.ProtocolVersion = strings.TrimSpace(proof.ProtocolVersion)
		proof.RequestID = strings.TrimSpace(proof.RequestID)
		req.ManagedAuthority = &proof
	}
	normalized, err := normalizeRegisterRequestWithRolePolicy(req, false)
	if err != nil {
		return dto.RegisterRequest{}, err
	}
	negotiated, _ := negotiateRegisterCapabilities(normalized)
	if missing := missingCapabilities(normalized.CapabilitiesRequired, negotiated); len(missing) != 0 {
		return dto.RegisterRequest{}, errCapabilityMismatch(
			"mcp required capabilities are rejected by managed profile: %s",
			strings.Join(missing, ","),
		)
	}
	return normalized, nil
}

// normalizeRegisterRequestWithRolePolicy 共享字段规范化，并由调用方显式决定是否改写旧 canonical role。
func normalizeRegisterRequestWithRolePolicy(req dto.RegisterRequest, rewriteCanonicalRole bool) (dto.RegisterRequest, error) {
	req.InstanceID = strings.TrimSpace(req.InstanceID)
	if req.InstanceID == "" {
		return dto.RegisterRequest{}, errInvalidParams("mcp register instance_id is required")
	}
	req.BinaryName = strings.TrimSpace(req.BinaryName)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.BootID = strings.TrimSpace(req.BootID)
	if rewriteCanonicalRole {
		req.PeerKind = normalizePeerKind(req.PeerKind, req.ClientKind)
		req.ClientKind = normalizeClientKind(req.ClientKind)
	} else {
		req.PeerKind = strings.TrimSpace(req.PeerKind)
		req.ClientKind = strings.TrimSpace(req.ClientKind)
	}
	if rewriteCanonicalRole && isCanonicalServiceClientKind(req.ClientKind) {
		// 主进程托管的单例服务没有 agent scope，注册为 shared-service 后可被各 agent 调用路由复用。
		req.PeerKind = dto.PeerKindSharedService
		req.Shared = true
	}
	req.CapabilitiesOffered = uniqueTrimmed(req.CapabilitiesOffered)
	req.CapabilitiesRequired = uniqueTrimmed(req.CapabilitiesRequired)
	req.Subscriptions = uniqueTrimmed(req.Subscriptions)
	if missing := missingCapabilities(req.CapabilitiesRequired, req.CapabilitiesOffered); len(missing) != 0 {
		return dto.RegisterRequest{}, errCapabilityMismatch("mcp required capabilities are not offered: %s", strings.Join(missing, ","))
	}
	return req, nil
}

// normalizeLeaseKey 标准化租约键并阻断空 instance 或零代际的非法控制面请求。
func normalizeLeaseKey(key dto.LeaseKey) (LeaseKey, error) {
	key.InstanceID = strings.TrimSpace(key.InstanceID)
	if key.InstanceID == "" || key.Generation == 0 {
		return LeaseKey{}, errInvalidParams("mcp lease requires instance_id and generation")
	}
	return key, nil
}

// resolveLease 通过注册表当前状态解析租约，可按调用场景决定是否允许 stale 实例。
func (r *ToolRegistry) resolveLease(key dto.LeaseKey, expected LeaseKey, allowStale bool) (*ToolInstance, error) {
	return lookupLease(leaseLookupOptions{
		registry:   r,
		key:        key,
		expected:   expected,
		allowStale: allowStale,
	})
}

// reserveReport 为 report_id 预留幂等收据，冲突 payload 或同 ID 并发提交会 fail-fast。
func (r *ToolRegistry) reserveReport(key LeaseKey, req dto.ReportRequest) (*dto.ReportResponse, string, error) {
	reportID := strings.TrimSpace(req.ReportID)
	if reportID == "" {
		return nil, "", errInvalidParams("mcp report_id is required")
	}
	fingerprint := reportFingerprint(req)

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.instances[key]; !ok {
		return nil, "", errLeaseNotFound("mcp lease %s/%d not found", key.InstanceID, key.Generation)
	}
	receipts := r.reportReceipts[key]
	if receipts == nil {
		receipts = make(map[string]reportReceipt)
		r.reportReceipts[key] = receipts
	}
	if existing, ok := receipts[reportID]; ok {
		if existing.fingerprint != fingerprint {
			return nil, "", errReportConflict("mcp report_id %q conflicts with an existing payload", reportID)
		}
		if existing.pending {
			return nil, "", errBusy("mcp report_id %q is already in flight", reportID)
		}
		response := existing.response
		return &response, fingerprint, nil
	}
	receipts[reportID] = reportReceipt{fingerprint: fingerprint, pending: true}
	return nil, fingerprint, nil
}

// completeReport 完成报告收据；失败会删除 pending 记录，允许调用方用同一 report_id 重试。
func (r *ToolRegistry) completeReport(key LeaseKey, reportID, fingerprint string, response dto.ReportResponse, err error) {
	reportID = strings.TrimSpace(reportID)
	r.mu.Lock()
	defer r.mu.Unlock()

	receipts := r.reportReceipts[key]
	if receipts == nil || reportID == "" {
		return
	}
	if err != nil {
		delete(receipts, reportID)
		if len(receipts) == 0 {
			delete(r.reportReceipts, key)
		}
		return
	}
	receipts[reportID] = reportReceipt{
		fingerprint: fingerprint,
		response:    response,
	}
}

// indexLocked 将实例写入所有查询索引；调用方必须已持有注册表写锁。
func (r *ToolRegistry) indexLocked(instance *ToolInstance) {
	r.forEachInstanceBucket(instance, func(index map[string]map[LeaseKey]struct{}, bucket string, key LeaseKey) {
		addIndex(index, bucket, key)
	})
}

// evictLocked 从主表和所有索引移除租约并返回待关闭 peer；调用方负责锁外关闭连接。
func (r *ToolRegistry) evictLocked(key LeaseKey) Peer {
	instance := r.instances[key]
	if instance == nil {
		return nil
	}
	delete(r.instances, key)
	delete(r.reportReceipts, key)
	if latest, ok := r.latestByInstance[key.InstanceID]; ok && latest == key {
		delete(r.latestByInstance, key.InstanceID)
	}
	r.forEachInstanceBucket(instance, func(index map[string]map[LeaseKey]struct{}, bucket string, key LeaseKey) {
		removeIndex(index, bucket, key)
	})
	if instance.runtime != nil {
		return instance.runtime.retire()
	}
	return instance.Peer
}

// addIndex 把租约键加入索引桶，空桶名不建索引。
func addIndex(index map[string]map[LeaseKey]struct{}, bucket string, key LeaseKey) {
	if bucket == "" {
		return
	}
	current := index[bucket]
	if current == nil {
		current = make(map[LeaseKey]struct{})
		index[bucket] = current
	}
	current[key] = struct{}{}
}

// removeIndex 从索引桶移除租约键，并在桶为空时删除桶以保持后续 selector 判断准确。
func removeIndex(index map[string]map[LeaseKey]struct{}, bucket string, key LeaseKey) {
	current := index[bucket]
	if len(current) == 0 {
		return
	}
	delete(current, key)
	if len(current) == 0 {
		delete(index, bucket)
	}
}

// cloneInstance 深拷贝实例中的切片字段，避免锁外调用方修改注册表共享切片。
func cloneInstance(instance *ToolInstance) *ToolInstance {
	if instance == nil {
		return nil
	}
	cloned := *instance
	cloned.Capabilities = platformshared.CloneStrings(instance.Capabilities)
	cloned.Subscriptions = platformshared.CloneStrings(instance.Subscriptions)
	return &cloned
}

// toContractInstance 把内部实例投影为跨模块 contract 视图，不暴露 Peer 和失败计数。
func toContractInstance(instance *ToolInstance) contract.ToolInstance {
	return contract.ToolInstance{
		Lease:         instance.Lease,
		LeaseID:       instance.LeaseID, // Deprecated: 为旧 contract 字段保留到 2026-06-30。
		BinaryName:    instance.BinaryName,
		AgentID:       instance.AgentID,
		ThreadID:      instance.ThreadID,
		PID:           instance.PID,
		Capabilities:  platformshared.CloneStrings(instance.Capabilities),
		Subscriptions: platformshared.CloneStrings(instance.Subscriptions),
		PeerKind:      instance.PeerKind,
		ClientKind:    instance.ClientKind,
		Shared:        instance.Shared,
		Status:        instance.Status,
		ConfigVersion: instance.ConfigVersion,
	}
}

// uniqueTrimmed 去重并删除空字符串，保持能力和订阅列表的注册顺序。
func uniqueTrimmed(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizedStringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

// missingCapabilities 返回 required 中未被 offered 声明的能力列表。
func missingCapabilities(required, offered []string) []string {
	available := make(map[string]struct{}, len(offered))
	for _, capability := range offered {
		available[capability] = struct{}{}
	}
	var missing []string
	for _, capability := range required {
		if _, ok := available[capability]; !ok {
			missing = append(missing, capability)
		}
	}
	return missing
}

var managedOrchAllowedCapabilities = normalizedStringSet(dto.OrchCapabilities())

// negotiateRegisterCapabilities 只为 managed orch 套用服务端 profile；旧 LSP/IDA 保持全部 offered 接受。
func negotiateRegisterCapabilities(req dto.RegisterRequest) ([]string, []string) {
	if req.ManagedAuthority == nil || req.ClientKind != dto.ClientKindOrch {
		return platformshared.CloneStrings(req.CapabilitiesOffered), []string{}
	}
	negotiated := make([]string, 0, len(req.CapabilitiesOffered))
	rejected := make([]string, 0)
	for _, capability := range req.CapabilitiesOffered {
		if _, ok := managedOrchAllowedCapabilities[capability]; ok {
			negotiated = append(negotiated, capability)
		} else {
			rejected = append(rejected, capability)
		}
	}
	return negotiated, rejected
}

// normalizePeerKind 标准化 peer 类型；未声明时默认按工具 peer 处理。
func normalizePeerKind(peerKind, clientKind string) string {
	switch strings.ToLower(strings.TrimSpace(peerKind)) {
	case dto.PeerKindUI:
		return dto.PeerKindUI
	case dto.PeerKindTool:
		return dto.PeerKindTool
	case dto.PeerKindSharedService:
		return dto.PeerKindSharedService
	}
	switch strings.ToLower(strings.TrimSpace(clientKind)) {
	case dto.PeerKindUI:
		return dto.PeerKindUI
	default:
		return dto.PeerKindTool
	}
}

// normalizeClientKind 只接受内置服务 client kind，其余统一归为 custom。
func normalizeClientKind(clientKind string) string {
	switch strings.ToLower(strings.TrimSpace(clientKind)) {
	case dto.ClientKindOrch, dto.ClientKindLSP, dto.ClientKindIDA:
		return strings.ToLower(strings.TrimSpace(clientKind))
	default:
		return dto.ClientKindCustom
	}
}

// reportFingerprint 生成 report 幂等指纹，包含 report_id、显式类型和完整 envelope。
func reportFingerprint(req dto.ReportRequest) string {
	return req.ReportID + "\n" + req.Report.Type + "\n" + marshalReport(req.Report)
}

// marshalReport 为幂等指纹序列化报告；异常时至少保留类型字段参与冲突判断。
func marshalReport(report dto.ReportEnvelope) string {
	raw, err := json.Marshal(report)
	if err != nil {
		return report.Type
	}
	return string(raw)
}

// intOrDefault 在配置值为零或负数时返回默认值。
func intOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// durationOrDefault 在配置时长为零或负数时返回默认值。
func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

// withTimeoutContext 统一走 platform config 的 peer timeout 包装，保持控制面超时语义一致。
func withTimeoutContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return platformconfig.WithPeerTimeout(ctx, timeout)
}

// isCanonicalServiceClientKind 判断 clientKind 是否为主进程托管的单例 MCP 服务。
// 这类 peer 启动时不绑定 agent，必须按 shared-service 注册才能被各 agent 路由到。
func isCanonicalServiceClientKind(clientKind string) bool {
	switch strings.ToLower(strings.TrimSpace(clientKind)) {
	case dto.ClientKindOrch, dto.ClientKindLSP, dto.ClientKindIDA:
		return true
	default:
		return false
	}
}
