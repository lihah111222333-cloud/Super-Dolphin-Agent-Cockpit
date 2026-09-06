package securitytime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type memoryStateStore struct {
	mu         sync.Mutex
	state      State
	have       bool
	forceStale bool
}

func (s *memoryStateStore) Load(_ context.Context, domain string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.have || s.state.SecurityDomainID != domain {
		return State{}, ErrStateNotFound
	}
	return s.state, nil
}

func (s *memoryStateStore) CompareAndSwap(_ context.Context, domain string, expected int64, next State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forceStale {
		return ErrStaleWriter
	}
	if domain != next.SecurityDomainID || (s.have && s.state.Version != expected) || (!s.have && expected != 0) {
		return ErrStaleWriter
	}
	s.state, s.have = next, true
	return nil
}

type scriptedClock struct {
	mu      sync.Mutex
	samples []Sample
}

func (c *scriptedClock) Sample(_ context.Context) (Sample, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.samples) == 0 {
		return Sample{}, errors.New("no sample")
	}
	sample := c.samples[0]
	c.samples = c.samples[1:]
	return sample, nil
}

func newTestOwner(t *testing.T, clock *scriptedClock, store *memoryStateStore) *ClockOwner {
	t.Helper()
	owner, err := NewClockOwner(Config{
		SecurityDomainID: "domain-a", LineageDigest: "lineage-a", BootEpoch: 1, MaxSkewMS: 100,
	}, store, clock)
	if err != nil {
		t.Fatalf("NewClockOwner() error = %v", err)
	}
	return owner
}

func sample(wall, mono int64) Sample {
	return Sample{SourceID: "source-a", BootEpoch: 1, WallMS: wall, MonoNS: mono, Fresh: true}
}

func TestClockOwnerInitialObserveAndAllocate(t *testing.T) {
	clock := &scriptedClock{samples: []Sample{sample(1000, 1_000_000), sample(1010, 11_000_000), sample(3011, 2_012_000_000)}}
	owner := newTestOwner(t, clock, &memoryStateStore{})
	observed, err := owner.Observe(context.Background())
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if observed.WallHighWaterMS != 1000 || observed.ClockEpoch != 1 || observed.Generation != 1 {
		t.Fatalf("initial snapshot = %+v", observed)
	}
	lease, err := owner.Allocate(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}
	if lease.IssuedAtMS != 1010 || lease.LeaseExpiresAtMS != 3010 || lease.MonoDeadlineNS != 2_011_000_000 {
		t.Fatalf("lease = %+v", lease)
	}
	if err := owner.Validate(context.Background(), lease); !errors.Is(err, ErrSnapshotExpired) {
		t.Fatalf("expired lease validation = %v, want ErrSnapshotExpired", err)
	}
}

func TestClockOwnerRejectsNilContextAndInitialBootMismatch(t *testing.T) {
	owner := newTestOwner(t, &scriptedClock{samples: []Sample{{SourceID: "source-a", BootEpoch: 2, WallMS: 1000, MonoNS: 1, Fresh: true}}}, &memoryStateStore{})
	if _, err := owner.Observe(nil); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("nil context error = %v, want ErrInvalidContext", err)
	}
	if _, err := owner.Observe(context.Background()); !errors.Is(err, ErrInvalidSample) {
		t.Fatalf("initial boot mismatch error = %v, want ErrInvalidSample", err)
	}
}

func TestClockOwnerRollbackFencesStickyUnavailable(t *testing.T) {
	clock := &scriptedClock{samples: []Sample{sample(10_000, 1_000_000), sample(8_000, 2_000_000), sample(9_000, 3_000_000)}}
	store := &memoryStateStore{}
	owner := newTestOwner(t, clock, store)
	if _, err := owner.Observe(context.Background()); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}
	if _, err := owner.Observe(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("rollback error = %v, want ErrUnavailable", err)
	}
	if _, err := owner.Observe(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("sticky error = %v, want ErrUnavailable", err)
	}
}

func TestClockOwnerForwardJumpAndSleepResumeFence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		samples []Sample
	}{
		{name: "forward jump", samples: []Sample{sample(10_000, 1_000_000), sample(20_000, 2_000_000)}},
		{name: "sleep resume", samples: []Sample{sample(10_000, 1_000_000), sample(10_500, 1_001_000)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := newTestOwner(t, &scriptedClock{samples: tc.samples}, &memoryStateStore{})
			if _, err := owner.Observe(context.Background()); err != nil {
				t.Fatalf("initial Observe() error = %v", err)
			}
			if _, err := owner.Observe(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("anomaly error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestClockOwnerMixedSourceFences(t *testing.T) {
	clock := &scriptedClock{samples: []Sample{sample(1000, 1), {SourceID: "source-b", BootEpoch: 1, WallMS: 1001, MonoNS: 2, Fresh: true}}}
	owner := newTestOwner(t, clock, &memoryStateStore{})
	if _, err := owner.Observe(context.Background()); err != nil {
		t.Fatalf("initial Observe() error = %v", err)
	}
	if _, err := owner.Observe(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("mixed source error = %v, want ErrUnavailable", err)
	}
}

func TestClockOwnerRestartRejectsOldSnapshotAndAdvancesEpoch(t *testing.T) {
	store := &memoryStateStore{}
	first := newTestOwner(t, &scriptedClock{samples: []Sample{sample(10_000, 1_000_000), sample(10_010, 2_000_000)}}, store)
	old, err := first.Allocate(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("first Allocate() error = %v", err)
	}
	secondClock := &scriptedClock{samples: []Sample{{SourceID: "source-a", BootEpoch: 2, WallMS: 10_020, MonoNS: 1_000_000, Fresh: true}}}
	second, err := NewClockOwner(Config{SecurityDomainID: "domain-a", LineageDigest: "lineage-a", BootEpoch: 2, MaxSkewMS: 100}, store, secondClock)
	if err != nil {
		t.Fatalf("second NewClockOwner() error = %v", err)
	}
	next, err := second.Observe(context.Background())
	if err != nil {
		t.Fatalf("restart Observe() error = %v", err)
	}
	if next.ClockEpoch != old.ClockEpoch+1 || next.Generation != old.Generation+1 {
		t.Fatalf("restart snapshot = %+v, want advanced epochs from %+v", next, old)
	}
	if err := second.Validate(context.Background(), old); !errors.Is(err, ErrSnapshotMismatch) {
		t.Fatalf("old snapshot validation = %v, want ErrSnapshotMismatch", err)
	}
}

func TestClockOwnerStaleWriterIsFailClosed(t *testing.T) {
	store := &memoryStateStore{}
	clockA := &scriptedClock{samples: []Sample{sample(1000, 1)}}
	clockB := &scriptedClock{samples: []Sample{sample(1001, 2)}}
	ownerA := newTestOwner(t, clockA, store)
	ownerB := newTestOwner(t, clockB, store)
	if _, err := ownerA.Observe(context.Background()); err != nil {
		t.Fatalf("owner A Observe() error = %v", err)
	}
	store.forceStale = true
	if _, err := ownerB.Observe(context.Background()); !errors.Is(err, ErrStaleWriter) {
		t.Fatalf("owner B stale error = %v, want ErrStaleWriter", err)
	}
}

func TestClockOwnerRejectsUnknownFreshnessAndExpiredSnapshot(t *testing.T) {
	store := &memoryStateStore{}
	clock := &scriptedClock{samples: []Sample{sample(1000, 1), {SourceID: "source-a", BootEpoch: 1, WallMS: 1010, MonoNS: 2, Fresh: false}, sample(1020, 3)}}
	owner := newTestOwner(t, clock, store)
	lease, err := owner.Allocate(context.Background(), time.Millisecond)
	if err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}
	if err := owner.Validate(context.Background(), lease); !errors.Is(err, ErrInvalidSample) && !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unknown freshness validation = %v", err)
	}
	if _, err := owner.Observe(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("sticky unavailable after unknown freshness = %v", err)
	}
}

func TestSnapshotValidateRejectsMalformedLease(t *testing.T) {
	if err := (Snapshot{SecurityDomainID: "domain-a", LineageDigest: "lineage-a", SourceID: "source-a", BootEpoch: 1,
		ClockEpoch: 1, Generation: 1, Revision: 1, WallHighWaterMS: 1000, IssuedAtMS: 1000, MaxSkewMS: 100}).Validate(); !errors.Is(err, ErrSnapshotMismatch) {
		t.Fatalf("malformed snapshot validation = %v, want ErrSnapshotMismatch", err)
	}
}
