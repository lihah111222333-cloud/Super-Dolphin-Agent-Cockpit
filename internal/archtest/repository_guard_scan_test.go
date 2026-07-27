package archtest

import (
	"sync/atomic"
	"testing"
)

func TestRepositoryGuardScanCacheRunsMatchingScopeOnceAndReturnsCopies(t *testing.T) {
	cache := &RepositoryGuardScanCache{}
	var calls atomic.Int32
	check := func(CheckOptions) []Violation {
		calls.Add(1)
		return []Violation{{File: "internal/example.go", Message: "fixture"}}
	}
	opts := CheckOptions{RepoRoot: t.TempDir(), ScanRoots: []string{"internal"}}

	first, err := checkRepositoryGuardsOnce(cache, opts, check)
	if err != nil {
		t.Fatal(err)
	}
	first[0].Message = "mutated"
	second, err := checkRepositoryGuardsOnce(cache, opts, check)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("repository scan calls = %d, want 1", calls.Load())
	}
	if second[0].Message != "fixture" {
		t.Fatalf("cached result was mutable: %#v", second)
	}
}

func TestRepositoryGuardScanCacheIsRequired(t *testing.T) {
	_, err := CheckRepositoryGuardsOnce(nil, CheckOptions{RepoRoot: t.TempDir()})
	if err == nil {
		t.Fatal("CheckRepositoryGuardsOnce() error = nil, want missing cache error")
	}
}
