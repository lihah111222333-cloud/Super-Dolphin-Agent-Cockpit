package accesswait

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestListDueGrantCandidatesUsesSecurityTimeAndHardLimit(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	securityNow := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond)
	seedDueGrantCandidates(t, fixture.store, securityNow)
	assertDueGrantCandidates(t, fixture.store, securityNow)
}

// seedDueGrantCandidates 准备覆盖到期排序与边界限制的候选授权。
func seedDueGrantCandidates(t *testing.T, store *Store, securityNow time.Time) {
	t.Helper()
	for index, offset := range []time.Duration{-4 * time.Second, -2 * time.Second, 0, 2 * time.Second} {
		request := expiryCandidateRequest(
			"grant-expiry-list-"+string(rune('a'+index)),
			securityNow.Add(-time.Hour), securityNow.Add(offset), byte('a'+index),
		)
		mustCreateGrant(t, store, request)
	}
}

// assertDueGrantCandidates 校验候选授权按到期时间排序并遵守查询上限。
func assertDueGrantCandidates(t *testing.T, store *Store, securityNow time.Time) {
	t.Helper()
	candidates, err := store.ListDueGrantCandidates(t.Context(), securityNow, 2)
	if err != nil {
		t.Fatalf("ListDueGrantCandidates() error = %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d, want bounded 2", len(candidates))
	}
	if candidates[0].GrantID != "grant-expiry-list-a" || candidates[1].GrantID != "grant-expiry-list-b" {
		t.Fatalf("candidate order = %#v, want earliest expiry order", candidates)
	}
	if !candidates[0].ExpiresAt.Before(candidates[1].ExpiresAt) {
		t.Fatalf("candidate expiry order = %v then %v, want ascending", candidates[0].ExpiresAt, candidates[1].ExpiresAt)
	}
	assertDueCandidateRows(t, candidates, securityNow)
	assertDueCandidateBounds(t, store, securityNow)
}

// assertDueCandidateRows 校验候选授权版本与安全时间过滤结果。
func assertDueCandidateRows(t *testing.T, candidates []GrantExpiryCandidate, securityNow time.Time) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.ExpectedRevision != 1 || candidate.ExpiresAt.After(securityNow) {
			t.Fatalf("candidate = %#v, want active revision 1 due at security time", candidate)
		}
	}
}

// assertDueCandidateBounds 校验候选查询的单行与硬上限约束。
func assertDueCandidateBounds(t *testing.T, store *Store, securityNow time.Time) {
	t.Helper()
	if got, err := store.ListDueGrantCandidates(t.Context(), securityNow, 1); err != nil || len(got) != 1 {
		t.Fatalf("single-row bounded query = %#v err=%v, want one row", got, err)
	}
	if _, err := store.ListDueGrantCandidates(t.Context(), securityNow, DefaultGrantExpiryBatchLimit+1); err == nil {
		t.Fatal("oversized expiry candidate limit unexpectedly accepted")
	}
}

func TestListDueGrantCandidatesNormalizesTimestampPrecision(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	// SQLite stores canonical UTC text in either second or fractional-second
	// precision. Numeric julianday comparison must not let a trailing `Z` sort
	// after a fractional timestamp and hide an already due grant.
	securityNow := time.Date(2026, 9, 3, 0, 0, 0, 500*int(time.Millisecond), time.UTC)
	expiresAt := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	request := expiryCandidateRequest("grant-expiry-precision", securityNow.Add(-time.Hour), expiresAt, 'c')
	store, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return securityNow })
	if err != nil {
		t.Fatal(err)
	}
	mustCreateGrant(t, store, request)
	candidates, err := store.ListDueGrantCandidates(t.Context(), securityNow, 1)
	if err != nil || len(candidates) != 1 || !candidates[0].ExpiresAt.Equal(expiresAt) {
		t.Fatalf("precision-normalized candidates = %#v err=%v, want exact-second expiry", candidates, err)
	}
}

func TestExpireDueGrantsUsesExactRevisionAndExpiryTime(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	securityNow := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond)
	// Deliberately make the legacy Store clock stale. ExpireDueGrants must bind
	// ExpireGrant to the explicit securityNow snapshot rather than this clock.
	expiresAt := securityNow.Add(-time.Second)
	store, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return expiresAt })
	if err != nil {
		t.Fatalf("clocked store: %v", err)
	}
	grant := mustCreateGrant(t, store, expiryCandidateRequest("grant-expiry-exact", securityNow.Add(-time.Hour), expiresAt, 'e'))

	expired, err := store.ExpireDueGrants(t.Context(), securityNow, 8)
	if err != nil || expired != 1 {
		t.Fatalf("ExpireDueGrants() = count %d err %v, want one winner", expired, err)
	}
	var status string
	var revision int64
	var updatedAt string
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT status, revision, updated_at FROM orchestration_access_grants WHERE grant_id = ?`, grant.GrantID).Scan(&status, &revision, &updatedAt); err != nil {
		t.Fatalf("read expired grant: %v", err)
	}
	if status != "expired" || revision != 2 || updatedAt != utcText(expiresAt) {
		t.Fatalf("expired grant row = status %q revision %d updated_at %q, want expired/2/%q", status, revision, updatedAt, utcText(expiresAt))
	}
	if second, err := store.ExpireDueGrants(t.Context(), securityNow, 8); err != nil || second != 0 {
		t.Fatalf("repeated expiry pass = count %d err %v, want zero side effects", second, err)
	}
}

func TestExpireDueGrantsCancellationAndMalformedClockFailFast(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	securityNow := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond)
	store, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return securityNow })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if count, err := store.ExpireDueGrants(ctx, securityNow, 1); !errors.Is(err, context.Canceled) || count != 0 {
		t.Fatalf("cancelled expiry pass = count %d err %v, want context.Canceled/0", count, err)
	}
	for _, invalid := range []time.Time{time.Time{}, securityNow.In(time.FixedZone("non-utc", 0))} {
		if _, err := store.ListDueGrantCandidates(t.Context(), invalid, 1); err == nil {
			t.Fatalf("invalid security time %v unexpectedly accepted", invalid)
		}
	}
	for _, limit := range []int64{0, -1, DefaultGrantExpiryBatchLimit + 1} {
		if _, err := store.ListDueGrantCandidates(t.Context(), securityNow, limit); err == nil {
			t.Fatalf("invalid expiry limit %d unexpectedly accepted", limit)
		}
	}
}

func TestExpireDueGrantsConcurrentCASHasSingleWinner(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	securityNow := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond)
	store, err := NewStore(fixture.db, fixture.identity, fixture.events, func() time.Time { return securityNow })
	if err != nil {
		t.Fatal(err)
	}
	grant := mustCreateGrant(t, store, expiryCandidateRequest("grant-expiry-race", securityNow.Add(-time.Hour), securityNow.Add(-time.Second), 'f'))

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	counts := make(chan int64, 2)
	for range 2 {
		wg.Add(1)
		runtimesafe.SafeGo(t.Context(), nil, "accesswait.test.expire-due-concurrent", func(context.Context) {
			defer wg.Done()
			count, runErr := store.ExpireDueGrants(context.Background(), securityNow, 1)
			counts <- count
			errs <- runErr
		})
	}
	wg.Wait()
	close(counts)
	close(errs)
	var total int64
	for count := range counts {
		total += count
	}
	for runErr := range errs {
		if runErr != nil {
			t.Fatalf("concurrent expiry error = %v", runErr)
		}
	}
	if total != 1 {
		t.Fatalf("concurrent expiry winners = %d, want exactly one", total)
	}
	var status string
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT status FROM orchestration_access_grants WHERE grant_id = ?`, grant.GrantID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "expired" {
		t.Fatalf("concurrent expiry status = %q, want expired", status)
	}
}

func TestExpireDueGrantsSurvivesSQLiteReopen(t *testing.T) {
	fixture := newSQLiteRestartFixture(t)
	grant := sqliteRestartGrant(t, fixture, "expiry-reopen")
	securityNow := fixture.now.Add(2 * time.Hour)
	fixture.closeDB(t)
	reopened := fixture.reopen(t)
	count, err := reopened.access.ExpireDueGrants(t.Context(), securityNow, 4)
	if err != nil || count != 1 {
		t.Fatalf("ExpireDueGrants(after reopen) = count %d err %v, want one durable candidate", count, err)
	}
	row, err := reopened.access.GetGrant(t.Context(), grant.GrantID)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != "expired" || row.Revision != 2 {
		t.Fatalf("reopened grant = status %q revision %d, want expired/2", row.Status, row.Revision)
	}
}

func TestGrantExpiryRunnerRequiresInjectedClockAndHonorsContext(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	clock := func() time.Time { return time.Now().UTC() }
	assertExpiryRunnerConstructorValidation(t, fixture.store, clock)
	assertExpiryRunnerCancellation(t, fixture.store)
}

// assertExpiryRunnerConstructorValidation 校验 runner 的依赖与批量上限必须显式有效。
func assertExpiryRunnerConstructorValidation(t *testing.T, store *Store, clock func() time.Time) {
	t.Helper()
	if runner, err := NewGrantExpiryRunner(nil, clock); runner != nil || err == nil {
		t.Fatalf("nil store runner = %#v err %v, want constructor failure", runner, err)
	}
	if runner, err := NewGrantExpiryRunner(store, nil); runner != nil || err == nil {
		t.Fatalf("nil security clock runner = %#v err %v, want constructor failure", runner, err)
	}
	if runner, err := NewGrantExpiryRunner(store, clock, GrantExpiryRunnerConfig{BatchLimit: DefaultGrantExpiryBatchLimit + 1}); runner != nil || err == nil {
		t.Fatalf("oversized batch runner = %#v err %v, want constructor failure", runner, err)
	}
}

// assertExpiryRunnerCancellation 校验取消上下文不会调用时钟且可安全执行空批次。
func assertExpiryRunnerCancellation(t *testing.T, store *Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	clockCalls := 0
	runner, err := NewGrantExpiryRunner(store, func() time.Time {
		clockCalls++
		return time.Now().UTC()
	}, GrantExpiryRunnerConfig{TickInterval: time.Millisecond, BatchLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := runner.RunOnce(ctx); !errors.Is(err, context.Canceled) || count != 0 || clockCalls != 0 {
		t.Fatalf("cancelled RunOnce = count %d err %v clock_calls %d, want context.Canceled/0/0", count, err, clockCalls)
	}
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce(zero grants) error = %v", err)
	}
}

func TestGrantExpiryRunnerRejectsTypedNilOwner(t *testing.T) {
	var owner *Store
	clock := func() time.Time { return time.Now().UTC() }
	if runner, err := NewGrantExpiryRunner(owner, clock); runner != nil || err == nil {
		t.Fatalf("typed-nil owner runner = %#v err %v, want constructor failure", runner, err)
	}
}

func TestGrantExpiryRunnerRunStopsOnCancellation(t *testing.T) {
	fixture := newAccessWaitFixture(t)
	clockReady := make(chan struct{}, 1)
	runner, err := NewGrantExpiryRunner(fixture.store, func() time.Time {
		select {
		case clockReady <- struct{}{}:
		default:
		}
		return time.Now().UTC()
	}, GrantExpiryRunnerConfig{TickInterval: time.Millisecond, BatchLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	runtimesafe.SafeGo(ctx, nil, "accesswait.test.expiry-runner", func(runCtx context.Context) {
		defer wg.Done()
		done <- runner.Run(runCtx)
	})
	select {
	case <-clockReady:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("runner did not obtain an injected security time")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not stop after cancellation")
	}
	wg.Wait()
}

func expiryCandidateRequest(id string, issuedAt, expiresAt time.Time, key byte) AccessGrantRequest {
	request := validGrantRequest(id, "expiry/"+id, strings.Repeat(string(key), 64))
	request.IssuedAt = issuedAt.UTC()
	request.ExpiresAt = expiresAt.UTC()
	return request
}
