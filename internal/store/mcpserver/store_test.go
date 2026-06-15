package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestConfigStoreInsertLazilyCreatesTable(t *testing.T) {
	db := &recordingMCPServerDB{}
	store := &configStore{db: db}
	params := contract.StoreMCPServerConfigParams{
		WorkspaceRoot: "D:\\project",
		Name:          "my-search",
		Config: contract.MCPServerConfig{
			Transport: "http",
			URL:       "https://your-domain.com/mcp",
			Headers: map[string]string{
				"Authorization": "Bearer YOUR_API_KEY",
			},
		},
	}

	inserted, err := store.InsertServer(context.Background(), params)
	if err != nil {
		t.Fatalf("first InsertServer() error = %v", err)
	}
	if !inserted {
		t.Fatal("first InsertServer() inserted = false, want true")
	}
	if _, err := store.InsertServer(context.Background(), params); err != nil {
		t.Fatalf("second InsertServer() error = %v", err)
	}

	if len(db.execSQL) != 3 {
		t.Fatalf("Exec calls = %d, want create + two inserts", len(db.execSQL))
	}
	if !strings.Contains(db.execSQL[0], "CREATE TABLE IF NOT EXISTS mcp_server_configs") {
		t.Fatalf("first Exec SQL = %q, want mcp server table creation", db.execSQL[0])
	}
	if strings.Contains(db.execSQL[1], "CREATE TABLE") || strings.Contains(db.execSQL[2], "CREATE TABLE") {
		t.Fatalf("table creation repeated after first successful ensure: %#v", db.execSQL)
	}
	if !strings.Contains(db.execSQL[1], "INSERT INTO mcp_server_configs") {
		t.Fatalf("second Exec SQL = %q, want mcp server insert", db.execSQL[1])
	}
}

func TestConfigStoreDeleteUsesMCPServerTable(t *testing.T) {
	db := &recordingMCPServerDB{rowsAffected: 1}
	store := &configStore{db: db, ensured: true}

	deleted, err := store.DeleteServer(context.Background(), "D:\\project", "my-search")
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if !deleted {
		t.Fatal("DeleteServer() deleted = false, want true")
	}
	if len(db.execSQL) != 1 {
		t.Fatalf("Exec calls = %d, want one delete", len(db.execSQL))
	}
	if !strings.Contains(db.execSQL[0], "DELETE FROM mcp_server_configs") {
		t.Fatalf("Exec SQL = %q, want mcp server delete", db.execSQL[0])
	}
}

type recordingMCPServerDB struct {
	execSQL      []string
	rowsAffected int64
}

func (db *recordingMCPServerDB) ExecContext(_ context.Context, sql string, _ ...any) (sql.Result, error) {
	db.execSQL = append(db.execSQL, sql)
	rowsAffected := db.rowsAffected
	switch {
	case strings.Contains(sql, "INSERT INTO mcp_server_configs"):
		if rowsAffected == 0 {
			rowsAffected = 1
		}
		return recordingSQLResult(rowsAffected), nil
	case strings.Contains(sql, "DELETE FROM mcp_server_configs"):
		return recordingSQLResult(rowsAffected), nil
	default:
		return recordingSQLResult(0), nil
	}
}

func (db *recordingMCPServerDB) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (db *recordingMCPServerDB) QueryRowContext(context.Context, string, ...any) *sql.Row {
	return nil
}

type recordingSQLResult int64

func (r recordingSQLResult) LastInsertId() (int64, error) { return 0, nil }

func (r recordingSQLResult) RowsAffected() (int64, error) { return int64(r), nil }
