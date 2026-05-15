//go:build !windows

package codexapp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type localTransportSpawnResult struct {
	transport *transport
	err       error
}

func TestSpawnLocalConcurrentDoesNotCollide(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve")
	const transportsN = 8

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	results := spawnLocalTransportsConcurrently(ctx, transportsN)
	transports, seen, failures := collectLocalTransportResults(results, transportsN)
	t.Cleanup(func() {
		for _, transport := range transports {
			_ = transport.Close()
		}
	})

	assertSpawnLocalFailures(t, failures)
	assertSpawnLocalCounts(t, transports, seen, transportsN)

	events := waitForHelperEvents(t, helper.logPath, transportsN, 5*time.Second)
	if got := countEvent(events, "initialize"); got != transportsN {
		t.Fatalf("initialize events = %d, want %d; events=%v", got, transportsN, events)
	}
}

func spawnLocalTransportsConcurrently(ctx context.Context, count int) <-chan localTransportSpawnResult {
	results := make(chan localTransportSpawnResult, count)
	var wg sync.WaitGroup
	for range count {
		wg.Go(func() {
			transport, err := newTransport(ctx, "")
			results <- localTransportSpawnResult{transport: transport, err: err}
		})
	}
	wg.Wait()
	close(results)
	return results
}

func collectLocalTransportResults(results <-chan localTransportSpawnResult, count int) ([]*transport, map[string]struct{}, []string) {
	var transports []*transport
	seen := make(map[string]struct{}, count)
	var failures []string
	for result := range results {
		transports, failures = appendLocalTransportResult(transports, failures, result)
		if result.err == nil && result.transport != nil {
			failures = appendUniqueServerURL(failures, seen, result.transport.serverURL)
		}
	}
	return transports, seen, failures
}

func appendLocalTransportResult(transports []*transport, failures []string, result localTransportSpawnResult) ([]*transport, []string) {
	if result.transport != nil {
		transports = append(transports, result.transport)
	}
	if result.err != nil {
		return transports, append(failures, result.err.Error())
	}
	if !result.transport.local {
		failures = append(failures, "transport.local=false")
	}
	if !result.transport.Running() {
		failures = append(failures, fmt.Sprintf("transport not running: %s", result.transport.serverURL))
	}
	return transports, failures
}

func appendUniqueServerURL(failures []string, seen map[string]struct{}, serverURL string) []string {
	if _, exists := seen[serverURL]; exists {
		return append(failures, "duplicate serverURL: "+serverURL)
	}
	seen[serverURL] = struct{}{}
	return failures
}

func assertSpawnLocalFailures(t *testing.T, failures []string) {
	t.Helper()
	for _, failure := range failures {
		if strings.Contains(failure, "use of closed network connection") {
			t.Fatalf("spawnLocal hit closed network connection race: %v", failures)
		}
		if strings.Contains(failure, "address already in use") {
			t.Fatalf("spawnLocal hit address reuse race: %v", failures)
		}
	}
	if len(failures) > 0 {
		t.Fatalf("spawnLocal concurrent failures: %v", failures)
	}
}

func assertSpawnLocalCounts(t *testing.T, transports []*transport, seen map[string]struct{}, want int) {
	t.Helper()
	if len(transports) != want {
		t.Fatalf("successful transports = %d, want %d", len(transports), want)
	}
	if len(seen) != want {
		t.Fatalf("unique serverURLs = %d, want %d", len(seen), want)
	}
}

func TestReserveServerURLUniqueUnderContention(t *testing.T) {
	const reservations = 500

	var (
		seen     sync.Map
		wg       sync.WaitGroup
		errCh    = make(chan error, reservations)
		releaseM sync.Mutex
		releases []func()
	)
	t.Cleanup(func() {
		releaseM.Lock()
		defer releaseM.Unlock()
		for _, release := range releases {
			release()
		}
	})

	for range reservations {
		wg.Go(func() {
			serverURL, release, err := reserveServerURL()
			if err != nil {
				errCh <- err
				return
			}
			releaseM.Lock()
			releases = append(releases, release)
			releaseM.Unlock()
			if _, loaded := seen.LoadOrStore(serverURL, struct{}{}); loaded {
				errCh <- fmt.Errorf("duplicate serverURL reserved: %s", serverURL)
			}
		})
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	var count int
	seen.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != reservations {
		t.Fatalf("unique reserved serverURLs = %d, want %d", count, reservations)
	}
}

func countEvent(events []string, want string) int {
	count := 0
	for _, event := range events {
		if event == want {
			count++
		}
	}
	return count
}
