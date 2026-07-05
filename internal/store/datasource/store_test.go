package datasource

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// TestDocumentStoreDoesNotCreateDatasourceTable 固定 datasource store 不能在运行时自建表。
func TestDocumentStoreDoesNotCreateDatasourceTable(t *testing.T) {
	db := &recordingDocumentDB{}
	store := NewDocumentStore(db)
	params := contract.UpsertDatasourceDocumentParams{
		WorkspaceRoot: "D:\\project",
		Name:          "notes.txt",
		Extension:     ".txt",
		Size:          12,
		StoredPath:    "D:\\project\\.agent\\datasources\\uploads\\notes.txt",
		Content:       "hello world",
	}

	if err := store.UpsertDocument(context.Background(), params); err != nil {
		t.Fatalf("UpsertDocument() error = %v", err)
	}

	if len(db.execSQL) != 1 {
		t.Fatalf("Exec calls = %d, want only upsert SQL", len(db.execSQL))
	}
	for _, sql := range db.execSQL {
		if strings.Contains(strings.ToUpper(sql), "CREATE TABLE") {
			t.Fatalf("document store executed runtime DDL: %q", sql)
		}
	}
}

type recordingDocumentDB struct {
	execSQL []string
}

func (db *recordingDocumentDB) ExecContext(_ context.Context, sql string, _ ...any) (sql.Result, error) {
	db.execSQL = append(db.execSQL, sql)
	return recordingSQLResult(1), nil
}

// PrepareContext satisfies sqlc.DBTX; datasource queries should execute without prepared statements.
func (db *recordingDocumentDB) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	return nil, errors.New("unexpected Prepare call")
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
