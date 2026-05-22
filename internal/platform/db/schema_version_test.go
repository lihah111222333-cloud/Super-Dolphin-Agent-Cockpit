package db

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// fakeVersionRow implements schemaVersionQueryRow + pgx.Row pair to feed
// verifyMinSchemaVersion under unit-test conditions without a live Postgres.
type fakeVersionRow struct {
	version int
	err     error
}

func (r fakeVersionRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("fake row expected one dest")
	}
	if ptr, ok := dest[0].(*int); ok {
		*ptr = r.version
		return nil
	}
	return errors.New("fake row dest not *int")
}

type fakeVersionQueryRow struct {
	row fakeVersionRow
}

func (q fakeVersionQueryRow) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	return q.row
}

func TestMinRequiredSchemaVersion(t *testing.T) {
	t.Parallel()

	if MinRequiredSchemaVersion != 103 {
		t.Fatalf("MinRequiredSchemaVersion = %d, want 103", MinRequiredSchemaVersion)
	}
}

// TestVerifyMinSchemaVersion_AcceptsAtOrAboveMinimum exercises the happy path
// where schema_migrations has caught up with MinRequiredSchemaVersion.
func TestVerifyMinSchemaVersion_AcceptsAtOrAboveMinimum(t *testing.T) {
	t.Parallel()

	for _, v := range []int{MinRequiredSchemaVersion, MinRequiredSchemaVersion + 1, MinRequiredSchemaVersion + 50} {
		q := fakeVersionQueryRow{row: fakeVersionRow{version: v}}
		if err := verifyMinSchemaVersion(context.Background(), q); err != nil {
			t.Errorf("version=%d should pass, got error %v", v, err)
		}
	}
}

// TestVerifyMinSchemaVersion_RejectsBelowMinimum asserts the fail-fast wiring
// gate. The error MUST be bilingual (中文 + English) so operators see actionable
// instructions regardless of locale.
func TestVerifyMinSchemaVersion_RejectsBelowMinimum(t *testing.T) {
	t.Parallel()

	q := fakeVersionQueryRow{row: fakeVersionRow{version: MinRequiredSchemaVersion - 1}}
	err := verifyMinSchemaVersion(context.Background(), q)
	if err == nil {
		t.Fatalf("verifyMinSchemaVersion should reject below-minimum, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"migration", "apply"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
	// 双语 sanity: 中文 + English phrases both present.
	if !strings.Contains(msg, "数据库") {
		t.Errorf("error message missing Chinese half: %s", msg)
	}
	if !strings.Contains(msg, "database migration version") {
		t.Errorf("error message missing English half: %s", msg)
	}
}

// TestVerifyMinSchemaVersion_PropagatesQueryError ensures DB-level failures are
// wrapped (so callers can errors.Is sentinels) rather than mistaken for the
// fence-rejection path.
func TestVerifyMinSchemaVersion_PropagatesQueryError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("conn refused")
	q := fakeVersionQueryRow{row: fakeVersionRow{err: sentinel}}
	err := verifyMinSchemaVersion(context.Background(), q)
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("verifyMinSchemaVersion should propagate query error, got %v", err)
	}
}
