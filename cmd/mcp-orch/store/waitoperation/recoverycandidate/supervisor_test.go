package recoverycandidate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

type owner struct {
	mu                  sync.Mutex
	marker              Marker
	canonicalPostDigest string
}

type nilVerifier struct{}

func (*nilVerifier) VerifyPostState(context.Context, Marker) (string, error) {
	return "", nil
}

func (o *owner) ReadMarker(context.Context, string) (Marker, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.marker, nil
}
func (o *owner) ListPendingMarkers(context.Context) ([]Marker, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.marker.State != "pending" {
		return nil, nil
	}
	return []Marker{o.marker}, nil
}
func (o *owner) ResolveMarker(_ context.Context, key, proof string) (Marker, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if key != o.marker.MarkerKey || proof != o.marker.PostStateDigest {
		return Marker{}, ErrProofMismatch
	}
	if o.marker.State == "pending" {
		o.marker.State = "resolved"
		o.marker.ResolvedAt = formatTime(time.Unix(2, 0).UTC())
	}
	return o.marker, nil
}

func TestRecoverySupervisorDualReceiptWinnerAndRestart(t *testing.T) {
	fixture := newDualRecoveryFixture(t)
	assertDualRecoveryRace(t, fixture)
	assertDualRecoveryRestart(t, fixture)
}

type dualRecoveryFixture struct {
	product  *sql.DB
	receipt  *sql.DB
	marker   Marker
	owner    *owner
	verifier CanonicalPostStateVerifier
	first    *Supervisor
	second   *Supervisor
}

// newDualRecoveryFixture 准备双 supervisor 竞争同一恢复回执的真实 SQLite 场景。
func newDualRecoveryFixture(t *testing.T) dualRecoveryFixture {
	t.Helper()
	product := openDB(t, filepath.Join(t.TempDir(), "product.sqlite"))
	receipt := openDB(t, filepath.Join(t.TempDir(), "receipt.sqlite"))
	t.Cleanup(func() { _ = product.Close(); _ = receipt.Close() })
	if _, err := receipt.Exec(ReceiptSchema); err != nil {
		t.Fatal(err)
	}
	marker := testMarker()
	owner := &owner{marker: marker, canonicalPostDigest: marker.PostStateDigest}
	verifier := CanonicalPostStateVerifierFunc(func(_ context.Context, got Marker) (string, error) {
		if got.State != "pending" && got.State != "resolved" {
			return "", errors.New("non-canonical state")
		}
		owner.mu.Lock()
		defer owner.mu.Unlock()
		return owner.canonicalPostDigest, nil
	})
	first, err := New(receipt, product, owner, owner, verifier, func() time.Time { return time.Unix(10, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(receipt, product, owner, owner, verifier, func() time.Time { return time.Unix(11, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	return dualRecoveryFixture{product: product, receipt: receipt, marker: marker, owner: owner, verifier: verifier, first: first, second: second}
}

// assertDualRecoveryRace 校验并发恢复仅产生一个赢家且只持久化一份回执。
func assertDualRecoveryRace(t *testing.T, fixture dualRecoveryFixture) {
	t.Helper()
	var wg sync.WaitGroup
	observations := make(chan Observation, 2)
	errs := make(chan error, 2)
	for _, supervisor := range []*Supervisor{fixture.first, fixture.second} {
		sup := supervisor
		wg.Add(1)
		runtimesafe.SafeGo(t.Context(), nil, "waitoperation.recoverycandidate.test.concurrent-recover", func(context.Context) {
			defer wg.Done()
			observation, recoverErr := sup.RecoverMarker(context.Background(), fixture.marker.MarkerKey)
			observations <- observation
			errs <- recoverErr
		})
	}
	wg.Wait()
	close(observations)
	close(errs)
	winners := 0
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for observation := range observations {
		if observation.Winner {
			winners++
		}
		if observation.Authoritative {
			t.Fatal("candidate claimed authority")
		}
	}
	if winners != 1 {
		t.Fatalf("winners=%d, want one", winners)
	}
	var count int
	if err := fixture.receipt.QueryRow(`SELECT COUNT(*) FROM orchestration_wait_operation_recovery_receipts`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("receipts=%d, want one", count)
	}
}

// assertDualRecoveryRestart 校验重启后的恢复调用只返回既有回执而不会重新夺胜。
func assertDualRecoveryRestart(t *testing.T, fixture dualRecoveryFixture) {
	t.Helper()
	restarted, err := New(fixture.receipt, fixture.product, fixture.owner, fixture.owner, fixture.verifier, func() time.Time { return time.Unix(12, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	replay, err := restarted.RecoverMarker(context.Background(), fixture.marker.MarkerKey)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Winner {
		t.Fatal("restart replay became winner")
	}
}

func TestRecoverySupervisorRejectsSameDBAndMissingProof(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "db.sqlite"))
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(ReceiptSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := New(db, db, &owner{}, &owner{}, CanonicalPostStateVerifierFunc(func(context.Context, Marker) (string, error) { return "", nil }), func() time.Time { return time.Unix(1, 0) }); !errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("same DB constructor=%v", err)
	}
	marker := testMarker()
	marker.PostStateDigest = ""
	marker.MarkerKey = markerKey(marker)
	candidateOwner := &owner{marker: marker}
	supervisor, err := New(db, openDB(t, filepath.Join(t.TempDir(), "product.sqlite")), candidateOwner, candidateOwner, CanonicalPostStateVerifierFunc(func(context.Context, Marker) (string, error) { return "", nil }), func() time.Time { return time.Unix(1, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.RecoverMarker(context.Background(), marker.MarkerKey); !errors.Is(err, ErrProofRequired) {
		t.Fatalf("missing proof=%v", err)
	}
}

func TestRecoverySupervisorRejectsTypedNilCapabilitiesAndNonUTCTime(t *testing.T) {
	receipt, product := &sql.DB{}, &sql.DB{}
	validVerifier := CanonicalPostStateVerifierFunc(func(context.Context, Marker) (string, error) { return "", nil })
	utcClock := func() time.Time { return time.Unix(1, 0).UTC() }
	var typedNilOwner *owner
	if _, err := New(receipt, product, typedNilOwner, &owner{}, validVerifier, utcClock); !errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("typed-nil reader=%v, want %v", err, ErrRecoveryUnavailable)
	}
	if _, err := New(receipt, product, &owner{}, typedNilOwner, validVerifier, utcClock); !errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("typed-nil resolver=%v, want %v", err, ErrRecoveryUnavailable)
	}
	var typedNilVerifier *nilVerifier
	if _, err := New(receipt, product, &owner{}, &owner{}, typedNilVerifier, utcClock); !errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("typed-nil verifier=%v, want %v", err, ErrRecoveryUnavailable)
	}
	for name, clock := range map[string]Clock{
		"zero":     func() time.Time { return time.Time{} },
		"local":    func() time.Time { return time.Unix(1, 0) },
		"fixedUTC": func() time.Time { return time.Unix(1, 0).In(time.FixedZone("UTC", 0)) },
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(receipt, product, &owner{}, &owner{}, validVerifier, clock); err == nil {
				t.Fatal("non-UTC or zero clock unexpectedly accepted")
			}
		})
	}
}

func TestRecoveryReceiptImmutableAndSchemaGuard(t *testing.T) {
	product := openDB(t, filepath.Join(t.TempDir(), "product.sqlite"))
	receipt := openDB(t, filepath.Join(t.TempDir(), "receipt.sqlite"))
	t.Cleanup(func() { _ = product.Close(); _ = receipt.Close() })
	marker := testMarker()
	owner := &owner{marker: marker, canonicalPostDigest: marker.PostStateDigest}
	if _, err := receipt.Exec(ReceiptSchema); err != nil {
		t.Fatal(err)
	}
	supervisor, err := New(receipt, product, owner, owner, CanonicalPostStateVerifierFunc(func(context.Context, Marker) (string, error) {
		owner.mu.Lock()
		defer owner.mu.Unlock()
		return owner.canonicalPostDigest, nil
	}), func() time.Time { return time.Unix(10, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := supervisor.RecoverMarker(context.Background(), marker.MarkerKey); err != nil {
		t.Fatal(err)
	}
	if _, err := receipt.Exec(`UPDATE orchestration_wait_operation_recovery_receipts SET recovered_at = ?`, formatTime(time.Unix(11, 0).UTC())); err == nil {
		t.Fatal("receipt timestamp update unexpectedly succeeded")
	}
	if _, err := receipt.Exec(`DELETE FROM orchestration_wait_operation_recovery_receipts`); err == nil {
		t.Fatal("receipt delete unexpectedly succeeded")
	}
	if err := supervisor.EnsureSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(context.Background(), sqliteruntime.OpenOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := sqliteruntime.RunEmbeddedMigrations(context.Background(), db); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	return db
}
func testMarker() Marker {
	marker := Marker{OperationKey: "operation", OperationDigest: repeat('a'), MutationKind: "state_advance", RequestDigest: repeat('b'), PreStateDigest: repeat('c'), PostStateDigest: repeat('d'), State: "pending", CreatedAt: formatTime(time.Unix(1, 0).UTC()), SchemaVersion: schemaVersion}
	marker.MarkerKey = markerKey(marker)
	return marker
}
func repeat(char byte) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = char
	}
	return string(value)
}
