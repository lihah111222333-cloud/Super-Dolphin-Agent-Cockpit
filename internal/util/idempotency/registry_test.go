package idempotency

import (
	"errors"
	"sync"
	"testing"
)

func TestRegistryReplaysSuccessfulResult(t *testing.T) {
	var registry Registry[string]
	calls := 0

	first, err := registry.Do("launch-key", "same-params", func() (string, error) {
		calls++
		return "agent-1", nil
	})
	if err != nil {
		t.Fatalf("first Do() error = %v", err)
	}
	second, err := registry.Do("launch-key", "same-params", func() (string, error) {
		calls++
		return "agent-2", nil
	})
	if err != nil {
		t.Fatalf("second Do() error = %v", err)
	}

	if first != "agent-1" || second != "agent-1" {
		t.Fatalf("Do() = %q then %q, want cached agent-1", first, second)
	}
	if calls != 1 {
		t.Fatalf("fn calls = %d, want 1", calls)
	}
}

func TestRegistryDoesNotCacheFailures(t *testing.T) {
	var registry Registry[string]
	fail := errors.New("partial launch failed")
	calls := 0

	_, err := registry.Do("launch-key", "same-params", func() (string, error) {
		calls++
		return "", fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("first Do() error = %v, want %v", err, fail)
	}
	got, err := registry.Do("launch-key", "same-params", func() (string, error) {
		calls++
		return "agent-1", nil
	})
	if err != nil {
		t.Fatalf("second Do() error = %v", err)
	}

	if got != "agent-1" {
		t.Fatalf("second Do() = %q, want agent-1", got)
	}
	if calls != 2 {
		t.Fatalf("fn calls = %d, want retry after failure", calls)
	}
}

func TestRegistryReplaysRetainedFailures(t *testing.T) {
	var registry Registry[string]
	fail := errors.New("cleanup failed")
	calls := 0

	for i := 0; i < 2; i++ {
		_, err := registry.Do("launch-key", "same-params", func() (string, error) {
			calls++
			return "", Retain(fail)
		})
		if !errors.Is(err, fail) {
			t.Fatalf("Do() error = %v, want %v", err, fail)
		}
	}

	if calls != 1 {
		t.Fatalf("fn calls = %d, want retained failure replay", calls)
	}
}

func TestRegistryMappedErrorFindsRetainedResult(t *testing.T) {
	var registry Registry[string]
	var mapping sync.Map
	if _, err := registry.Do("launch-key", "same-params", func() (string, error) {
		return "agent-1", nil
	}); err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	mapping.Store("thread-1", "launch-key")
	fail := errors.New("cleanup failed")
	if !RetainMappedError(&mapping, &registry, " thread-1 ", Retain(fail)) {
		t.Fatal("RetainMappedError() = false, want retained mapping")
	}
	err, ok := MappedError(&mapping, &registry, "thread-1")
	if !ok || !errors.Is(err, fail) {
		t.Fatalf("MappedError() = %v, %v; want retained cleanup failure", err, ok)
	}
}

func TestRegistryForgetAllowsFreshResult(t *testing.T) {
	var registry Registry[string]
	if _, err := registry.Do("launch-key", "same-params", func() (string, error) {
		return "agent-1", nil
	}); err != nil {
		t.Fatalf("first Do() error = %v", err)
	}
	registry.Forget("launch-key")
	got, err := registry.Do("launch-key", "same-params", func() (string, error) {
		return "agent-2", nil
	})
	if err != nil {
		t.Fatalf("second Do() error = %v", err)
	}
	if got != "agent-2" {
		t.Fatalf("second Do() = %q, want fresh result", got)
	}
}
