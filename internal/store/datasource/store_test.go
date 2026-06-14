package datasource

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	if !strings.Contains(db.execSQL[0], "CREATE TABLE IF NOT EXISTS public.datasource_documents") {
		t.Fatalf("first Exec SQL = %q, want datasource table creation", db.execSQL[0])
	}
	if strings.Contains(db.execSQL[1], "CREATE TABLE") || strings.Contains(db.execSQL[2], "CREATE TABLE") {
		t.Fatalf("table creation repeated after first successful ensure: %#v", db.execSQL)
	}
}

type recordingDocumentDB struct {
	execSQL []string
}

func (db *recordingDocumentDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	db.execSQL = append(db.execSQL, sql)
	return pgconn.CommandTag{}, nil
}

func (db *recordingDocumentDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (db *recordingDocumentDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return recordingDocumentRow{}
}

type recordingDocumentRow struct{}

func (recordingDocumentRow) Scan(...any) error {
	return errors.New("unexpected QueryRow call")
}
