package gopls

import (
	"context"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultPoolSize = 10
	maxPoolSize     = 20
)

const lspPoolSizeEnv = "AGENT_LSP_POOL_SIZE"

type ManagerPool struct {
	primary *manager
	size    int

	clones   map[string]*manager
	clonesMu sync.Mutex

	leases   map[Client]int
	leasesMu sync.Mutex

	recycler *poolRecycler
}

type poolManagerSnapshot struct {
	index   int
	manager *manager
}

func NewManagerPool(primary *manager, size int) *ManagerPool {
	pool := &ManagerPool{
		primary: primary,
		size:    clampPoolSize(size),
		clones:  map[string]*manager{},
		leases:  map[Client]int{},
	}
	pool.recycler = newPoolRecycler(pool)
	pool.recycler.Start()
	return pool
}

func PoolSizeFromEnv() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(lspPoolSizeEnv)))
	if err != nil {
		return defaultPoolSize
	}
	return clampPoolSize(value)
}

func (p *ManagerPool) ForAgent(agentID string) Manager {
	if p == nil || p.primary == nil {
		return nil
	}
	index := shardIndex(agentID, p.size)
	if index == 0 {
		return p.primary
	}
	clone, err := p.resolveClone(index, managerRootURI(p.primary))
	if err != nil || clone == nil {
		return p.primary
	}
	p.recycler.TouchShard(index)
	return clone
}

func (p *ManagerPool) Primary() Manager {
	if p == nil {
		return nil
	}
	return p.primary
}

func (p *ManagerPool) Size() int {
	if p == nil {
		return 0
	}
	return p.size
}

func (p *ManagerPool) StopAll() error {
	if p == nil {
		return nil
	}
	if p.recycler != nil {
		p.recycler.Stop()
	}

	p.clonesMu.Lock()
	clones := make([]*manager, 0, len(p.clones))
	for _, clone := range p.clones {
		clones = append(clones, clone)
	}
	clear(p.clones)
	p.clonesMu.Unlock()

	var firstErr error
	for _, clone := range clones {
		if clone == nil {
			continue
		}
		if err := clone.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (p *ManagerPool) Close() error {
	return p.StopAll()
}

func (p *ManagerPool) acquireWorkspace(ctx context.Context, cfg workspaceConfig) (Client, error) {
	manager, index, err := p.managerForWorkspace(cfg)
	if err != nil {
		return nil, err
	}
	if p.recycler != nil {
		p.recycler.TouchShard(index)
	}
	client, err := manager.ensureClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p.acquire(client)
	return client, nil
}

func (p *ManagerPool) acquire(client Client) {
	if p == nil || client == nil {
		return
	}
	p.trackLease(client, 1)
}

func (p *ManagerPool) release(client Client) {
	if p == nil || client == nil {
		return
	}
	p.trackLease(client, -1)
}

func (p *ManagerPool) resolveClone(index int, rootURI string) (*manager, error) {
	if p == nil || p.primary == nil || index <= 0 {
		return p.primary, nil
	}
	key := cloneKey(index, rootURI)

	p.clonesMu.Lock()
	defer p.clonesMu.Unlock()

	if clone := p.clones[key]; clone != nil {
		return clone, nil
	}

	rootPath, err := absolutePathFromURI(rootURI)
	if err != nil {
		return nil, err
	}
	clone := cloneManager(p.primary, rootPath)
	p.clones[key] = clone
	return clone, nil
}

func (p *ManagerPool) managerForWorkspace(cfg workspaceConfig) (*manager, int, error) {
	if p == nil || p.primary == nil {
		return nil, 0, ErrManagerClosed
	}
	index := shardIndex(cfg.key, p.size)
	if index == 0 {
		return p.primary, 0, nil
	}
	clone, err := p.resolveClone(index, cfg.rootURI)
	return clone, index, err
}

func (p *ManagerPool) snapshotManagers() []poolManagerSnapshot {
	if p == nil || p.primary == nil {
		return nil
	}
	snapshots := []poolManagerSnapshot{{index: 0, manager: p.primary}}

	p.clonesMu.Lock()
	defer p.clonesMu.Unlock()

	for key, clone := range p.clones {
		if clone == nil {
			continue
		}
		snapshots = append(snapshots, poolManagerSnapshot{
			index:   parseCloneIndex(key),
			manager: clone,
		})
	}
	return snapshots
}

func (p *ManagerPool) activeLeases(client Client) int {
	p.leasesMu.Lock()
	defer p.leasesMu.Unlock()
	return p.leases[client]
}

func (p *ManagerPool) trackLease(client Client, delta int) {
	if p == nil || client == nil {
		return
	}
	p.leasesMu.Lock()
	defer p.leasesMu.Unlock()

	next := p.leases[client] + delta
	if next <= 0 {
		delete(p.leases, client)
		return
	}
	p.leases[client] = next
}

func clampPoolSize(size int) int {
	switch {
	case size <= 0:
		return defaultPoolSize
	case size > maxPoolSize:
		return maxPoolSize
	default:
		return size
	}
}

func cloneManager(primary *manager, rootPath string) *manager {
	clone := &manager{
		workspaceRoot: rootPath,
		factory:       primary.factory,
		logger:        primary.logger,
		workspaces:    map[string]*workspaceClient{},
		diagnostics:   map[string]diagnosticSnapshot{},
		diagInitial:   primary.diagInitial,
		diagPoll:      primary.diagPoll,
		diagMaxWait:   primary.diagMaxWait,
		pool:          primary.pool,
	}
	clone.diagGeneration.Store(primary.CurrentDiagnosticGeneration())
	return clone
}

func managerRootURI(m *manager) string {
	if m == nil {
		return ""
	}
	root := m.workspaceRoot
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = cwd
		}
	}
	root, _ = normalizeAbsolutePath(root)
	return fileURIFromPath(root)
}

func shardIndex(key string, size int) int {
	if size <= 1 || strings.TrimSpace(key) == "" {
		return 0
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(key))
	return int(hasher.Sum32() % uint32(size))
}

func cloneKey(index int, rootURI string) string {
	return strconv.Itoa(index) + "\x00" + rootURI
}

func parseCloneIndex(key string) int {
	head, _, _ := strings.Cut(key, "\x00")
	index, _ := strconv.Atoi(head)
	return index
}
