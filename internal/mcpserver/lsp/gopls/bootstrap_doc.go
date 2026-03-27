package gopls

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

const (
	maxSiblingBootstrap   = 20
	maxRefreshFiles       = 50
	maxRefreshConcurrency = 8
)

var bootstrapCoordinators sync.Map

type bootstrapCoordinator struct {
	cache  *lspCacheStore
	states *bootstrapStateStore
}

type documentSnapshot struct {
	ref         documentRef
	text        string
	size        int64
	fingerprint string
	modTimeNano int64
}

func (m *manager) bootstrapDocument(ctx context.Context, uri string) error {
	ref, cfg, err := m.bootstrapTarget(uri)
	if err != nil {
		return err
	}
	if !shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	coordinator := bootstrapCoordinatorFor(m)
	if err := coordinator.syncDocument(ctx, m, cfg, ref); err != nil {
		return err
	}
	coordinator.refreshWorkspace(ctx, m, cfg, ref.uri)
	coordinator.bootstrapSiblings(ctx, m, cfg, ref)
	return nil
}

func restoreBootstrappedWorkspace(ctx context.Context, m *manager, cfg workspaceConfig) error {
	coordinator := bootstrapCoordinatorFor(m)
	coordinator.states.reset(cfg.key, coordinator.cache.WorkspaceURIs(cfg.key))
	coordinator.refreshWorkspace(ctx, m, cfg, "")
	return nil
}

func bootstrapCoordinatorFor(m *manager) *bootstrapCoordinator {
	if existing, ok := bootstrapCoordinators.Load(m); ok {
		return existing.(*bootstrapCoordinator)
	}
	created := &bootstrapCoordinator{
		cache:  newLSPCacheStoreFromEnv(m.logger),
		states: newBootstrapStateStore(),
	}
	actual, _ := bootstrapCoordinators.LoadOrStore(m, created)
	return actual.(*bootstrapCoordinator)
}

func closeBootstrapCoordinator(m *manager) {
	if m == nil {
		return
	}
	value, ok := bootstrapCoordinators.LoadAndDelete(m)
	if !ok {
		return
	}
	coordinator := value.(*bootstrapCoordinator)
	coordinator.cache.Close()
}

func (m *manager) bootstrapTarget(uri string) (documentRef, workspaceConfig, error) {
	ref, err := m.resolveDocumentRef(uri, "")
	if err != nil {
		return documentRef{}, workspaceConfig{}, err
	}
	if !shouldUseClientForLanguage(ref.languageID) {
		return ref, workspaceConfig{}, nil
	}
	cfg, err := m.resolveWorkspaceForDocument(ref)
	if err != nil {
		return documentRef{}, workspaceConfig{}, err
	}
	return ref, cfg, nil
}

func (c *bootstrapCoordinator) syncDocument(ctx context.Context, m *manager, cfg workspaceConfig, ref documentRef) error {
	if ref.uri == "" || !shouldUseClientForLanguage(ref.languageID) {
		return nil
	}
	snapshot, err := readDocumentSnapshot(ref)
	if err != nil {
		c.cache.Delete(lspCacheKey{Workspace: cfg.key, Language: ref.languageID, URI: ref.uri})
		c.states.fail(cfg.key, ref.uri, err)
		return err
	}
	return c.syncSnapshot(ctx, m, cfg, snapshot)
}

func (c *bootstrapCoordinator) syncSnapshot(ctx context.Context, m *manager, cfg workspaceConfig, snapshot documentSnapshot) error {
	key := lspCacheKey{Workspace: cfg.key, Language: snapshot.ref.languageID, URI: snapshot.ref.uri}
	decision := c.states.prepare(cfg.key, snapshot.ref.uri, snapshot.fingerprint)
	switch decision.action {
	case bootstrapActionSkip:
		return nil
	case bootstrapActionWait:
		return c.states.waitFor(ctx, cfg.key, snapshot.ref.uri, decision.wait)
	}

	record, cached := c.cache.Load(key)
	client, err := m.ensureClient(ctx, cfg)
	if err != nil {
		c.states.fail(cfg.key, snapshot.ref.uri, err)
		return err
	}
	if m.pool != nil {
		m.pool.acquire(client)
		defer m.pool.release(client)
	}

	version := 1
	if cached && record.Version > 0 {
		version = record.Version + 1
	}
	err = applyBootstrapUpdate(ctx, client, snapshot, decision.previous, cached, version)
	if err != nil {
		c.states.fail(cfg.key, snapshot.ref.uri, err)
		return err
	}

	c.cache.Upsert(lspCacheValue{
		Key:             key,
		Version:         version,
		Fingerprint:     snapshot.fingerprint,
		ModTimeUnixNano: snapshot.modTimeNano,
		Size:            snapshot.size,
	})
	c.states.complete(cfg.key, snapshot.ref.uri, snapshot.fingerprint, version)
	return nil
}

func applyBootstrapUpdate(ctx context.Context, client Client, snapshot documentSnapshot, previous bootstrapStatus, cached bool, version int) error {
	if cached && (previous == bootstrapReady || previous == bootstrapStale) {
		return client.DidChange(ctx, snapshot.ref.uri, version, []protocol.TextDocumentContentChangeEvent{{
			Text: snapshot.text,
		}})
	}
	return client.DidOpen(ctx, snapshot.ref.uri, snapshot.ref.languageID, version, snapshot.text)
}

func (c *bootstrapCoordinator) refreshWorkspace(ctx context.Context, m *manager, cfg workspaceConfig, excludeURI string) {
	records := limitRefreshRecords(c.cache.WorkspaceDocuments(cfg.key))
	if len(records) == 0 {
		return
	}
	c.states.restore(cfg.key, recordsToURIs(records))
	runRefreshTasks(ctx, maxRefreshConcurrency, len(records), func(index int) {
		record := records[index]
		if record.Key.URI == excludeURI {
			return
		}
		ref, err := m.resolveDocumentRef(record.Key.URI, record.Key.Language)
		if err != nil {
			logBootstrapWarning(m, record.Key.URI, err)
			return
		}
		if err := c.syncDocument(ctx, m, cfg, ref); err != nil {
			logBootstrapWarning(m, ref.uri, err)
		}
	})
}

func (c *bootstrapCoordinator) bootstrapSiblings(ctx context.Context, m *manager, cfg workspaceConfig, target documentRef) {
	if target.languageID != "go" {
		return
	}
	refs, err := siblingDocumentRefs(target)
	if err != nil {
		logBootstrapWarning(m, target.uri, err)
		return
	}
	runRefreshTasks(ctx, maxRefreshConcurrency, len(refs), func(index int) {
		if err := c.syncDocument(ctx, m, cfg, refs[index]); err != nil {
			logBootstrapWarning(m, refs[index].uri, err)
		}
	})
}

func readDocumentSnapshot(ref documentRef) (documentSnapshot, error) {
	info, err := os.Stat(ref.absPath)
	if err != nil {
		return documentSnapshot{}, err
	}
	payload, err := os.ReadFile(ref.absPath)
	if err != nil {
		return documentSnapshot{}, err
	}
	return documentSnapshot{
		ref:         ref,
		text:        string(payload),
		size:        info.Size(),
		modTimeNano: info.ModTime().UnixNano(),
		fingerprint: hashDocument(payload),
	}, nil
}

func siblingDocumentRefs(target documentRef) ([]documentRef, error) {
	entries, err := os.ReadDir(filepath.Dir(target.absPath))
	if err != nil {
		return nil, err
	}
	refs := make([]documentRef, 0, maxSiblingBootstrap)
	for _, entry := range entries {
		if entry.IsDir() || len(refs) >= maxSiblingBootstrap {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".go") {
			continue
		}
		path := filepath.Join(filepath.Dir(target.absPath), name)
		if path == target.absPath {
			continue
		}
		absPath, err := normalizeAbsolutePath(path)
		if err != nil {
			return nil, err
		}
		refs = append(refs, documentRef{
			raw:        absPath,
			uri:        fileURIFromPath(absPath),
			absPath:    absPath,
			languageID: "go",
		})
	}
	return refs, nil
}

func runRefreshTasks(ctx context.Context, width, count int, fn func(int)) {
	if count == 0 {
		return
	}
	if width <= 0 {
		width = 1
	}
	sem := make(chan struct{}, width)
	var wg sync.WaitGroup
	for index := 0; index < count; index++ {
		select {
		case <-ctx.Done():
			return
		default:
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			fn(i)
		}(index)
	}
	wg.Wait()
}

func limitRefreshRecords(records []lspCacheValue) []lspCacheValue {
	if len(records) <= maxRefreshFiles {
		return records
	}
	return records[:maxRefreshFiles]
}

func recordsToURIs(records []lspCacheValue) []string {
	uris := make([]string, 0, len(records))
	for _, record := range records {
		uris = append(uris, record.Key.URI)
	}
	return uris
}

func hashDocument(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func logBootstrapWarning(m *manager, uri string, err error) {
	if m == nil || m.logger == nil || err == nil {
		return
	}
	m.logger.Warn("gopls bootstrap skipped document", "uri", uri, "err", err)
}
