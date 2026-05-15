package multilsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

const (
	defaultRecyclerInterval = 30 * time.Second
	defaultRSSLimitBytes    = 768 * 1024 * 1024
	lspRSSLimitEnv          = "AGENT_LSP_RSS_LIMIT_MB"
)

// poolRecycler is the background loop that periodically scans managed
// LSP server processes and recycles ones whose RSS exceeds the configured
// limit. Pre-P22 P2 it spun itself up from NewManagerPool via a
// self-owned goroutine + stopCh pair; it is now a plain
// platformrunner.Runner owned by the root `group:"runners"` bridge.
// See docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md §480-494.
type poolRecycler struct {
	pool     *ManagerPool
	interval time.Duration

	mu         sync.Mutex
	lastActive map[int]time.Time
}

// Compile-time assertion: poolRecycler satisfies the Runner contract
// consumed by the `group:"runners"` aggregation.
var _ platformrunner.Runner = (*poolRecycler)(nil)

func newPoolRecycler(pool *ManagerPool) *poolRecycler {
	return &poolRecycler{
		pool:       pool,
		interval:   defaultRecyclerInterval,
		lastActive: map[int]time.Time{},
	}
}

func (r *poolRecycler) TouchShard(index int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.lastActive[index] = time.Now()
	r.mu.Unlock()
}

// Run drives the recycler check loop until ctx is cancelled. A nil
// receiver is accepted as a no-op so callers that rely on
// ManagerPool.RecyclerRunner() without a pool still wire cleanly.
func (r *poolRecycler) Run(ctx context.Context) error {
	if r == nil {
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.check()
		}
	}
}

func (r *poolRecycler) check() {
	if r == nil || r.pool == nil {
		return
	}
	for _, snapshot := range r.pool.snapshotManagers() {
		r.checkManager(snapshot.index, snapshot.manager)
	}
}

func (r *poolRecycler) checkManager(index int, mgr *manager) {
	if mgr == nil || managerIsClosed(mgr) {
		return
	}
	if !r.shouldCheck(index) {
		return
	}

	for _, workspace := range snapshotWorkspaceClients(mgr) {
		r.recycleIfNeeded(mgr, workspace)
	}
}

func (r *poolRecycler) recycleIfNeeded(mgr *manager, workspace workspaceClient) {
	rssBytes, pid, err := clientRSSBytes(workspace.client)
	if err != nil || pid <= 0 || rssBytes <= rssLimitBytes() {
		return
	}
	if r.pool.activeLeases(workspace.client) > 0 {
		return
	}
	if mgr.logger != nil {
		mgr.logger.Warn("recycling LSP process", "workspace", workspace.key, "pid", pid, "rss_bytes", rssBytes)
	}
	if err := recycleWorkspaceClient(mgr, workspace); err != nil && mgr.logger != nil {
		mgr.logger.Warn("LSP recycle failed", "workspace", workspace.key, "pid", pid, "err", err)
	}
}

func (r *poolRecycler) shouldCheck(index int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	last := r.lastActive[index]
	return last.IsZero() || time.Since(last) >= r.interval/2
}

func recycleWorkspaceClient(mgr *manager, workspace workspaceClient) error {
	detached := detachWorkspaceClient(mgr, workspace.key)
	if detached == nil || detached.client == nil {
		return nil
	}
	mgr.AdvanceDiagnosticGeneration()

	ctx, cancel := platformconfig.WithTimeout(context.Background(), managerShutdownTimeout)
	shutdownErr := detached.client.Shutdown(ctx)
	cancel()
	closeErr := detached.client.Close()

	languageID := workspace.languageID
	if languageID == "" {
		languageID = "go"
	}
	cfg := workspaceConfig{
		key:              workspace.key,
		rootPath:         workspace.rootPath,
		rootURI:          workspace.rootURI,
		languageID:       languageID,
		env:              append([]string(nil), workspace.env...),
		workspaceFolders: cloneWorkspaceFolders(workspace.workspaceFolders),
	}
	_, ensureErr := mgr.ensureClient(context.Background(), cfg)
	restoreErr := restoreBootstrappedWorkspace(context.Background(), mgr, cfg)
	return errors.Join(shutdownErr, closeErr, ensureErr, restoreErr)
}

func snapshotWorkspaceClients(mgr *manager) []workspaceClient {
	if mgr == nil {
		return nil
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	items := make([]workspaceClient, 0, len(mgr.workspaces))
	for _, workspace := range mgr.workspaces {
		if workspace == nil || workspace.client == nil {
			continue
		}
		items = append(items, *workspace)
	}
	return items
}

func detachWorkspaceClient(mgr *manager, key string) *workspaceClient {
	if mgr == nil {
		return nil
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	workspace := mgr.workspaces[key]
	delete(mgr.workspaces, key)
	return workspace
}

func managerIsClosed(mgr *manager) bool {
	if mgr == nil {
		return true
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.closed
}

func clientRSSBytes(current Client) (uint64, int, error) {
	typed, ok := current.(*client)
	if !ok || typed.transport == nil || typed.transport.cmd == nil || typed.transport.cmd.Process == nil {
		return 0, 0, nil
	}
	pid := typed.transport.cmd.Process.Pid
	rss, err := processRSSBytes(pid)
	return rss, pid, err
}

func processRSSBytes(pid int) (uint64, error) {
	switch runtime.GOOS {
	case "linux":
		return linuxRSSBytes(pid)
	case "darwin":
		return psRSSBytes(pid)
	default:
		return 0, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func linuxRSSBytes(pid int) (uint64, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(payload))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected statm payload for pid %d", pid)
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * uint64(os.Getpagesize()), nil
}

func psRSSBytes(pid int) (uint64, error) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return 0, nil
	}
	kilobytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return kilobytes * 1024, nil
}

func rssLimitBytes() uint64 {
	value, err := strconv.ParseUint(strings.TrimSpace(os.Getenv(lspRSSLimitEnv)), 10, 64)
	if err != nil || value == 0 {
		return defaultRSSLimitBytes
	}
	return value * 1024 * 1024
}
