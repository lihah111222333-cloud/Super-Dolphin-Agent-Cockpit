package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type reportReceipt struct {
	fingerprint string
	response    dto.ReportResponse
	pending     bool
}

func normalizeRegisterRequest(req dto.RegisterRequest) (dto.RegisterRequest, error) {
	req.InstanceID = strings.TrimSpace(req.InstanceID)
	if req.InstanceID == "" {
		return dto.RegisterRequest{}, errInvalidParams("mcp register instance_id is required")
	}
	req.BinaryName = strings.TrimSpace(req.BinaryName)
	req.AgentID = strings.TrimSpace(req.AgentID)
	req.ThreadID = strings.TrimSpace(req.ThreadID)
	req.PeerKind = normalizePeerKind(req.PeerKind, req.ClientKind)
	req.ClientKind = normalizeClientKind(req.ClientKind)
	if isCanonicalServiceClientKind(req.ClientKind) {
		// Host-managed singleton services (mcp-orch / mcp-lsp / mcp-ida) start
		// once with no agent scope; register them as shared-service peers so
		// FindActiveForScope resolves them for every agent's tool calls.
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

func normalizeLeaseKey(key dto.LeaseKey) (LeaseKey, error) {
	key.InstanceID = strings.TrimSpace(key.InstanceID)
	if key.InstanceID == "" || key.Generation == 0 {
		return LeaseKey{}, errInvalidParams("mcp lease requires instance_id and generation")
	}
	return key, nil
}

func (r *ToolRegistry) resolveLease(key dto.LeaseKey, expected LeaseKey, allowStale bool) (*ToolInstance, error) {
	return lookupLease(leaseLookupOptions{
		registry:   r,
		key:        key,
		expected:   expected,
		allowStale: allowStale,
	})
}

// reserveReport 处理reservereport。
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

func (r *ToolRegistry) indexLocked(instance *ToolInstance) {
	r.forEachInstanceBucket(instance, func(index map[string]map[LeaseKey]struct{}, bucket string, key LeaseKey) {
		addIndex(index, bucket, key)
	})
}

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
	return instance.Peer
}

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

func cloneInstance(instance *ToolInstance) *ToolInstance {
	if instance == nil {
		return nil
	}
	cloned := *instance
	cloned.Capabilities = platformshared.CloneStrings(instance.Capabilities)
	cloned.Subscriptions = platformshared.CloneStrings(instance.Subscriptions)
	return &cloned
}

func toContractInstance(instance *ToolInstance) contract.ToolInstance {
	return contract.ToolInstance{
		Lease:         instance.Lease,
		LeaseID:       instance.LeaseID, // Deprecated: use LeaseKey. Will be removed after 2026-06-30.
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

func normalizeClientKind(clientKind string) string {
	switch strings.ToLower(strings.TrimSpace(clientKind)) {
	case dto.ClientKindOrch, dto.ClientKindLSP, dto.ClientKindIDA:
		return strings.ToLower(strings.TrimSpace(clientKind))
	default:
		return dto.ClientKindCustom
	}
}

func reportFingerprint(req dto.ReportRequest) string {
	return req.ReportID + "\n" + req.Report.Type + "\n" + marshalReport(req.Report)
}

func marshalReport(report dto.ReportEnvelope) string {
	raw, err := json.Marshal(report)
	if err != nil {
		return report.Type
	}
	return string(raw)
}

func intOrDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

func durationOrDefault(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

func withTimeoutContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return platformconfig.WithPeerTimeout(ctx, timeout)
}

// isCanonicalServiceClientKind reports whether clientKind identifies one of the
// host-managed singleton MCP services (mcp-orch / mcp-lsp / mcp-ida). Those
// peers are launched once at startup with no agent scope, so they must be
// registered as shared-service peers to remain resolvable for every agent.
func isCanonicalServiceClientKind(clientKind string) bool {
	switch strings.ToLower(strings.TrimSpace(clientKind)) {
	case dto.ClientKindOrch, dto.ClientKindLSP, dto.ClientKindIDA:
		return true
	default:
		return false
	}
}
