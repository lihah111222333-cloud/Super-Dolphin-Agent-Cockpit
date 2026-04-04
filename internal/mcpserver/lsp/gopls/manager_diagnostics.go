package gopls

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

type managerNotificationHandler struct {
	publishDiagnostics func(protocol.PublishDiagnosticsParams) error
	logMessage         func(protocol.LogMessageParams) error
}

func (h managerNotificationHandler) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	return h.publishDiagnostics(params)
}

func (h managerNotificationHandler) LogMessage(params protocol.LogMessageParams) error {
	return h.logMessage(params)
}

func (m *manager) Diagnostics(_ context.Context, uris []string) ([]protocol.PublishDiagnosticsParams, error) {
	filter, err := m.normalizeDiagnosticFilter(uris)
	if err != nil {
		return nil, err
	}
	items := make([]protocol.PublishDiagnosticsParams, 0, len(m.diagnostics))
	m.forEachCurrentDiagnostic(filter, func(snapshot diagnosticSnapshot) {
		items = append(items, snapshot.params)
	})
	sort.Slice(items, func(i, j int) bool {
		return items[i].URI < items[j].URI
	})
	return items, nil
}

func (m *manager) WaitDiagnosticsStable(ctx context.Context, uris []string) error {
	if err := sleepContext(ctx, m.diagInitial); err != nil {
		return err
	}
	filter, err := m.normalizeDiagnosticFilter(uris)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(m.diagMaxWait)
	lastUpdate := m.latestDiagnosticUpdate(filter)
	for {
		if time.Now().After(deadline) {
			return nil
		}
		if lastUpdate.IsZero() || time.Since(lastUpdate) >= m.diagPoll {
			return nil
		}
		waitFor := minDuration(m.diagPoll, time.Until(deadline))
		if err := sleepContext(ctx, waitFor); err != nil {
			return err
		}
		if next := m.latestDiagnosticUpdate(filter); next.After(lastUpdate) {
			lastUpdate = next
		}
	}
}

func (m *manager) CurrentDiagnosticGeneration() uint64 {
	return m.diagGeneration.Load()
}

func (m *manager) AdvanceDiagnosticGeneration() uint64 {
	next := m.diagGeneration.Add(1)
	m.diagMu.Lock()
	clear(m.diagnostics)
	m.diagMu.Unlock()
	return next
}

func (m *manager) PublishDiagnostics(params protocol.PublishDiagnosticsParams) error {
	return m.publishDiagnosticsForGeneration(params, m.CurrentDiagnosticGeneration())
}

func (m *manager) publishDiagnosticsForGeneration(params protocol.PublishDiagnosticsParams, capturedGen uint64) error {
	m.diagMu.Lock()
	defer m.diagMu.Unlock()
	if capturedGen < m.CurrentDiagnosticGeneration() {
		return nil
	}
	m.diagnostics[params.URI] = diagnosticSnapshot{
		params:     params,
		generation: capturedGen,
		updatedAt:  time.Now(),
	}
	return nil
}

func (m *manager) latestDiagnosticUpdate(filter map[string]struct{}) time.Time {
	var latest time.Time
	m.forEachCurrentDiagnostic(filter, func(snapshot diagnosticSnapshot) {
		if snapshot.updatedAt.After(latest) {
			latest = snapshot.updatedAt
		}
	})
	return latest
}

func (m *manager) normalizeDiagnosticFilter(uris []string) (map[string]struct{}, error) {
	filter := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		if strings.TrimSpace(uri) == "" {
			continue
		}
		ref, err := m.resolveDocumentRef(uri, "")
		if err != nil {
			return nil, err
		}
		filter[ref.uri] = struct{}{}
	}
	return filter, nil
}

func minDuration(left, right time.Duration) time.Duration {
	if right <= 0 {
		return left
	}
	if left < right {
		return left
	}
	return right
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
