package multilsp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
)

type diagnosticReadiness struct {
	latest  time.Time
	missing []string
}

type diagnosticStableWait struct {
	manager   *manager
	ctx       context.Context
	filter    diagnosticFilter
	uris      []string
	readiness diagnosticReadiness
	last      time.Time
}

func (m *manager) newDiagnosticStableWait(ctx context.Context, filter diagnosticFilter, uris []string) (*diagnosticStableWait, error) {
	readiness, err := m.diagnosticReadiness(ctx, filter, uris)
	if err != nil {
		return nil, err
	}
	return &diagnosticStableWait{
		manager:   m,
		ctx:       ctx,
		filter:    filter,
		uris:      uris,
		readiness: readiness,
		last:      readiness.latest,
	}, nil
}

// wait 等待LSP。
func (w *diagnosticStableWait) wait() error {
	for {
		if err := w.contextError(w.ctx.Err()); err != nil {
			return err
		}
		if len(w.readiness.missing) > 0 {
			if err := w.refresh(true); err != nil {
				return err
			}
			continue
		}
		if w.last.IsZero() || time.Since(w.last) >= w.manager.diagPoll {
			return nil
		}
		if err := w.refresh(false); err != nil {
			return err
		}
	}
}

func (w *diagnosticStableWait) contextError(err error) error {
	if err == nil {
		return nil
	}
	if len(w.readiness.missing) > 0 {
		return fmt.Errorf("%w: diagnostics did not publish for requested targets before context finished: %s: %w", lspmanager.ErrDiagnosticsNotReady, strings.Join(w.readiness.missing, ", "), err)
	}
	return fmt.Errorf("%w: diagnostics did not stabilize before context finished: %w", lspmanager.ErrDiagnosticsNotReady, err)
}

// refresh 刷新LSP。
func (w *diagnosticStableWait) refresh(resetLast bool) error {
	waitFor := w.manager.diagPoll
	if deadline, ok := w.ctx.Deadline(); ok {
		waitFor = minDuration(waitFor, time.Until(deadline))
	}
	if err := sleepContext(w.ctx, waitFor); err != nil {
		return w.contextError(err)
	}
	readiness, err := w.manager.diagnosticReadiness(w.ctx, w.filter, w.uris)
	if err != nil {
		return err
	}
	w.readiness = readiness
	if resetLast || readiness.latest.After(w.last) {
		w.last = readiness.latest
	}
	return nil
}

func (m *manager) diagnosticReadiness(ctx context.Context, filter diagnosticFilter, uris []string) (diagnosticReadiness, error) {
	if len(uris) == 0 {
		return diagnosticReadiness{latest: m.latestDiagnosticUpdate(filter)}, nil
	}
	return m.requestedDiagnosticReadiness(ctx, filter, uniqueDiagnosticURIs(uris))
}

func (m *manager) requestedDiagnosticReadiness(ctx context.Context, filter diagnosticFilter, uris []string) (diagnosticReadiness, error) {
	ready := m.readyDiagnosticSnapshots(filter)
	result := diagnosticReadiness{}
	for _, uri := range uris {
		ref, ok, err := m.awaitableDiagnosticRef(ctx, uri)
		if err != nil {
			return diagnosticReadiness{}, err
		}
		if !ok {
			continue
		}
		result.observe(uri, ready[ref.uri])
	}
	sort.Strings(result.missing)
	return result, nil
}

func (m *manager) readyDiagnosticSnapshots(filter diagnosticFilter) map[string]time.Time {
	ready := make(map[string]time.Time)
	m.forEachCurrentDiagnosticSnapshot(filter, func(snapshot diagnosticSnapshot) {
		if snapshot.state == diagnosticStateReady {
			ready[snapshot.uri] = snapshot.updatedAt
		}
	})
	return ready
}

func (m *manager) awaitableDiagnosticRef(ctx context.Context, uri string) (documentRef, bool, error) {
	ref, err := m.resolveDocumentRef(ctx, uri, "")
	if err != nil {
		return documentRef{}, false, err
	}
	if !m.shouldUseClientForLanguage(ref.languageID) || !fileExists(ref.absPath) {
		return documentRef{}, false, nil
	}
	return ref, true, nil
}

func (r *diagnosticReadiness) observe(uri string, updatedAt time.Time) {
	if updatedAt.IsZero() {
		r.missing = append(r.missing, uri)
		return
	}
	if updatedAt.After(r.latest) {
		r.latest = updatedAt
	}
}

func canonicalDiagnosticURI(uri string) (string, error) {
	path, err := absolutePathFromURI(uri)
	if err != nil {
		return "", err
	}
	return fileURIFromPath(path), nil
}

func uniqueDiagnosticURIs(uris []string) []string {
	out := make([]string, 0, len(uris))
	seen := make(map[string]struct{}, len(uris))
	for _, uri := range uris {
		uri = strings.TrimSpace(uri)
		if uri == "" {
			continue
		}
		if _, ok := seen[uri]; ok {
			continue
		}
		seen[uri] = struct{}{}
		out = append(out, uri)
	}
	return out
}
