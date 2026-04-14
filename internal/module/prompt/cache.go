package prompt

import (
	"strings"
	"sync"
)

type sectionCache struct {
	mu         sync.RWMutex
	generation uint64
	values     map[string]*string
}

func newSectionCache() *sectionCache {
	return &sectionCache{values: map[string]*string{}}
}

func (c *sectionCache) Generation() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generation
}

func (c *sectionCache) Lookup(name string, generation uint64) (*string, bool) {
	key := strings.TrimSpace(name)
	if key == "" {
		return nil, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if generation != c.generation {
		return nil, false
	}
	value, ok := c.values[key]
	if !ok {
		return nil, false
	}
	return cloneStringPtr(value), true
}

func (c *sectionCache) Store(name string, generation uint64, value *string) bool {
	key := strings.TrimSpace(name)
	if key == "" {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation {
		return false
	}
	c.values[key] = cloneStringPtr(value)
	return true
}

func (c *sectionCache) InvalidateAll(_ InvalidateReason) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.values = map[string]*string{}
	return c.generation
}

func (c *sectionCache) InvalidateSections(names ...string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	for _, name := range names {
		delete(c.values, strings.TrimSpace(name))
	}
	return c.generation
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
