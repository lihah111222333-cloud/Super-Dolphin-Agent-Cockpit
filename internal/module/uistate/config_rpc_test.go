package uistate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

func TestConfigHandlersReadAndWriteLSPPromptHint(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", lspPromptHintOverrideKey):                           mustJSONRaw(t, "custom prompt"),
		preferenceStubKey("/window", normalizePreferenceKey(preferenceActiveThreadID)): mustJSONRaw(t, "thread-7"),
	}}
	sharedFiles := &sharedFileStoreStub{files: map[string]sharedfilestore.SharedFile{
		lspPromptHintDefaultPath: {Path: lspPromptHintDefaultPath, Content: "default prompt"},
	}}
	threads := &configThreadServiceStub{getConfigResult: dto.ThreadConfig{
		ThreadID: "thread-7",
		Effective: dto.ThreadConfigValues{
			Model:     "gpt-5.5",
			Approvals: "never",
		},
	}, runtimeConfigResult: map[string]any{
		"modelProvider":         "openai",
		"developerInstructions": "be precise",
		"personality":           "balanced",
		"sandbox": map[string]any{
			"type": "workspace-write",
		},
		"toolRouting": map[string]any{
			"mode":                "dynamic",
			"routerModel":         "router-1",
			"routerProvider":      "router-x",
			"routerBaseURL":       "https://router.example",
			"routerHasAPIKey":     true,
			"confidenceThreshold": 0.9,
			"timeoutSec":          11,
		},
	}}
	server := newConfigTestServer(
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/window"},
		prefs,
		sharedFiles,
		threads,
	)

	cfg := dispatchConfig[runtimeConfigResult](t, server, "config/read", `{}`)
	if cfg.CWD != "/window" || cfg.Model != "gpt-5.5" || cfg.ApprovalPolicy != "never" {
		t.Fatalf("config/read = %#v", cfg)
	}
	if len(threads.getConfigIDs) != 1 || threads.getConfigIDs[0] != "thread-7" {
		t.Fatalf("GetConfig thread ids = %#v, want [thread-7]", threads.getConfigIDs)
	}
	if len(threads.runtimeConfigIDs) != 1 || threads.runtimeConfigIDs[0] != "thread-7" {
		t.Fatalf("ReadRuntimeConfig thread ids = %#v, want [thread-7]", threads.runtimeConfigIDs)
	}
	if cfg.ModelProvider != "openai" || cfg.Config != nil || cfg.BaseInstructions != nil || cfg.DeveloperInstructions != "be precise" || cfg.Personality != "balanced" {
		t.Fatalf("config/read nullable defaults = %#v", cfg)
	}
	if cfg.ToolRouting != (runtimeConfigToolRouting{
		Mode:                "dynamic",
		RouterModel:         "router-1",
		RouterProvider:      "router-x",
		RouterBaseURL:       "https://router.example",
		RouterHasAPIKey:     true,
		ConfidenceThreshold: 0.9,
		TimeoutSec:          11,
	}) {
		t.Fatalf("config/read toolRouting = %#v", cfg.ToolRouting)
	}
	sandbox, _ := cfg.Sandbox.(map[string]any)
	if sandbox["type"] != "workspace-write" {
		t.Fatalf("config/read sandbox = %#v", cfg.Sandbox)
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

func TestConfigReadFallsBackToDefaultsWhenThreadConfigUnavailable(t *testing.T) {
	t.Parallel()

	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/window", normalizePreferenceKey(preferenceActiveThreadID)): mustJSONRaw(t, "thread-9"),
	}}
	threads := &configThreadServiceStub{
		getConfigErr: errors.New("session offline"),
		runtimeConfigResult: map[string]any{
			"toolRouting": map[string]any{
				"mode": "legacy",
			},
		},
	}
	server := newConfigTestServer(
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/window"},
		prefs,
		&sharedFileStoreStub{},
		threads,
	)

	cfg := dispatchConfig[runtimeConfigResult](t, server, "config/read", `{}`)
	if cfg.CWD != "/window" || cfg.Model != "o4-mini" || cfg.ApprovalPolicy != "on-failure" {
		t.Fatalf("config/read fallback = %#v", cfg)
	}
	if cfg.ToolRouting != (runtimeConfigToolRouting{
		Mode:                "legacy",
		RouterModel:         "",
		RouterProvider:      "openai_compatible",
		RouterBaseURL:       "",
		RouterHasAPIKey:     false,
		ConfidenceThreshold: 0.65,
		TimeoutSec:          8,
	}) {
		t.Fatalf("config/read toolRouting fallback = %#v", cfg.ToolRouting)
	}
	if len(threads.getConfigIDs) != 1 || threads.getConfigIDs[0] != "thread-9" {
		t.Fatalf("GetConfig thread ids = %#v, want [thread-9]", threads.getConfigIDs)
	}
	if len(threads.runtimeConfigIDs) != 1 || threads.runtimeConfigIDs[0] != "thread-9" {
		t.Fatalf("ReadRuntimeConfig thread ids = %#v, want [thread-9]", threads.runtimeConfigIDs)
	}
}

func TestConfigPromptHintWriteRequiresPreferenceStore(t *testing.T) {
	t.Parallel()

	server := newConfigTestServer(
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: "/window"},
		nil,
		&sharedFileStoreStub{},
		nil,
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
	cfg *contract.Config,
	prefs uipreference.Store,
	sharedFiles sharedfilestore.Reader,
	threads contract.ThreadConfigReader,
) *platformrpc.Server {
	server := platformrpc.NewServer(platformrpc.Params{Config: cfg})
	server.Register(NewConfigHandlers(cfg, prefs, sharedFiles, threads, nil, testNativeTools).Handlers)
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

func (s *sharedFileStoreStub) Get(_ context.Context, path string) (*sharedfilestore.SharedFile, error) {
	if s.files == nil {
		return nil, platformdb.ErrNotFound
	}
	file, ok := s.files[path]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	return &file, nil
}

func (s *sharedFileStoreStub) List(_ context.Context, filter sharedfilestore.ListFilter) ([]sharedfilestore.SharedFile, error) {
	rows := make([]sharedfilestore.SharedFile, 0, len(s.files))
	for path, file := range s.files {
		if filter.Prefix == "" || len(path) >= len(filter.Prefix) && path[:len(filter.Prefix)] == filter.Prefix {
			rows = append(rows, file)
		}
	}
	return rows, nil
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

// configThreadServiceStub implements contract.ThreadConfigReader and
// contract.ThreadRuntimeConfigReader for config_rpc tests.
type configThreadServiceStub struct {
	getConfigResult     dto.ThreadConfig
	getConfigErr        error
	getConfigIDs        []string
	runtimeConfigResult map[string]any
	runtimeConfigErr    error
	runtimeConfigIDs    []string
}

func (s *configThreadServiceStub) GetConfig(_ context.Context, threadID string) (dto.ThreadConfig, error) {
	s.getConfigIDs = append(s.getConfigIDs, threadID)
	return s.getConfigResult, s.getConfigErr
}

func (s *configThreadServiceStub) ReadRuntimeConfig(_ context.Context, threadID string) (map[string]any, error) {
	s.runtimeConfigIDs = append(s.runtimeConfigIDs, threadID)
	return s.runtimeConfigResult, s.runtimeConfigErr
}
