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

func TestSpawnLocalConcurrentDoesNotCollide(t *testing.T) {
	helper := installLocalCodexHelper(t, "serve")
	const transportsN = 8

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	type result struct {
		transport *transport
		err       error
	}

	results := make(chan result, transportsN)
	var wg sync.WaitGroup
	for range transportsN {
		wg.Go(func() {
			transport, err := newTransport(ctx, "")
			results <- result{transport: transport, err: err}
		})
	}
	wg.Wait()
	close(results)

	var (
		transports []*transport
		seen       = make(map[string]struct{}, transportsN)
		failures   []string
	)
	for result := range results {
		if result.transport != nil {
			transports = append(transports, result.transport)
		}
		if result.err != nil {
			failures = append(failures, result.err.Error())
			continue
		}
		if !result.transport.local {
			failures = append(failures, "transport.local=false")
		}
		if !result.transport.Running() {
			failures = append(failures, fmt.Sprintf("transport not running: %s", result.transport.serverURL))
		}
		if _, exists := seen[result.transport.serverURL]; exists {
			failures = append(failures, "duplicate serverURL: "+result.transport.serverURL)
			continue
		}
		seen[result.transport.serverURL] = struct{}{}
	}
	t.Cleanup(func() {
		for _, transport := range transports {
			_ = transport.Close()
		}
	})

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
	if len(transports) != transportsN {
		t.Fatalf("successful transports = %d, want %d", len(transports), transportsN)
	}
	if len(seen) != transportsN {
		t.Fatalf("unique serverURLs = %d, want %d", len(seen), transportsN)
	}

	events := waitForHelperEvents(t, helper.logPath, transportsN, 5*time.Second)
	if got := countEvent(events, "initialize"); got != transportsN {
		t.Fatalf("initialize events = %d, want %d; events=%v", got, transportsN, events)
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
