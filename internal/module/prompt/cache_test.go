package prompt

import "testing"

func TestSectionCacheStoresNilAndBumpsGeneration(t *testing.T) {
	cache := newSectionCache()
	generation := cache.Generation()
	if !cache.Store("memory", generation, nil) {
		t.Fatal("Store() rejected nil value for current generation")
	}
	value, ok := cache.Lookup("memory", generation)
	if !ok {
		t.Fatal("Lookup() missed cached nil value")
	}
	if value != nil {
		t.Fatalf("Lookup() value = %v, want nil", value)
	}

	next := cache.InvalidateSections("memory")
	if next != generation+1 {
		t.Fatalf("InvalidateSections() generation = %d, want %d", next, generation+1)
	}
	if _, ok := cache.Lookup("memory", generation); ok {
		t.Fatal("Lookup() unexpectedly hit stale generation")
	}
	if _, ok := cache.Lookup("memory", next); ok {
		t.Fatal("Lookup() unexpectedly hit after invalidation")
	}
}
