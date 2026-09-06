package accesswait

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

// TestAccessWaitOptionalWatcherSeam verifies that Wait registers only the
// fixed canonical source/lane and still closes the advisory subscription on a
// terminal durable result.
func TestAccessWaitOptionalWatcherSeam(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	grantRequest := validGrantRequest("grant-watcher-seam", "grant/watcher-seam", strings.Repeat("a", 64))
	mustCreateGrant(t, fixture.store, grantRequest)
	selector := waitSelector()
	handle := mustCreateWait(t, fixture.store, waitRequest(grantRequest, "wait-watcher-seam", selector, "wait/watcher-seam", strings.Repeat("b", 64), strings.Repeat("c", 64)))
	fixture.events.events = append(fixture.events.events, matchingWaitEvent(selector))
	watcher, err := eventstore.NewWatcher(eventstore.WatcherConfig{BufferSize: 1, MaxRegistrations: 2})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	store, err := NewStoreWithWatcher(fixture.db, fixture.identity, fixture.events, watcher)
	if err != nil {
		t.Fatalf("NewStoreWithWatcher() error = %v", err)
	}
	result, err := store.Wait(t.Context(), PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: handle.CallerPrincipalRef, ScopeRef: handle.ScopeRef, GrantRevision: handle.GrantRevision})
	if err != nil || result.Status != "satisfied" || result.Event == nil {
		t.Fatalf("Wait() = %#v err=%v, want satisfied event", result, err)
	}
	if result.Event.SourceID != selector.SourceID || result.Event.SourceEpoch != selector.SourceEpoch || result.Event.Lane != selector.Lane {
		t.Fatalf("Wait() event source = %#v, want selector %#v", result.Event, selector)
	}
	if watcher.RegistrationCount() != 0 {
		t.Fatalf("watcher registrations after terminal Wait = %d, want 0", watcher.RegistrationCount())
	}
	watcher.Close()
}

// TestAccessWaitWatcherConstructorRejectsTypedNil ensures an optional seam
// cannot silently degrade when a typed-nil watcher crosses the owner boundary.
func TestAccessWaitWatcherConstructorRejectsTypedNil(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	var typedNil *eventstore.Watcher
	if store, err := NewStoreWithWatcher(fixture.db, fixture.identity, fixture.events, typedNil); store != nil || err == nil {
		t.Fatalf("NewStoreWithWatcher(typed nil) = store=%#v err=%v, want fail-fast", store, err)
	}
}
