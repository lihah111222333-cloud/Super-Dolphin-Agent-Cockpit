package mcpcontrol

import (
	"errors"
	"sync"
)

// ErrManagedLeaseStale 表示 concrete lease 已被 replacement 或 sweeper 撤销。
var ErrManagedLeaseStale = errors.New("managed MCP lease is stale")

type leaseRuntime struct {
	mu      sync.Mutex
	peer    Peer
	current bool
	refs    int
	closed  bool
}

func newLeaseRuntime(peer Peer) *leaseRuntime {
	return &leaseRuntime{peer: peer, current: true}
}

// LeasePin 固定一次 concrete lease callback；replacement 后结果必须通过 Current 再提交。
type LeasePin struct {
	runtime *leaseRuntime
	peer    Peer
	once    sync.Once
}

// Peer 返回 pin 捕获的 immutable peer。
func (p *LeasePin) Peer() Peer {
	if p == nil {
		return nil
	}
	return p.peer
}

// Current 判断 pin 对应 generation 是否仍是当前 authority。
func (p *LeasePin) Current() bool {
	if p == nil || p.runtime == nil {
		return false
	}
	p.runtime.mu.Lock()
	defer p.runtime.mu.Unlock()
	return p.runtime.current
}

// Release 释放引用；若 lease 已撤销且没有其他 pin，则在 registry 锁外关闭 peer。
func (p *LeasePin) Release() error {
	if p == nil || p.runtime == nil {
		return nil
	}
	var closeErr error
	p.once.Do(func() {
		var peer Peer
		p.runtime.mu.Lock()
		if p.runtime.refs > 0 {
			p.runtime.refs--
		}
		if !p.runtime.current && p.runtime.refs == 0 && !p.runtime.closed {
			p.runtime.closed = true
			peer = p.runtime.peer
		}
		p.runtime.mu.Unlock()
		if peer != nil {
			closeErr = peer.Close()
		}
	})
	return closeErr
}

// Pin 固定 ToolInstance 的 concrete lease。
func (i *ToolInstance) Pin() (*LeasePin, error) {
	if i == nil || i.runtime == nil {
		return nil, ErrManagedLeaseStale
	}
	return i.runtime.pin()
}

func (r *leaseRuntime) pin() (*LeasePin, error) {
	if r == nil {
		return nil, ErrManagedLeaseStale
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.current || r.closed || r.peer == nil {
		return nil, ErrManagedLeaseStale
	}
	r.refs++
	return &LeasePin{runtime: r, peer: r.peer}, nil
}

func (r *leaseRuntime) retire() Peer {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = false
	if r.refs != 0 || r.closed {
		return nil
	}
	r.closed = true
	return r.peer
}
