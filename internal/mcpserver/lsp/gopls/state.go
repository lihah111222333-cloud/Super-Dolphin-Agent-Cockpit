package gopls

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type bootstrapStatus string

const (
	bootstrapPending       bootstrapStatus = "pending"
	bootstrapBootstrapping bootstrapStatus = "bootstrapping"
	bootstrapReady         bootstrapStatus = "ready"
	bootstrapStale         bootstrapStatus = "stale"
	bootstrapError         bootstrapStatus = "error"
)

type bootstrapAction uint8

const (
	bootstrapActionSkip bootstrapAction = iota
	bootstrapActionWait
	bootstrapActionRun
)

type bootstrapDecision struct {
	action   bootstrapAction
	previous bootstrapStatus
	wait     <-chan struct{}
}

type bootstrapKey struct {
	workspace string
	uri       string
}

type bootstrapEntry struct {
	status      bootstrapStatus
	fingerprint string
	version     int
	err         error
	updatedAt   time.Time
	wait        chan struct{}
}

type bootstrapStateStore struct {
	mu      sync.Mutex
	entries map[bootstrapKey]*bootstrapEntry
}

func newBootstrapStateStore() *bootstrapStateStore {
	return &bootstrapStateStore{entries: map[bootstrapKey]*bootstrapEntry{}}
}

func (s *bootstrapStateStore) restore(workspace string, uris []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, uri := range uris {
		entry := s.entryLocked(bootstrapKey{workspace: workspace, uri: uri})
		if entry.status == bootstrapReady || entry.status == bootstrapStale || entry.status == bootstrapBootstrapping {
			continue
		}
		entry.status = bootstrapPending
		entry.updatedAt = now
	}
}

func (s *bootstrapStateStore) reset(workspace string, uris []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, uri := range uris {
		entry := s.entryLocked(bootstrapKey{workspace: workspace, uri: uri})
		if entry.status == bootstrapBootstrapping {
			continue
		}
		entry.status = bootstrapPending
		entry.fingerprint = ""
		entry.version = 0
		entry.err = nil
		entry.updatedAt = now
	}
}

func (s *bootstrapStateStore) prepare(workspace, uri, fingerprint string) bootstrapDecision {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := bootstrapKey{workspace: workspace, uri: uri}
	entry := s.entryLocked(key)
	previous := entry.status

	switch {
	case entry.status == bootstrapBootstrapping && entry.wait != nil:
		return bootstrapDecision{action: bootstrapActionWait, previous: previous, wait: entry.wait}
	case entry.status == bootstrapReady && entry.fingerprint == fingerprint:
		return bootstrapDecision{action: bootstrapActionSkip, previous: previous}
	case entry.status == bootstrapReady && entry.fingerprint != fingerprint:
		entry.status = bootstrapStale
		previous = bootstrapStale
	}

	entry.status = bootstrapBootstrapping
	entry.err = nil
	entry.updatedAt = time.Now()
	entry.wait = make(chan struct{})
	return bootstrapDecision{action: bootstrapActionRun, previous: previous, wait: entry.wait}
}

func (s *bootstrapStateStore) complete(workspace, uri, fingerprint string, version int) {
	s.finish(workspace, uri, func(entry *bootstrapEntry) {
		entry.status = bootstrapReady
		entry.fingerprint = fingerprint
		entry.version = version
		entry.err = nil
		entry.updatedAt = time.Now()
	})
}

func (s *bootstrapStateStore) fail(workspace, uri string, err error) {
	s.finish(workspace, uri, func(entry *bootstrapEntry) {
		entry.status = bootstrapError
		entry.err = err
		entry.updatedAt = time.Now()
	})
}

func (s *bootstrapStateStore) waitFor(ctx context.Context, workspace, uri string, ch <-chan struct{}) error {
	if ch == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ch:
		s.mu.Lock()
		defer s.mu.Unlock()

		entry := s.entries[bootstrapKey{workspace: workspace, uri: uri}]
		if entry == nil {
			return nil
		}
		if entry.status == bootstrapError && entry.err != nil {
			return entry.err
		}
		return nil
	}
}

func (s *bootstrapStateStore) status(workspace, uri string) bootstrapStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.entries[bootstrapKey{workspace: workspace, uri: uri}]
	if entry == nil {
		return bootstrapPending
	}
	return entry.status
}

func (s *bootstrapStateStore) finish(workspace, uri string, apply func(*bootstrapEntry)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := bootstrapKey{workspace: workspace, uri: uri}
	entry := s.entryLocked(key)
	apply(entry)
	if entry.wait != nil {
		close(entry.wait)
		entry.wait = nil
	}
}

func (s *bootstrapStateStore) entryLocked(key bootstrapKey) *bootstrapEntry {
	if entry := s.entries[key]; entry != nil {
		return entry
	}
	entry := &bootstrapEntry{status: bootstrapPending}
	s.entries[key] = entry
	return entry
}

func (s *bootstrapStateStore) debugString(workspace, uri string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.entries[bootstrapKey{workspace: workspace, uri: uri}]
	if entry == nil {
		return string(bootstrapPending)
	}
	if entry.err == nil {
		return fmt.Sprintf("%s@v%d", entry.status, entry.version)
	}
	return fmt.Sprintf("%s@v%d: %v", entry.status, entry.version, entry.err)
}
