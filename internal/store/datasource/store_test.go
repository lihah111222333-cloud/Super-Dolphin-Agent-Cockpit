package datasource

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestDocumentStoreUpsertLazilyCreatesTable(t *testing.T) {
	db := &recordingDocumentDB{}
	store := &documentStore{db: db}
	params := contract.UpsertDatasourceDocumentParams{
		WorkspaceRoot: "D:\\project",
		Name:          "notes.txt",
		Extension:     ".txt",
		Size:          12,
		StoredPath:    "D:\\project\\.agent\\datasources\\uploads\\notes.txt",
		Content:       "hello world",
	}

	if err := store.UpsertDocument(context.Background(), params); err != nil {
		t.Fatalf("first UpsertDocument() error = %v", err)
	}
	if err := store.UpsertDocument(context.Background(), params); err != nil {
		t.Fatalf("second UpsertDocument() error = %v", err)
	}

	if len(db.execSQL) != 3 {
		t.Fatalf("Exec calls = %d, want create + two upserts", len(db.execSQL))
	}
	if !strings.Contains(db.execSQL[0], "CREATE TABLE IF NOT EXISTS datasource_documents") {
		t.Fatalf("first Exec SQL = %q, want datasource table creation", db.execSQL[0])
	}
	if strings.Contains(db.execSQL[1], "CREATE TABLE") || strings.Contains(db.execSQL[2], "CREATE TABLE") {
		t.Fatalf("table creation repeated after first successful ensure: %#v", db.execSQL)
	}
}

type recordingDocumentDB struct {
	execSQL []string
}

func (db *recordingDocumentDB) ExecContext(_ context.Context, sql string, _ ...any) (sql.Result, error) {
	db.execSQL = append(db.execSQL, sql)
	return recordingSQLResult(1), nil
}

func (db *recordingDocumentDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (db *recordingDocumentDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

type recordingSQLResult int64

func (r recordingSQLResult) LastInsertId() (int64, error) { return 0, nil }

func (r recordingSQLResult) RowsAffected() (int64, error) { return int64(r), nil }
