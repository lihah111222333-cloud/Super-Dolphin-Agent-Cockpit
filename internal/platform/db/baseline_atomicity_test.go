package db

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestApplyBaselineDetectionQueryErrorRollsBackWithoutMarker(t *testing.T) {
	ctx := context.Background()
	tx := newBaselineAtomicityTx()
	tx.markerDetectionErr = errors.New("detect marker failed")

	err := applyBaselineIfMissingWithBegin(ctx, tx.begin, t.TempDir(), successfulBaselineRead)

	assertBaselineAtomicityError(t, err, "detect marker failed")
	assertBaselineAtomicityRolledBackWithoutMarker(t, tx)
}

func TestApplyBaselineExistingSchemaProbeErrorRollsBackWithoutMarker(t *testing.T) {
	ctx := context.Background()
	tx := newBaselineAtomicityTx()
	tx.existingSchemaProbeErr = errors.New("probe schema failed")

	err := applyBaselineIfMissingWithBegin(ctx, tx.begin, t.TempDir(), successfulBaselineRead)

	assertBaselineAtomicityError(t, err, "probe schema failed")
	assertBaselineAtomicityRolledBackWithoutMarker(t, tx)
}

func TestApplyBaselineUnreadableBaselineRollsBackWithoutMarker(t *testing.T) {
	ctx := context.Background()
	tx := newBaselineAtomicityTx()

	err := applyBaselineIfMissingWithBegin(ctx, tx.begin, t.TempDir(), func(string) ([]byte, error) {
		return nil, errors.New("baseline file unreadable")
	})

	assertBaselineAtomicityError(t, err, "baseline file unreadable")
	assertBaselineAtomicityRolledBackWithoutMarker(t, tx)
}

func TestApplyBaselineExecFailureRollsBackWithoutMarker(t *testing.T) {
	ctx := context.Background()
	tx := newBaselineAtomicityTx()
	tx.baselineExecErr = errors.New("baseline exec failed")

	err := applyBaselineIfMissingWithBegin(ctx, tx.begin, t.TempDir(), successfulBaselineRead)

	assertBaselineAtomicityError(t, err, "baseline exec failed")
	assertBaselineAtomicityRolledBackWithoutMarker(t, tx)
}

func TestApplyBaselineMarkerInsertFailureRollsBackBaselineChanges(t *testing.T) {
	ctx := context.Background()
	tx := newBaselineAtomicityTx()
	tx.markerInsertErr = errors.New("marker insert failed")

	err := applyBaselineIfMissingWithBegin(ctx, tx.begin, t.TempDir(), successfulBaselineRead)

	assertBaselineAtomicityError(t, err, "marker insert failed")
	assertBaselineAtomicityRolledBackWithoutMarker(t, tx)
	if tx.baselineCommitted {
		t.Fatal("baseline SQL changes committed even though marker insert failed")
	}
}

func TestApplyBaselinePartialExistingSchemaRollsBackWithoutMarker(t *testing.T) {
	ctx := context.Background()
	tx := newBaselineAtomicityTx()
	tx.existingBaselineTableCount = 1

	err := applyBaselineIfMissingWithBegin(ctx, tx.begin, t.TempDir(), successfulBaselineRead)

	assertBaselineAtomicityError(t, err, "partial existing baseline schema")
	assertBaselineAtomicityRolledBackWithoutMarker(t, tx)
	if tx.baselineExecCalled {
		t.Fatal("partial existing schema must not execute baseline SQL over the top")
	}
}

func TestApplyBaselineConfirmedExistingSchemaWritesMarkerAfterInvariant(t *testing.T) {
	ctx := context.Background()
	tx := newBaselineAtomicityTx()
	tx.existingBaselineTableCount = len(requiredBaselineTables)

	err := applyBaselineIfMissingWithBegin(ctx, tx.begin, t.TempDir(), successfulBaselineRead)
	if err != nil {
		t.Fatalf("applyBaselineIfMissingWithBegin() error = %v", err)
	}
	if !tx.existingSchemaProbeCalled {
		t.Fatal("marker was written without probing the existing schema invariant")
	}
	if tx.baselineExecCalled {
		t.Fatal("confirmed existing schema path must not execute baseline SQL")
	}
	if !tx.markerCommitted {
		t.Fatal("confirmed existing schema path did not commit baseline marker")
	}
}

func successfulBaselineRead(path string) ([]byte, error) {
	if filepath.Base(path) != "001_baseline.sql" {
		return nil, fmt.Errorf("unexpected baseline path %q", path)
	}
	return []byte("CREATE TABLE baseline_side_effect(id integer);"), nil
}

type baselineAtomicityTx struct {
	markerDetectionErr         error
	existingSchemaProbeErr     error
	baselineExecErr            error
	markerInsertErr            error
	existingBaselineTableCount int

	begun                     bool
	committed                 bool
	rolledBack                bool
	existingSchemaProbeCalled bool
	baselineExecCalled        bool
	baselineApplied           bool
	baselineCommitted         bool
	markerInserted            bool
	markerCommitted           bool
}

func newBaselineAtomicityTx() *baselineAtomicityTx {
	return &baselineAtomicityTx{}
}

func (tx *baselineAtomicityTx) begin(context.Context) (baselineTx, error) {
	tx.begun = true
	return tx, nil
}

func (tx *baselineAtomicityTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "schema_migrations") && strings.Contains(sql, "001_baseline.sql"):
		return baselineAtomicityRow{value: false, err: tx.markerDetectionErr}
	case strings.Contains(sql, "information_schema.tables"):
		tx.existingSchemaProbeCalled = true
		return baselineAtomicityRow{value: tx.existingBaselineTableCount, err: tx.existingSchemaProbeErr}
	default:
		return baselineAtomicityRow{err: fmt.Errorf("unexpected query: %s", sql)}
	}
}

func (tx *baselineAtomicityTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	switch {
	case strings.Contains(sql, "INSERT INTO schema_migrations"):
		if tx.markerInsertErr != nil {
			return pgconn.CommandTag{}, tx.markerInsertErr
		}
		tx.markerInserted = true
		return pgconn.CommandTag{}, nil
	default:
		tx.baselineExecCalled = true
		if tx.baselineExecErr != nil {
			return pgconn.CommandTag{}, tx.baselineExecErr
		}
		tx.baselineApplied = true
		return pgconn.CommandTag{}, nil
	}
}

func (tx *baselineAtomicityTx) Commit(context.Context) error {
	tx.committed = true
	tx.baselineCommitted = tx.baselineApplied
	tx.markerCommitted = tx.markerInserted
	return nil
}

func (tx *baselineAtomicityTx) Rollback(context.Context) error {
	tx.rolledBack = true
	tx.baselineApplied = false
	tx.markerInserted = false
	return nil
}

type baselineAtomicityRow struct {
	value any
	err   error
}

func (r baselineAtomicityRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return fmt.Errorf("expected one scan destination, got %d", len(dest))
	}
	switch d := dest[0].(type) {
	case *bool:
		v, ok := r.value.(bool)
		if !ok {
			return fmt.Errorf("scan bool from %T", r.value)
		}
		*d = v
	case *int:
		v, ok := r.value.(int)
		if !ok {
			return fmt.Errorf("scan int from %T", r.value)
		}
		*d = v
	default:
		return fmt.Errorf("unsupported scan destination %T", dest[0])
	}
	return nil
}

func assertBaselineAtomicityError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("applyBaselineIfMissingWithBegin() error = nil, want %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("applyBaselineIfMissingWithBegin() error = %v, want containing %q", err, want)
	}
}

func assertBaselineAtomicityRolledBackWithoutMarker(t *testing.T, tx *baselineAtomicityTx) {
	t.Helper()
	if !tx.begun {
		t.Fatal("baseline transaction was not started")
	}
	if tx.committed {
		t.Fatal("baseline transaction committed after failure")
	}
	if !tx.rolledBack {
		t.Fatal("baseline transaction was not rolled back after failure")
	}
	if tx.markerCommitted {
		t.Fatal("baseline marker committed after failure")
	}
}
