package multilsp

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
)

const (
	maxDocumentOperationGates   = 1024
	maxDocumentOperationWaiters = 256
)

type documentOperationKind uint8

const (
	documentOperationObserveBootstrap documentOperationKind = iota
	documentOperationObserveSync
	documentOperationDidOpen
	documentOperationDidChange
	documentOperationDidClose
)

// documentOperationGate 为单 URI 提供显式 FIFO；gate 本身不保护 manager/cache 状态。
type documentOperationGate struct {
	mu             sync.Mutex
	cond           *sync.Cond
	nextTicket     uint64
	servingTicket  uint64
	mutationIntent uint64
	bootstrapEpoch uint64
	explicitClosed bool
	retainClosed   bool
	refs           int
	canceled       map[uint64]struct{}
}

type documentOperationToken struct {
	manager        *manager
	uri            string
	gate           *documentOperationGate
	ticket         uint64
	mutationEpoch  uint64
	bootstrapEpoch uint64
	kind           documentOperationKind
	released       bool
}

// documentOperationReference 仅保留 URI gate 的瞬时生命周期，不占用 FIFO ticket。
// file workspace/symbol 在能力检查期间用它保存并发 DidClose 已提交的关闭纪元。
type documentOperationReference struct {
	manager  *manager
	uri      string
	gate     *documentOperationGate
	released bool
}

func newDocumentOperationGate() *documentOperationGate {
	gate := &documentOperationGate{}
	gate.cond = sync.NewCond(&gate.mu)
	return gate
}

func (m *manager) beginDocumentOperation(
	ctx context.Context,
	uri string,
	kind documentOperationKind,
) (*documentOperationToken, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m == nil || strings.TrimSpace(uri) == "" {
		return &documentOperationToken{}, nil
	}
	gate, err := m.documentOperationGateForURI(uri)
	if err != nil {
		return nil, err
	}

	gate.mu.Lock()
	ticket := gate.nextTicket
	gate.nextTicket++
	mutationEpoch := gate.reserveOperationIntentLocked(kind)
	stopWake := context.AfterFunc(ctx, func() {
		gate.mu.Lock()
		gate.cond.Broadcast()
		gate.mu.Unlock()
	})
	for ticket != gate.servingTicket {
		if err := ctx.Err(); err != nil {
			gate.cancelTicketLocked(ticket)
			gate.mu.Unlock()
			stopWake()
			m.releaseDocumentOperationReference(uri, gate, kind)
			return nil, err
		}
		gate.cond.Wait()
	}
	if err := ctx.Err(); err != nil {
		gate.cancelTicketLocked(ticket)
		gate.mu.Unlock()
		stopWake()
		m.releaseDocumentOperationReference(uri, gate, kind)
		return nil, err
	}
	stopWake()
	token := &documentOperationToken{
		manager:        m,
		uri:            uri,
		gate:           gate,
		ticket:         ticket,
		mutationEpoch:  mutationEpoch,
		bootstrapEpoch: gate.bootstrapEpoch,
		kind:           kind,
	}
	gate.mu.Unlock()
	return token, nil
}

func (m *manager) documentOperationGateForURI(uri string) (*documentOperationGate, error) {
	m.documentOperationMu.Lock()
	defer m.documentOperationMu.Unlock()
	if m.documentOperations == nil {
		m.documentOperations = make(map[string]*documentOperationGate)
	}
	gate := m.documentOperations[uri]
	if gate == nil {
		limit := positiveDocumentLimit(m.documentOperationLimit, maxDocumentOperationGates)
		if len(m.documentOperations) >= limit {
			return nil, fmt.Errorf("managed document operation gate limit %d reached", limit)
		}
		gate = newDocumentOperationGate()
		m.documentOperations[uri] = gate
	}
	waiterLimit := positiveDocumentLimit(m.documentOperationWaiterLimit, maxDocumentOperationWaiters)
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.refs >= waiterLimit {
		return nil, fmt.Errorf("managed document operation waiter limit %d reached for %s", waiterLimit, uri)
	}
	gate.refs++
	return gate, nil
}

func (m *manager) retainDocumentOperationReference(uri string) (*documentOperationReference, error) {
	gate, err := m.documentOperationGateForURI(uri)
	if err != nil {
		return nil, err
	}
	return &documentOperationReference{manager: m, uri: uri, gate: gate}, nil
}

func (r *documentOperationReference) release() {
	if r == nil || r.manager == nil || r.gate == nil || r.released {
		return
	}
	r.released = true
	r.manager.releaseDocumentOperationReference(r.uri, r.gate, documentOperationObserveBootstrap)
}

func (g *documentOperationGate) reserveOperationIntentLocked(kind documentOperationKind) uint64 {
	switch kind {
	case documentOperationDidOpen, documentOperationDidChange, documentOperationDidClose:
		g.mutationIntent++
	}
	return g.mutationIntent
}

func (g *documentOperationGate) cancelTicketLocked(ticket uint64) {
	if g.canceled == nil {
		g.canceled = make(map[uint64]struct{})
	}
	g.canceled[ticket] = struct{}{}
	g.advanceCanceledLocked()
	g.cond.Broadcast()
}

// commitMutation 只在 mutation 已成功完成 wire side effect 后推进 bootstrap 可见状态。
func (t *documentOperationToken) commitMutation() {
	t.commitMutationWithClosedRetention(true)
}

// commitManagedCloseMutation 保留当前 gate 内的关闭屏障，但在所有排队操作退出后回收历史 gate。
func (t *documentOperationToken) commitManagedCloseMutation() {
	t.commitMutationWithClosedRetention(false)
}

func (t *documentOperationToken) commitMutationWithClosedRetention(retainClosed bool) {
	if t == nil || t.gate == nil {
		return
	}
	t.gate.mu.Lock()
	defer t.gate.mu.Unlock()
	switch t.kind {
	case documentOperationDidOpen:
		t.gate.bootstrapEpoch++
		t.gate.explicitClosed = false
		t.gate.retainClosed = false
	case documentOperationDidClose:
		t.gate.bootstrapEpoch++
		t.gate.explicitClosed = true
		t.gate.retainClosed = retainClosed
	}
}

func (g *documentOperationGate) advanceCanceledLocked() {
	for {
		if _, ok := g.canceled[g.servingTicket]; !ok {
			return
		}
		delete(g.canceled, g.servingTicket)
		g.servingTicket++
	}
}

func (t *documentOperationToken) bootstrapSendAllowed() bool {
	if t == nil || t.gate == nil {
		return false
	}
	t.gate.mu.Lock()
	defer t.gate.mu.Unlock()
	return t.bootstrapEpoch == t.gate.bootstrapEpoch && !t.gate.explicitClosed
}

func (t *documentOperationToken) mutationStillCurrent() bool {
	if t == nil || t.gate == nil {
		return false
	}
	t.gate.mu.Lock()
	defer t.gate.mu.Unlock()
	return t.mutationEpoch == t.gate.mutationIntent
}

func (t *documentOperationToken) release() {
	if t == nil || t.gate == nil || t.manager == nil || t.released {
		return
	}
	t.released = true
	t.gate.mu.Lock()
	if t.gate.servingTicket == t.ticket {
		t.gate.servingTicket++
		t.gate.advanceCanceledLocked()
		t.gate.cond.Broadcast()
	}
	t.gate.mu.Unlock()
	t.manager.releaseDocumentOperationReference(t.uri, t.gate, t.kind)
}

func (m *manager) releaseDocumentOperationReference(uri string, gate *documentOperationGate, kind documentOperationKind) {
	if hook := m.documentOperationReleaseHook; hook != nil {
		hook(kind)
	}
	m.documentOperationMu.Lock()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	defer m.documentOperationMu.Unlock()
	gate.refs--
	if gate.refs == 0 && (!gate.explicitClosed || !gate.retainClosed) && m.documentOperations[uri] == gate {
		delete(m.documentOperations, uri)
	}
}

type workspaceSymbolDocumentGuard struct {
	manager         *manager
	membershipKey   string
	membershipEpoch uint64
	tokens          []*documentOperationToken
}

func (g *workspaceSymbolDocumentGuard) release() {
	if g == nil {
		return
	}
	for _, token := range slices.Backward(g.tokens) {
		token.release()
	}
	g.tokens = nil
}

func (g *workspaceSymbolDocumentGuard) mutationsStillCurrent() bool {
	if g == nil {
		return true
	}
	for _, token := range g.tokens {
		if !token.mutationStillCurrent() {
			return false
		}
	}
	return g.membershipStillCurrent()
}

func (g *workspaceSymbolDocumentGuard) membershipStillCurrent() bool {
	if g == nil || g.manager == nil || g.membershipKey == "" {
		return true
	}
	g.manager.explicitOpenMu.RLock()
	defer g.manager.explicitOpenMu.RUnlock()
	return g.manager.explicitMembershipEpoch[g.membershipKey] == g.membershipEpoch &&
		g.manager.explicitMembershipBusy[g.membershipKey] == 0
}
