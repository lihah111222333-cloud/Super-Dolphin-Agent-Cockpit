package gopls

import (
	"os"
	"strconv"
	"strings"
	"sync"

	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

const (
	defaultPoolSize = 10
	maxPoolSize     = 20
)

const lspPoolSizeEnv = "AGENT_LSP_POOL_SIZE"

type ManagerPool struct {
	primary *manager
	size    int

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
		leases:  map[Client]int{},
	}
	// P22 P2 gopls-S1: do not start the recycler loop from the
	// constructor. The root `group:"runners"` aggregation owns it via
	// RecyclerRunner() below so shutdown is driven by ctx cancellation.
	pool.recycler = newPoolRecycler(pool)
	return pool
}

// RecyclerRunner exposes the pool's background recycler as a
// platformrunner.Runner. The root bridge collects recyclers from each
// language pool and feeds them into `group:"runners"`.
func (p *ManagerPool) RecyclerRunner() platformrunner.Runner {
	if p == nil {
		return nil
	}
	return p.recycler
}

func PoolSizeFromEnv() int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(lspPoolSizeEnv)))
	if err != nil {
		return defaultPoolSize
	}
	return clampPoolSize(value)
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

// StopAll is retained as the *ManagerPool shutdown hook for non-runner
// resources (leases bookkeeping, etc.). Recycler lifecycle is now owned
// by the root runner group via ctx cancellation, so this method is a
// no-op for the recycler; callers that previously relied on StopAll
// halting the loop must now cancel the runner context instead.
func (p *ManagerPool) StopAll() error {
	return nil
}

func (p *ManagerPool) Close() error {
	return p.StopAll()
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

func (p *ManagerPool) snapshotManagers() []poolManagerSnapshot {
	if p == nil || p.primary == nil {
		return nil
	}
	return []poolManagerSnapshot{{index: 0, manager: p.primary}}
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
