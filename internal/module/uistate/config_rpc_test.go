package uistate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestConfigHandlersReadAndWriteLSPPromptHint(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", lspPromptHintOverrideKey): mustJSONRaw(t, "custom prompt"),
	}}
	db := &configDBTXStub{files: map[string]sqlc.SharedFile{
		lspPromptHintDefaultPath: {Path: lspPromptHintDefaultPath, Content: "default prompt"},
	}}
	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/window"},
		prefs,
		sqlc.New(db),
	)

	cfg := dispatchConfig[runtimeConfigResult](t, server, "config/read", `{}`)
	if cfg.CWD != "/window" {
		t.Fatalf("config/read cwd = %q", cfg.CWD)
	}

	readRes := dispatchConfig[lspPromptHintResult](t, server, "config/lspPromptHint/read", `{"cwd":"/repo"}`)
	if readRes.Hint != "custom prompt" || readRes.DefaultHint != "default prompt" || readRes.UsingDefault {
		t.Fatalf("config/lspPromptHint/read = %#v", readRes)
	}

	writeRes := dispatchConfig[lspPromptHintResult](t, server, "config/lspPromptHint/write", `{"cwd":"/repo","hint":""}`)
	if writeRes.Hint != "default prompt" || !writeRes.UsingDefault || writeRes.OverrideHint != "" {
		t.Fatalf("config/lspPromptHint/write = %#v", writeRes)
	}

	raw, err := prefs.GetValue(context.Background(), "/repo", lspPromptHintOverrideKey)
	if err != nil {
		t.Fatalf("GetValue() error = %v", err)
	}
	if string(raw) != `""` {
		t.Fatalf("stored override = %s", raw)
	}
}

func TestConfigPromptHintWriteRequiresPreferenceStore(t *testing.T) {
	t.Parallel()

	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/window"},
		nil,
		sqlc.New(&configDBTXStub{}),
	)

	if _, err := server.Dispatch(
		context.Background(),
		"config/lspPromptHint/write",
		json.RawMessage(`{"cwd":"/repo","hint":"custom"}`),
	); err == nil {
		t.Fatal("Dispatch(config/lspPromptHint/write) error = nil, want missing preference store")
	}
}

func newConfigTestServer(
	cfg *platformconfig.Config,
	prefs uipreference.Store,
	queries *sqlc.Queries,
) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: cfg})
	server.Register(NewConfigHandlers(cfg, prefs, queries).Handlers)
	return server
}

func dispatchConfig[T any](t *testing.T, server *platformrpc.Server, method, raw string) T {
	t.Helper()

	result, err := server.Dispatch(context.Background(), method, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Dispatch(%q) error = %v", method, err)
	}
	var value T
	if err := json.Unmarshal(result, &value); err != nil {
		t.Fatalf("json.Unmarshal(%q) error = %v", method, err)
	}
	return value
}

type uiPreferenceStoreStub struct {
	values map[string]json.RawMessage
}

func (s *uiPreferenceStoreStub) GetValue(_ context.Context, cwd, key string) (json.RawMessage, error) {
	raw, ok := s.values[preferenceStubKey(cwd, key)]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	return append(json.RawMessage(nil), raw...), nil
}

func (s *uiPreferenceStoreStub) Upsert(_ context.Context, params uipreference.UpsertParams) error {
	if s.values == nil {
		s.values = map[string]json.RawMessage{}
	}
	s.values[preferenceStubKey(params.Cwd, params.Key)] = append(json.RawMessage(nil), params.Value...)
	return nil
}

func (s *uiPreferenceStoreStub) List(_ context.Context, cwd string) ([]uipreference.UIPreference, error) {
	rows := []uipreference.UIPreference{}
	for rawKey, value := range s.values {
		rowCwd, rowKey := splitPreferenceStubKey(rawKey)
		if rowCwd == cwd {
			rows = append(rows, uipreference.UIPreference{Cwd: rowCwd, Key: rowKey, Value: append(json.RawMessage(nil), value...)})
		}
	}
	return rows, nil
}

// configDBTXStub is a minimal DBTX stub that supports GetSharedFile queries.
type configDBTXStub struct {
	files map[string]sqlc.SharedFile
}

func (s *configDBTXStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("configDBTXStub: exec not implemented")
}

func (s *configDBTXStub) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("configDBTXStub: query not implemented")
}

func (s *configDBTXStub) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	if len(args) > 0 {
		if path, ok := args[0].(string); ok && s.files != nil {
			if file, exists := s.files[path]; exists {
				return &configRowStub{file: &file}
			}
		}
	}
	return &configRowStub{err: platformdb.ErrNotFound}
}

type configRowStub struct {
	file *sqlc.SharedFile
	err  error
}

func (r *configRowStub) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if r.file == nil || len(dest) < 5 {
		return errors.New("configRowStub: insufficient scan targets")
	}
	// GetSharedFile returns: path, content, updated_by, created_at, updated_at
	if p, ok := dest[0].(*string); ok {
		*p = r.file.Path
	}
	if p, ok := dest[1].(*string); ok {
		*p = r.file.Content
	}
	if p, ok := dest[2].(*string); ok {
		*p = r.file.UpdatedBy
	}
	// created_at and updated_at are time.Time, leave as zero values
	return nil
}

func preferenceStubKey(cwd, key string) string {
	return cwd + "\x00" + key
}

func splitPreferenceStubKey(value string) (string, string) {
	for i := 0; i < len(value); i++ {
		if value[i] == 0 {
			return value[:i], value[i+1:]
		}
	}
	return "", value
}

func mustJSONRaw(t *testing.T, value string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return raw
}
