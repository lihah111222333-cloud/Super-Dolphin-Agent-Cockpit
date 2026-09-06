package content

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func contentAssertRowCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s row count = %d, want %d", table, got, want)
	}
}

func contentAssertReservationState(t *testing.T, db *sql.DB, reservationID, state string, revision int64) {
	t.Helper()
	var gotState string
	var gotRevision int64
	err := db.QueryRowContext(t.Context(), `SELECT state, revision FROM orchestration_content_ingest_reservations
		WHERE ingest_reservation_id = ?`, reservationID).Scan(&gotState, &gotRevision)
	contentNoError(t, err)
	if gotState != state || gotRevision != revision {
		t.Fatalf("reservation state = (%q,%d), want (%q,%d)", gotState, gotRevision, state, revision)
	}
}

func contentAssertPreparationPreflight(t *testing.T, db *sql.DB, reservationID, operationID string, startedAt time.Time) {
	t.Helper()
	var gotOperationID, gotStartedAt string
	err := db.QueryRowContext(t.Context(), `SELECT prepare_operation_id, prepare_started_at
		FROM orchestration_content_ingest_reservations WHERE ingest_reservation_id = ?`, reservationID).
		Scan(&gotOperationID, &gotStartedAt)
	contentNoError(t, err)
	if gotOperationID != operationID || gotStartedAt != startedAt.Format(time.RFC3339Nano) {
		t.Fatalf("preflight row = (%q,%q), want (%q,%q)", gotOperationID, gotStartedAt,
			operationID, startedAt.Format(time.RFC3339Nano))
	}
}

func contentSealedObjectFiles(t *testing.T, stateDir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(stateDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() && strings.HasSuffix(path, ".sealed") {
			files = append(files, path)
		}
		return nil
	})
	contentNoError(t, err)
	return files
}

func contentAssertNoSensitiveSQLColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT name FROM pragma_table_info('orchestration_content_ingest_reservations')
		UNION SELECT name FROM pragma_table_info('orchestration_content_objects')
		UNION SELECT name FROM pragma_table_info('orchestration_content_private_payload_receipts')
		UNION SELECT name FROM pragma_table_info('orchestration_content_refs')`)
	contentNoError(t, err)
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close content column rows: %v", err)
		}
	}()
	for rows.Next() {
		var name string
		contentNoError(t, rows.Scan(&name))
		lower := strings.ToLower(name)
		for _, forbidden := range []string{"plaintext", "ciphertext_bytes", "raw_key", "wrapped_key", "auth_tag"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("content SQLite schema exposes sensitive byte column %q", name)
			}
		}
	}
	contentNoError(t, rows.Err())
}

func contentMkdir(t *testing.T, path string) {
	t.Helper()
	contentNoError(t, os.MkdirAll(path, 0o700))
}

func contentNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func contentWantError(t *testing.T, operation string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s error = %v, want %v", operation, err, want)
	}
}

func contentAssertDeepEqual(t *testing.T, operation string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %#v, want %#v", operation, got, want)
	}
}

func contentAssertActiveRef(t *testing.T, active ActiveRef) {
	t.Helper()
	if active.ContentState != "active" || active.ReferenceState != "live" || active.KeyState != "active" || active.RefVersion != 1 {
		t.Fatalf("Promote() = %#v, want active/live ref version 1", active)
	}
	if err := active.Validate(); err != nil {
		t.Fatalf("ActiveRef.Validate() error = %v", err)
	}
}
