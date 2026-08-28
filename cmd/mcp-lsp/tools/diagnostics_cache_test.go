package tools

import (
	"fmt"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestDiagnosticsCacheKeyNormalizesRequestAndInvalidatesContentOrGeneration(t *testing.T) {
	firstArgs, secondArgs := equivalentDiagnosticsCacheArguments()
	first := requireDiagnosticsCacheKey(t, firstArgs, "digest-1", "server-1", "first")
	second := requireDiagnosticsCacheKey(t, secondArgs, "digest-1", "server-1", "second")
	assertEquivalentDiagnosticsCacheKeys(t, first, second)
	assertInvalidatedDiagnosticsCacheKeys(t, first, secondArgs)
}

func equivalentDiagnosticsCacheArguments() (map[string]any, map[string]any) {
	first := map[string]any{
		"file_paths": []any{"b.py", "a.py"},
		"diff_filter": map[string]any{
			"mode":          "changed_lines",
			"changed_lines": map[string]any{"b.py": []any{4.0, 2.0}, "a.py": []any{3.0, 1.0}},
		},
	}
	second := map[string]any{
		"diff_filter": map[string]any{
			"changed_lines": map[string]any{"a.py": []any{1.0, 3.0}, "b.py": []any{2.0, 4.0}},
			"mode":          "changed_lines",
		},
		"file_paths": []any{"a.py", "b.py"},
	}
	return first, second
}

func requireDiagnosticsCacheKey(t *testing.T, arguments map[string]any, digest, generation, label string) DiagnosticsCacheKey {
	t.Helper()
	key, err := BuildDiagnosticsCacheKey(arguments, digest, generation)
	if err != nil {
		t.Fatalf("BuildDiagnosticsCacheKey(%s) error = %v", label, err)
	}
	return key
}

func assertEquivalentDiagnosticsCacheKeys(t *testing.T, first, second DiagnosticsCacheKey) {
	t.Helper()
	if first != second {
		t.Fatalf("normalized cache keys differ: first=%+v second=%+v", first, second)
	}
	if got := DecideDiagnosticsCache(&first, second); !got.Reuse || got.Reason != "cache_hit" {
		t.Fatalf("cache decision = %+v, want cache_hit", got)
	}
	if got := DecideDiagnosticsCache(nil, second); got.Reuse || got.Reason != "no_cached_result" {
		t.Fatalf("cold cache decision = %+v, want no_cached_result", got)
	}
}

func assertInvalidatedDiagnosticsCacheKeys(t *testing.T, first DiagnosticsCacheKey, arguments map[string]any) {
	t.Helper()
	changedDigest := requireDiagnosticsCacheKey(t, arguments, "digest-2", "server-1", "changed digest")
	if got := DecideDiagnosticsCache(&first, changedDigest); got.Reuse || got.Reason != "target_file_digest_changed" {
		t.Fatalf("digest cache decision = %+v, want target_file_digest_changed", got)
	}
	changedGeneration := requireDiagnosticsCacheKey(t, arguments, "digest-1", "server-2", "changed generation")
	if got := DecideDiagnosticsCache(&first, changedGeneration); got.Reuse || got.Reason != "server_generation_changed" {
		t.Fatalf("generation cache decision = %+v, want server_generation_changed", got)
	}
}

func TestDiagnosticsResultCacheUsesBoundedDeterministicLRU(t *testing.T) {
	cache := newDiagnosticsResultCache()
	cache.capacity = 2
	first := testDiagnosticsCacheKey("first")
	second := testDiagnosticsCacheKey("second")
	third := testDiagnosticsCacheKey("third")
	cache.put(first, diagnosticsCacheValue{Message: "first"})
	cache.put(second, diagnosticsCacheValue{Message: "second"})
	if _, ok := cache.get(first); !ok {
		t.Fatal("first cache entry missing before eviction")
	}
	cache.put(third, diagnosticsCacheValue{Message: "third"})
	if _, ok := cache.get(first); !ok {
		t.Fatal("recently used first entry was evicted")
	}
	if _, ok := cache.get(second); ok {
		t.Fatal("least recently used second entry was retained")
	}
	if _, ok := cache.get(third); !ok {
		t.Fatal("new third entry missing after eviction")
	}
	if got := cache.size(); got != 2 {
		t.Fatalf("cache size = %d, want capacity 2", got)
	}
}

func TestDiagnosticsResultCacheConcurrentAccessRespectsCapacity(t *testing.T) {
	cache := newDiagnosticsResultCache()
	cache.capacity = 8
	var group errgroup.Group
	for worker := 0; worker < 32; worker++ {
		worker := worker
		group.Go(func() error { return exerciseDiagnosticsCache(cache, worker) })
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent cache exercise: %v", err)
	}
	if got := cache.size(); got > cache.capacity {
		t.Fatalf("concurrent cache size = %d, exceeds capacity %d", got, cache.capacity)
	}
}

func exerciseDiagnosticsCache(cache *diagnosticsResultCache, worker int) error {
	for iteration := 0; iteration < 100; iteration++ {
		key := testDiagnosticsCacheKey(fmt.Sprintf("worker-%d-%d", worker, iteration%16))
		cache.put(key, diagnosticsCacheValue{Message: key.NormalizedArgs})
		cache.get(key)
	}
	return nil
}

func testDiagnosticsCacheKey(value string) DiagnosticsCacheKey {
	return DiagnosticsCacheKey{
		NormalizedArgs:   value,
		TargetFileDigest: "digest",
		ServerGeneration: "generation",
	}
}
