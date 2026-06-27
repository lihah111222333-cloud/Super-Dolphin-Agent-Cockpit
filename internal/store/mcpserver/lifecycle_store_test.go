package mcpserver

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestLifecycleStoreUpsertReadAndList(t *testing.T) {
	store, closeDB := newSQLiteLifecycleStore(t)
	defer closeDB()
	ctx := context.Background()
	key := contract.MCPToolLifecycleKey{
		WorkspaceRoot: "/tmp/project",
		ServerName:    "sqlite",
		ToolName:      "query",
	}

	record, err := store.UpsertMCPToolLifecycleState(ctx, contract.MCPToolLifecycleUpsertParams{
		Key:       key,
		State:     contract.MCPToolLifecycleStateSuspended,
		Reason:    "manual pause",
		Source:    contract.MCPToolLifecycleSourceUser,
		UpdatedBy: "operator",
	})
	if err != nil {
		t.Fatalf("UpsertMCPToolLifecycleState() error = %v", err)
	}
	assertLifecycleStateSource(t, record, contract.MCPToolLifecycleStateSuspended, contract.MCPToolLifecycleSourceUser)
	assertLifecycleTimestamps(t, record)

	got, err := store.GetMCPToolLifecycleState(ctx, key)
	if err != nil {
		t.Fatalf("GetMCPToolLifecycleState() error = %v", err)
	}
	assertLifecycleReadFields(t, got)

	upsertActiveLifecycleForTest(t, store, ctx)
	records, err := store.ListMCPToolLifecycleStates(ctx, contract.MCPToolLifecycleListParams{
		WorkspaceRoot: "/tmp/project",
		ServerName:    "sqlite",
	})
	if err != nil {
		t.Fatalf("ListMCPToolLifecycleStates() error = %v", err)
	}
	assertLifecycleToolOrder(t, records, "inspect", "query")
}

func assertLifecycleStateSource(
	t *testing.T,
	record contract.MCPToolLifecycleRecord,
	state contract.MCPToolLifecycleState,
	source contract.MCPToolLifecycleSource,
) {
	t.Helper()
	if record.State != state || record.Source != source {
		t.Fatalf("record = %+v, want state=%s source=%s", record, state, source)
	}
}

func assertLifecycleTimestamps(t *testing.T, record contract.MCPToolLifecycleRecord) {
	t.Helper()
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		t.Fatalf("timestamps were not populated: %+v", record)
	}
}

func assertLifecycleReadFields(t *testing.T, record contract.MCPToolLifecycleRecord) {
	t.Helper()
	if record.ToolName != "query" || record.Reason != "manual pause" || record.UpdatedBy != "operator" {
		t.Fatalf("record = %+v, want persisted fields", record)
	}
}

func upsertActiveLifecycleForTest(
	t *testing.T,
	store contract.MCPToolLifecycleStore,
	ctx context.Context,
) {
	t.Helper()
	_, err := store.UpsertMCPToolLifecycleState(ctx, contract.MCPToolLifecycleUpsertParams{
		Key:       contract.MCPToolLifecycleKey{WorkspaceRoot: "/tmp/project", ServerName: "sqlite", ToolName: "inspect"},
		State:     contract.MCPToolLifecycleStateActive,
		Source:    contract.MCPToolLifecycleSourceDiscovery,
		UpdatedBy: "discovery",
	})
	if err != nil {
		t.Fatalf("UpsertMCPToolLifecycleState(second) error = %v", err)
	}
}

func assertLifecycleToolOrder(t *testing.T, records []contract.MCPToolLifecycleRecord, want ...string) {
	t.Helper()
	if len(records) != len(want) {
		t.Fatalf("records = %+v, want %d records", records, len(want))
	}
	for i, toolName := range want {
		if records[i].ToolName != toolName {
			t.Fatalf("records = %+v, want tool_name ASC order %v", records, want)
		}
	}
}

func TestLifecycleStoreRejectsInvalidParams(t *testing.T) {
	store, closeDB := newSQLiteLifecycleStore(t)
	defer closeDB()
	ctx := context.Background()
	valid := contract.MCPToolLifecycleUpsertParams{
		Key: contract.MCPToolLifecycleKey{
			WorkspaceRoot: "/tmp/project",
			ServerName:    "sqlite",
			ToolName:      "query",
		},
		State:  contract.MCPToolLifecycleStateActive,
		Source: contract.MCPToolLifecycleSourceUser,
	}
	tests := []struct {
		name    string
		mutate  func(*contract.MCPToolLifecycleUpsertParams)
		wantErr string
	}{
		{
			name:    "workspace",
			mutate:  func(p *contract.MCPToolLifecycleUpsertParams) { p.Key.WorkspaceRoot = " " },
			wantErr: "workspaceRoot is required",
		},
		{
			name:    "server",
			mutate:  func(p *contract.MCPToolLifecycleUpsertParams) { p.Key.ServerName = "" },
			wantErr: "server name is required",
		},
		{
			name:    "tool",
			mutate:  func(p *contract.MCPToolLifecycleUpsertParams) { p.Key.ToolName = "\t" },
			wantErr: "tool name is required",
		},
		{
			name:    "state",
			mutate:  func(p *contract.MCPToolLifecycleUpsertParams) { p.State = "paused" },
			wantErr: "invalid state",
		},
		{
			name:    "source",
			mutate:  func(p *contract.MCPToolLifecycleUpsertParams) { p.Source = "fallback" },
			wantErr: "invalid source",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := valid
			tt.mutate(&params)
			_, err := store.UpsertMCPToolLifecycleState(ctx, params)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("UpsertMCPToolLifecycleState() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestLifecycleStoreEnsureDiscoveredDoesNotOverwriteUserState(t *testing.T) {
	store, closeDB := newSQLiteLifecycleStore(t)
	defer closeDB()
	ctx := context.Background()
	suspendedKey := contract.MCPToolLifecycleKey{
		WorkspaceRoot: "/tmp/project",
		ServerName:    "sqlite",
		ToolName:      "query",
	}
	if _, err := store.UpsertMCPToolLifecycleState(ctx, contract.MCPToolLifecycleUpsertParams{
		Key:    suspendedKey,
		State:  contract.MCPToolLifecycleStateSuspended,
		Reason: "operator pause",
		Source: contract.MCPToolLifecycleSourceUser,
	}); err != nil {
		t.Fatalf("UpsertMCPToolLifecycleState() error = %v", err)
	}

	record, inserted, err := store.EnsureDiscoveredMCPToolLifecycleState(ctx, contract.MCPToolLifecycleDiscoveryParams{
		Key:       suspendedKey,
		Reason:    "discovered again",
		UpdatedBy: "discovery",
	})
	if err != nil {
		t.Fatalf("EnsureDiscoveredMCPToolLifecycleState(existing) error = %v", err)
	}
	if inserted || record.State != contract.MCPToolLifecycleStateSuspended || record.Reason != "operator pause" {
		t.Fatalf("existing record = %+v inserted=%v, want unchanged suspended", record, inserted)
	}

	activeKey := contract.MCPToolLifecycleKey{
		WorkspaceRoot: "/tmp/project",
		ServerName:    "sqlite",
		ToolName:      "inspect",
	}
	record, inserted, err = store.EnsureDiscoveredMCPToolLifecycleState(ctx, contract.MCPToolLifecycleDiscoveryParams{
		Key:       activeKey,
		Reason:    "initial discovery",
		UpdatedBy: "discovery",
	})
	if err != nil {
		t.Fatalf("EnsureDiscoveredMCPToolLifecycleState(new) error = %v", err)
	}
	if !inserted || record.State != contract.MCPToolLifecycleStateActive ||
		record.Source != contract.MCPToolLifecycleSourceDiscovery {
		t.Fatalf("new record = %+v inserted=%v, want active discovery", record, inserted)
	}
}

func TestLifecycleStoreFailsFastOnUnknownStoredState(t *testing.T) {
	s := &lifecycleStore{q: lifecycleQuerierStub{
		getFn: func(context.Context, sqlc.GetMCPToolLifecycleStateParams) (sqlc.McpToolLifecycleState, error) {
			return sqlc.McpToolLifecycleState{
				WorkspaceRoot:  "/tmp/project",
				ServerName:     "sqlite",
				ToolName:       "query",
				LifecycleState: "paused",
				Source:         "user",
				CreatedAt:      1,
				UpdatedAt:      1,
			}, nil
		},
	}}
	_, err := s.GetMCPToolLifecycleState(context.Background(), contract.MCPToolLifecycleKey{
		WorkspaceRoot: "/tmp/project",
		ServerName:    "sqlite",
		ToolName:      "query",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid state") {
		t.Fatalf("GetMCPToolLifecycleState() error = %v, want invalid state", err)
	}
}

func TestLifecycleStoreMissingRecordReturnsNotFound(t *testing.T) {
	store, closeDB := newSQLiteLifecycleStore(t)
	defer closeDB()
	_, err := store.GetMCPToolLifecycleState(context.Background(), contract.MCPToolLifecycleKey{
		WorkspaceRoot: "/tmp/project",
		ServerName:    "sqlite",
		ToolName:      "missing",
	})
	if !errors.Is(err, platformdb.ErrNotFound) {
		t.Fatalf("GetMCPToolLifecycleState() error = %v, want not found", err)
	}
}

type lifecycleQuerierStub struct {
	getFn func(context.Context, sqlc.GetMCPToolLifecycleStateParams) (sqlc.McpToolLifecycleState, error)
}

func (s lifecycleQuerierStub) UpsertMCPToolLifecycleState(
	context.Context,
	sqlc.UpsertMCPToolLifecycleStateParams,
) (sqlc.McpToolLifecycleState, error) {
	return sqlc.McpToolLifecycleState{}, errors.New("unexpected upsert")
}

func (s lifecycleQuerierStub) InsertMCPToolLifecycleStateIfAbsent(
	context.Context,
	sqlc.InsertMCPToolLifecycleStateIfAbsentParams,
) (int64, error) {
	return 0, errors.New("unexpected insert")
}

func (s lifecycleQuerierStub) GetMCPToolLifecycleState(
	ctx context.Context,
	arg sqlc.GetMCPToolLifecycleStateParams,
) (sqlc.McpToolLifecycleState, error) {
	return s.getFn(ctx, arg)
}

func (s lifecycleQuerierStub) ListMCPToolLifecycleStatesByServer(
	context.Context,
	sqlc.ListMCPToolLifecycleStatesByServerParams,
) ([]sqlc.McpToolLifecycleState, error) {
	return nil, errors.New("unexpected list")
}

func newSQLiteLifecycleStore(t *testing.T) (contract.MCPToolLifecycleStore, func()) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	applyLifecycleMigration(t, db)
	return NewMCPToolLifecycleStore(sqlc.New(db)), func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	}
}

func applyLifecycleMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "platform", "db", "sqlite", "migrations", "109_mcp_tool_lifecycle_states.sql"))
	if err != nil {
		t.Fatalf("read lifecycle migration: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("apply lifecycle migration: %v", err)
	}
}
