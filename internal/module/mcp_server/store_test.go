package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConfigStoreInsertLazilyCreatesTable(t *testing.T) {
	db := &recordingMCPServerDB{}
	store := &configStore{db: db}
	params := StoreMCPServerConfigParams{
		WorkspaceRoot: "D:\\project",
		Name:          "my-search",
		Config: ServerConfig{
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
	if !strings.Contains(db.execSQL[0], "CREATE TABLE IF NOT EXISTS public.mcp_server_configs") {
		t.Fatalf("first Exec SQL = %q, want mcp server table creation", db.execSQL[0])
	}
	if strings.Contains(db.execSQL[1], "CREATE TABLE") || strings.Contains(db.execSQL[2], "CREATE TABLE") {
		t.Fatalf("table creation repeated after first successful ensure: %#v", db.execSQL)
	}
	if !strings.Contains(db.execSQL[1], "INSERT INTO public.mcp_server_configs") {
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
	if !strings.Contains(db.execSQL[0], "DELETE FROM public.mcp_server_configs") {
		t.Fatalf("Exec SQL = %q, want mcp server delete", db.execSQL[0])
	}
}

type recordingMCPServerDB struct {
	execSQL      []string
	rowsAffected int64
}

func (db *recordingMCPServerDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	db.execSQL = append(db.execSQL, sql)
	rowsAffected := db.rowsAffected
	switch {
	case strings.Contains(sql, "INSERT INTO public.mcp_server_configs"):
		if rowsAffected == 0 {
			rowsAffected = 1
		}
		return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", rowsAffected)), nil
	case strings.Contains(sql, "DELETE FROM public.mcp_server_configs"):
		return pgconn.NewCommandTag(fmt.Sprintf("DELETE %d", rowsAffected)), nil
	default:
		return pgconn.NewCommandTag("CREATE TABLE"), nil
	}
}

func (db *recordingMCPServerDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query call")
}

func (db *recordingMCPServerDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return recordingMCPServerRow{}
}

type recordingMCPServerRow struct{}

func (recordingMCPServerRow) Scan(...any) error {
	return errors.New("unexpected QueryRow call")
}
