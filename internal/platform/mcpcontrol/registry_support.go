package mcpcontrol

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
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
	normalized, err := normalizeLeaseKey(key)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	instance := r.instances[normalized]
	if instance == nil {
		return nil, errLeaseNotFound("mcp lease %s/%d not found", normalized.InstanceID, normalized.Generation)
	}
	if expected.InstanceID != "" && expected != normalized {
		return nil, errLeaseNotFound("mcp lease %s/%d does not match expected key", normalized.InstanceID, normalized.Generation)
	}
	switch instance.Status {
	case dto.StatusDisconnected:
		return nil, errPeerUnavailable("mcp peer %s/%d is disconnected", normalized.InstanceID, normalized.Generation)
	case dto.StatusStale:
		if !allowStale {
			return nil, errLeaseStale("mcp lease %s/%d is stale", normalized.InstanceID, normalized.Generation)
		}
	}
	return cloneInstance(instance), nil
}

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
	for _, topic := range instance.Subscriptions {
		addIndex(r.bySubscription, topic, instance.Lease)
	}
	for _, capability := range instance.Capabilities {
		addIndex(r.byCapability, capability, instance.Lease)
	}
	if instance.AgentID != "" {
		addIndex(r.byAgent, instance.AgentID, instance.Lease)
	}
	addIndex(r.byPeerKind, instance.PeerKind, instance.Lease)
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
	for _, topic := range instance.Subscriptions {
		removeIndex(r.bySubscription, topic, key)
	}
	for _, capability := range instance.Capabilities {
		removeIndex(r.byCapability, capability, key)
	}
	if instance.AgentID != "" {
		removeIndex(r.byAgent, instance.AgentID, key)
	}
	removeIndex(r.byPeerKind, instance.PeerKind, key)
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
	cloned.Capabilities = cloneStrings(instance.Capabilities)
	cloned.Subscriptions = cloneStrings(instance.Subscriptions)
	return &cloned
}

func toContractInstance(instance *ToolInstance) contract.ToolInstance {
	return contract.ToolInstance{
		Lease:         instance.Lease,
		LeaseID:       instance.LeaseID,
		BinaryName:    instance.BinaryName,
		AgentID:       instance.AgentID,
		ThreadID:      instance.ThreadID,
		PID:           instance.PID,
		Capabilities:  cloneStrings(instance.Capabilities),
		Subscriptions: cloneStrings(instance.Subscriptions),
		PeerKind:      instance.PeerKind,
		ClientKind:    instance.ClientKind,
		Status:        instance.Status,
		ConfigVersion: instance.ConfigVersion,
	}
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
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
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, timeout)
}
