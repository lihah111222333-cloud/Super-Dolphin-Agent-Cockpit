package prompt

import "testing"

func TestSectionCacheStoresNilAndBumpsGeneration(t *testing.T) {
	const sectionName = "cache_test_section"
	cache := newSectionCache()
	generation := cache.Generation()
	if !cache.Store(sectionName, generation, nil) {
		t.Fatal("Store() rejected nil value for current generation")
	}
	value, ok := cache.Lookup(sectionName, generation)
	if !ok {
		t.Fatal("Lookup() missed cached nil value")
	}
	if value != nil {
		t.Fatalf("Lookup() value = %v, want nil", value)
	}

	next := cache.InvalidateSections(sectionName)
	if next != generation+1 {
		t.Fatalf("InvalidateSections() generation = %d, want %d", next, generation+1)
	}
	if _, ok := cache.Lookup(sectionName, generation); ok {
		t.Fatal("Lookup() unexpectedly hit stale generation")
	}
	if _, ok := cache.Lookup(sectionName, next); ok {
		t.Fatal("Lookup() unexpectedly hit after invalidation")
	}
}
