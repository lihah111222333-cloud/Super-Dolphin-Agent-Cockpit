package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestSemanticCallDeduperSharesInFlightResult(t *testing.T) {
	deduper := newSemanticCallDeduper()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	var group errgroup.Group
	group.Go(func() error {
		result, err := deduper.do(context.Background(), "same-key", func() (any, error) {
			calls.Add(1)
			close(started)
			<-release
			return "shared", nil
		})
		if err != nil || result != "shared" {
			return fmt.Errorf("leader result=%v err=%v", result, err)
		}
		return nil
	})
	<-started
	group.Go(func() error {
		result, err := deduper.do(context.Background(), "same-key", func() (any, error) {
			return nil, errors.New("duplicate execution")
		})
		if err != nil || result != "shared" {
			return fmt.Errorf("waiter result=%v err=%v", result, err)
		}
		return nil
	})
	deadline := time.Now().Add(time.Second)
	for deduper.waiterCount("same-key") == 0 && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if deduper.waiterCount("same-key") == 0 {
		t.Fatal("waiter did not register before leader release")
	}
	close(release)
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("underlying calls = %d, want one in-flight execution", got)
	}
}

func TestSemanticCallKeyNormalizesArgsAndInvalidatesTargetOrGeneration(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sample.go")
	if err := os.WriteFile(target, []byte("package sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := &diagnosticsTestRegistry{generation: 1}
	first := requireSemanticCallKey(t, root, registry, `{"file_path":"sample.go","action":"document_symbol"}`)
	second := requireSemanticCallKey(t, root, registry, `{"action":"document_symbol","file_path":"sample.go"}`)
	if first != second {
		t.Fatalf("normalized semantic keys first=%q second=%q, want equal", first, second)
	}
	registry.generation = 2
	third := requireSemanticCallKey(t, root, registry, `{"file_path":"sample.go","action":"document_symbol"}`)
	if third == first {
		t.Fatalf("generation change key=%q first=%q, want invalidation", third, first)
	}
	if err := os.WriteFile(target, []byte("package changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fourth := requireSemanticCallKey(t, root, registry, `{"file_path":"sample.go","action":"document_symbol"}`)
	if fourth == third {
		t.Fatalf("target change key=%q third=%q, want invalidation", fourth, third)
	}
}

func requireSemanticCallKey(t *testing.T, root string, registry *diagnosticsTestRegistry, params string) string {
	t.Helper()
	key, eligible, err := buildSemanticCallKey(testToolContext(root), "structure", []byte(params), registry)
	if err != nil || !eligible {
		t.Fatalf("semantic key=%q eligible=%t err=%v, want eligible key", key, eligible, err)
	}
	return key
}
