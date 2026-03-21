package uistate

import (
	"context"
	"encoding/json"
	"testing"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

func TestConfigHandlersReadAndWriteLSPPromptHint(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", lspPromptHintOverrideKey): mustJSONRaw(t, "custom prompt"),
	}}
	files := &sharedFileStoreStub{files: map[string]sharedfilestore.SharedFile{
		lspPromptHintDefaultPath: {Path: lspPromptHintDefaultPath, Content: "default prompt"},
	}}
	server := newConfigTestServer(
		&platformconfig.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/window"},
		prefs,
		files,
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
		&sharedFileStoreStub{},
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
	files sharedfilestore.Store,
) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: cfg})
	server.Register(NewConfigHandlers(cfg, prefs, files).Handlers)
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

type sharedFileStoreStub struct {
	files map[string]sharedfilestore.SharedFile
}

func (s *sharedFileStoreStub) Upsert(context.Context, sharedfilestore.UpsertParams) (*sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (s *sharedFileStoreStub) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	file, ok := s.files[path]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	out := file
	return &out, nil
}

func (s *sharedFileStoreStub) List(context.Context, sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	return nil, nil
}

func (s *sharedFileStoreStub) Delete(context.Context, string) (int64, error) {
	return 0, nil
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
