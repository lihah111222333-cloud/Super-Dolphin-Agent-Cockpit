package multilsp

import (
	"fmt"
	"os"
)

const (
	defaultExplicitDocumentLimit  = 512
	defaultDirtyDocumentLimit     = 64
	defaultDirtyDocumentByteLimit = 16 << 20
	defaultCleanDocumentByteLimit = 4 << 20
	defaultCleanRefreshByteLimit  = 16 << 20
)

func positiveDocumentLimit(configured, fallback int) int {
	if configured > 0 {
		return configured
	}
	return fallback
}

func positiveDocumentByteLimit(configured, fallback int64) int64 {
	if configured > 0 {
		return configured
	}
	return fallback
}

func (m *manager) preflightExplicitDocumentSyncReads(states []explicitDocumentState) error {
	perDocumentLimit := m.effectiveCleanDocumentByteLimit()
	aggregateLimit := m.effectiveCleanRefreshByteLimit()
	var aggregateBytes int64
	for _, state := range states {
		if !state.fileBacked || !state.diskClean {
			continue
		}
		info, err := os.Stat(state.absPath)
		if err != nil {
			return fmt.Errorf("stat clean workspace document %s: %w", state.uri, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("clean workspace document is not regular: %s", state.uri)
		}
		if info.Size() > perDocumentLimit {
			return fmt.Errorf("clean workspace document exceeds read limit %d: %s", perDocumentLimit, state.uri)
		}
		if aggregateBytes > aggregateLimit-info.Size() {
			return fmt.Errorf("clean workspace document aggregate exceeds read limit %d", aggregateLimit)
		}
		aggregateBytes += info.Size()
	}
	return nil
}

type explicitDocumentReservation struct {
	manager        *manager
	before         explicitDocumentState
	hadBefore      bool
	after          explicitDocumentState
	reservedDocs   int
	reservedDirty  int
	reservedBytes  int
	membershipKeys []string
	active         bool
}

func (m *manager) reserveExplicitDocumentState(
	before explicitDocumentState,
	hadBefore bool,
	after explicitDocumentState,
) (*explicitDocumentReservation, error) {
	if m == nil {
		return nil, ErrManagerClosed
	}
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	current, exists := m.explicitDocuments[after.uri]
	if exists != hadBefore || (exists && current != before) {
		return nil, fmt.Errorf("managed document changed before capacity reservation: %s", after.uri)
	}
	documents, dirtyDocuments, dirtyBytes := explicitDocumentUsage(m.explicitDocuments)
	nextDocuments := documents
	if !hadBefore {
		nextDocuments++
	}
	beforeDirty, beforeBytes := explicitDocumentDirtyUsage(before, hadBefore)
	afterDirty, afterBytes := explicitDocumentDirtyUsage(after, true)
	nextDirty := dirtyDocuments - beforeDirty + afterDirty
	nextBytes := dirtyBytes - beforeBytes + afterBytes
	reservedDocs := max(0, nextDocuments-documents)
	reservedDirty := max(0, nextDirty-dirtyDocuments)
	reservedBytes := max(0, nextBytes-dirtyBytes)
	if nextDocuments+m.explicitReservedDocs > m.effectiveExplicitDocumentLimit() {
		return nil, fmt.Errorf("managed document limit exceeded: %d", m.effectiveExplicitDocumentLimit())
	}
	if nextDirty+m.explicitReservedDirty > m.effectiveDirtyDocumentLimit() {
		return nil, fmt.Errorf("dirty managed document limit exceeded: %d", m.effectiveDirtyDocumentLimit())
	}
	if nextBytes+m.explicitReservedBytes > m.effectiveDirtyDocumentByteLimit() {
		return nil, fmt.Errorf("dirty managed document byte limit exceeded: %d", m.effectiveDirtyDocumentByteLimit())
	}
	membershipKeys := explicitDocumentMembershipReservationKeys(before, hadBefore, after)
	m.beginExplicitMembershipReservationLocked(membershipKeys)
	m.explicitReservedDocs += reservedDocs
	m.explicitReservedDirty += reservedDirty
	m.explicitReservedBytes += reservedBytes
	return &explicitDocumentReservation{
		manager: m, before: before, hadBefore: hadBefore, after: after,
		reservedDocs: reservedDocs, reservedDirty: reservedDirty, reservedBytes: reservedBytes,
		membershipKeys: membershipKeys, active: true,
	}, nil
}

func (r *explicitDocumentReservation) commitForRecipient(client Client) error {
	if r == nil || r.manager == nil || !r.active {
		return fmt.Errorf("managed document reservation is not active")
	}
	m := r.manager
	m.mu.RLock()
	m.explicitOpenMu.Lock()
	defer m.explicitOpenMu.Unlock()
	defer m.mu.RUnlock()
	defer r.releaseLocked()
	if err := m.validateExplicitDocumentRecipientLocked(client, r.after); err != nil {
		return err
	}
	current, exists := m.explicitDocuments[r.after.uri]
	if exists != r.hadBefore || (exists && current != r.before) {
		return fmt.Errorf("managed document changed before reservation commit: %s", r.after.uri)
	}
	m.explicitDocuments[r.after.uri] = r.after
	return nil
}

func (m *manager) validateExplicitDocumentRecipientLocked(client Client, expected explicitDocumentState) error {
	if m.closed || m.retiring {
		return ErrManagerClosed
	}
	workspace := m.workspaces[expected.configKey]
	if workspace == nil || workspace.client != client || workspace.generation != expected.clientGeneration {
		return ErrStaleClientLease
	}
	return validateLeaseWorkspace(workspace, true)
}

func (r *explicitDocumentReservation) cancel() {
	if r == nil || r.manager == nil || !r.active {
		return
	}
	r.manager.explicitOpenMu.Lock()
	defer r.manager.explicitOpenMu.Unlock()
	r.releaseLocked()
}

func (r *explicitDocumentReservation) releaseLocked() {
	if !r.active {
		return
	}
	r.manager.explicitReservedDocs -= r.reservedDocs
	r.manager.explicitReservedDirty -= r.reservedDirty
	r.manager.explicitReservedBytes -= r.reservedBytes
	r.manager.finishExplicitMembershipReservationLocked(r.membershipKeys)
	r.active = false
}

func explicitDocumentMembershipReservationKeys(
	before explicitDocumentState,
	hadBefore bool,
	after explicitDocumentState,
) []string {
	afterKey := explicitDocumentMembershipKey(after)
	if !hadBefore {
		return []string{afterKey}
	}
	beforeKey := explicitDocumentMembershipKey(before)
	if beforeKey == afterKey {
		return nil
	}
	return []string{beforeKey, afterKey}
}

func explicitDocumentMembershipKey(state explicitDocumentState) string {
	return state.configKey + "\x00" + state.scopeKey + "\x00" + state.workspaceKey + "\x00" + state.languageID
}

func explicitDocumentMembershipKeyForScope(cfg workspaceConfig, scope ResolvedLSPToolScope) string {
	return cfg.key + "\x00" + scope.ScopeKey + "\x00" + scope.WorkspaceKey + "\x00" + cfg.languageID
}

func (m *manager) beginExplicitMembershipReservationLocked(keys []string) {
	if len(keys) > 0 && m.explicitMembershipEpoch == nil {
		m.explicitMembershipEpoch = make(map[string]uint64)
	}
	if len(keys) > 0 && m.explicitMembershipBusy == nil {
		m.explicitMembershipBusy = make(map[string]int)
	}
	for _, key := range keys {
		m.explicitMembershipEpoch[key]++
		m.explicitMembershipBusy[key]++
	}
}

func (m *manager) finishExplicitMembershipReservationLocked(keys []string) {
	for _, key := range keys {
		m.explicitMembershipEpoch[key]++
		m.explicitMembershipBusy[key]--
		if m.explicitMembershipBusy[key] == 0 {
			delete(m.explicitMembershipBusy, key)
		}
	}
}

func explicitDocumentUsage(states map[string]explicitDocumentState) (documents, dirtyDocuments, dirtyBytes int) {
	for _, state := range states {
		documents++
		dirty, bytes := explicitDocumentDirtyUsage(state, true)
		dirtyDocuments += dirty
		dirtyBytes += bytes
	}
	return documents, dirtyDocuments, dirtyBytes
}

func explicitDocumentDirtyUsage(state explicitDocumentState, exists bool) (documents, bytes int) {
	if !exists || state.diskClean {
		return 0, 0
	}
	return 1, len([]byte(state.text))
}

func (m *manager) effectiveExplicitDocumentLimit() int {
	if m.explicitDocumentLimit > 0 {
		return m.explicitDocumentLimit
	}
	return defaultExplicitDocumentLimit
}

func (m *manager) effectiveDirtyDocumentLimit() int {
	if m.dirtyDocumentLimit > 0 {
		return m.dirtyDocumentLimit
	}
	return defaultDirtyDocumentLimit
}

func (m *manager) effectiveDirtyDocumentByteLimit() int {
	if m.dirtyDocumentByteLimit > 0 {
		return m.dirtyDocumentByteLimit
	}
	return defaultDirtyDocumentByteLimit
}

func (m *manager) effectiveCleanDocumentByteLimit() int64 {
	return positiveDocumentByteLimit(m.cleanDocumentByteLimit, defaultCleanDocumentByteLimit)
}

func (m *manager) effectiveCleanRefreshByteLimit() int64 {
	return positiveDocumentByteLimit(m.cleanRefreshByteLimit, defaultCleanRefreshByteLimit)
}
