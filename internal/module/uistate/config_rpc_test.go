package uistate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestConfigHandlersReadAndWriteLSPPromptHint(t *testing.T) {
	t.Parallel()

	server, prefs, threads, projectRoot := newConfigPromptHintFixture(t)
	cfg := dispatchConfig[runtimeConfigResult](t, server, "config/read", `{}`)
	assertConfigReadResult(t, cfg, threads, projectRoot)
	assertLSPPromptHintRead(t, server)
	assertLSPPromptHintWrite(t, server)
	assertStoredLSPPromptHintOverride(t, prefs)
}

func TestDefaultRuntimeConfigRoutesVideoRequestsToVideoWithAudio(t *testing.T) {
	t.Parallel()

	cfg := defaultRuntimeConfig(&contract.Config{})
	instructions, ok := cfg.DeveloperInstructions.(string)
	if !ok {
		t.Fatalf("DeveloperInstructions type = %T, want string", cfg.DeveloperInstructions)
	}
	if !strings.Contains(instructions, "video_with_audio") {
		t.Fatalf("DeveloperInstructions = %q, want video_with_audio guidance", instructions)
	}
	if strings.Contains(instructions, "access to a `video_generate` MCP tool") {
		t.Fatalf("DeveloperInstructions = %q, must not advertise video_generate", instructions)
	}
	if !strings.Contains(instructions, "Do not call `video_generate`") {
		t.Fatalf("DeveloperInstructions = %q, want explicit video_generate ban", instructions)
	}
}

func newConfigPromptHintFixture(t *testing.T) (*platformrpc.Server, *uiPreferenceStoreStub, *configThreadServiceStub, string) {
	t.Helper()
	projectRoot := filepath.Clean(filepath.Join(t.TempDir(), "window"))
	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey("/repo", lspPromptHintOverrideKey):                             mustJSONRaw(t, "custom prompt"),
		preferenceStubKey(projectRoot, normalizePreferenceKey(preferenceActiveThreadID)): mustJSONRaw(t, "thread-7"),
	}}
	sharedFiles := &sharedFileStoreStub{files: map[string]SharedFile{
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
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: projectRoot},
		prefs,
		sharedFiles,
		threads,
	)
	return server, prefs, threads, projectRoot
}

func assertConfigReadResult(t *testing.T, cfg runtimeConfigResult, threads *configThreadServiceStub, projectRoot string) {
	t.Helper()
	assertConfigBasics(t, cfg, projectRoot)
	assertConfigThreadLookups(t, threads)
	assertConfigRuntimeFields(t, cfg)
	assertConfigToolRouting(t, cfg.ToolRouting)
	assertConfigSandbox(t, cfg.Sandbox)
}

func assertConfigBasics(t *testing.T, cfg runtimeConfigResult, projectRoot string) {
	t.Helper()
	if cfg.CWD != projectRoot || cfg.Model != "gpt-5.5" || cfg.ApprovalPolicy != "never" {
		t.Fatalf("config/read = %#v", cfg)
	}
}

func assertConfigThreadLookups(t *testing.T, threads *configThreadServiceStub) {
	t.Helper()
	if len(threads.getConfigIDs) != 1 || threads.getConfigIDs[0] != "thread-7" {
		t.Fatalf("GetConfig thread ids = %#v, want [thread-7]", threads.getConfigIDs)
	}
	if len(threads.runtimeConfigIDs) != 1 || threads.runtimeConfigIDs[0] != "thread-7" {
		t.Fatalf("ReadRuntimeConfig thread ids = %#v, want [thread-7]", threads.runtimeConfigIDs)
	}
}

func assertConfigRuntimeFields(t *testing.T, cfg runtimeConfigResult) {
	t.Helper()
	if cfg.ModelProvider != "openai" || cfg.Config != nil || cfg.BaseInstructions != nil || cfg.DeveloperInstructions != "be precise" || cfg.Personality != "balanced" {
		t.Fatalf("config/read nullable defaults = %#v", cfg)
	}
}

func assertConfigToolRouting(t *testing.T, got runtimeConfigToolRouting) {
	t.Helper()
	if got != (runtimeConfigToolRouting{
		Mode:                "dynamic",
		RouterModel:         "router-1",
		RouterProvider:      "router-x",
		RouterBaseURL:       "https://router.example",
		RouterHasAPIKey:     true,
		ConfidenceThreshold: 0.9,
		TimeoutSec:          11,
	}) {
		t.Fatalf("config/read toolRouting = %#v", got)
	}
}

func assertConfigSandbox(t *testing.T, got any) {
	t.Helper()
	sandbox, _ := got.(map[string]any)
	if sandbox["type"] != "workspace-write" {
		t.Fatalf("config/read sandbox = %#v", got)
	}
}

func assertLSPPromptHintRead(t *testing.T, server *platformrpc.Server) {
	t.Helper()
	readRes := dispatchConfig[lspPromptHintResult](t, server, "config/lspPromptHint/read", `{"cwd":"/repo"}`)
	if readRes.Hint != "custom prompt" || readRes.DefaultHint != "default prompt" || readRes.UsingDefault {
		t.Fatalf("config/lspPromptHint/read = %#v", readRes)
	}
}

func assertLSPPromptHintWrite(t *testing.T, server *platformrpc.Server) {
	t.Helper()
	writeRes := dispatchConfig[lspPromptHintResult](t, server, "config/lspPromptHint/write", `{"cwd":"/repo","hint":""}`)
	if writeRes.Hint != "default prompt" || !writeRes.UsingDefault || writeRes.OverrideHint != "" {
		t.Fatalf("config/lspPromptHint/write = %#v", writeRes)
	}
}

func assertStoredLSPPromptHintOverride(t *testing.T, prefs *uiPreferenceStoreStub) {
	t.Helper()
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

	projectRoot := filepath.Clean(filepath.Join(t.TempDir(), "window"))
	prefs := &uiPreferenceStoreStub{values: map[string]json.RawMessage{
		preferenceStubKey(projectRoot, normalizePreferenceKey(preferenceActiveThreadID)): mustJSONRaw(t, "thread-9"),
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
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: projectRoot},
		prefs,
		&sharedFileStoreStub{},
		threads,
	)

	cfg := dispatchConfig[runtimeConfigResult](t, server, "config/read", `{}`)
	if cfg.CWD != projectRoot || cfg.Model != "gpt-5.5" || cfg.ApprovalPolicy != "on-failure" {
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

func TestConfigReadUsesPackagedHomeCWDInsteadOfResources(t *testing.T) {
	resourcesRoot := t.TempDir()
	manifestPath := filepath.Join(resourcesRoot, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"app":"Super Dolphin"}`), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
	packagedHome := filepath.Clean(filepath.Join(t.TempDir(), "Library", "Application Support", "Super Dolphin"))
	if err := os.MkdirAll(packagedHome, 0o755); err != nil {
		t.Fatalf("mkdir packaged home: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", packagedHome)

	threads := &configThreadServiceStub{getConfigResult: dto.ThreadConfig{
		ThreadID: "thread-packaged",
		Effective: dto.ThreadConfigValues{
			Model: "gpt-5.5",
		},
	}}
	server := newConfigTestServer(
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: resourcesRoot},
		&uiPreferenceStoreStub{values: map[string]json.RawMessage{
			preferenceStubKey(packagedHome, normalizePreferenceKey(preferenceActiveThreadID)): mustJSONRaw(t, "thread-packaged"),
		}},
		&sharedFileStoreStub{},
		threads,
	)

	cfg := dispatchConfig[runtimeConfigResult](t, server, "config/read", `{}`)
	if cfg.CWD != packagedHome {
		t.Fatalf("config/read cwd = %q, want packaged home cwd %q", cfg.CWD, packagedHome)
	}
	if cfg.Model != "gpt-5.5" {
		t.Fatalf("config/read model = %q, want packaged thread override", cfg.Model)
	}
	if len(threads.getConfigIDs) != 1 || threads.getConfigIDs[0] != "thread-packaged" {
		t.Fatalf("GetConfig thread ids = %#v, want [thread-packaged]", threads.getConfigIDs)
	}
	if len(threads.runtimeConfigIDs) != 1 || threads.runtimeConfigIDs[0] != "thread-packaged" {
		t.Fatalf("ReadRuntimeConfig thread ids = %#v, want [thread-packaged]", threads.runtimeConfigIDs)
	}
}

func TestConfigReadFallsBackToUserHomeCWDWhenPackagedHomeUnset(t *testing.T) {
	resourcesRoot := t.TempDir()
	manifestPath := filepath.Join(resourcesRoot, "runtime-manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"app":"Super Dolphin"}`), 0o644); err != nil {
		t.Fatalf("write runtime manifest: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", "")

	threads := &configThreadServiceStub{}
	server := newConfigTestServer(
		&contract.Config{RPCAddr: "127.0.0.1:0", ProjectRoot: resourcesRoot},
		&uiPreferenceStoreStub{values: map[string]json.RawMessage{}},
		&sharedFileStoreStub{},
		threads,
	)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine user home dir: %v", err)
	}
	wantCWD := filepath.Join(home, "Super Dolphin")

	cfg := dispatchConfig[runtimeConfigResult](t, server, "config/read", `{}`)
	if cfg.CWD != wantCWD {
		t.Fatalf("config/read cwd = %q, want fallback cwd %q", cfg.CWD, wantCWD)
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
	prefs PreferenceStore,
	sharedFiles SharedFileReader,
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

func (s *uiPreferenceStoreStub) Upsert(_ context.Context, params PreferenceUpsertParams) error {
	if s.values == nil {
		s.values = map[string]json.RawMessage{}
	}
	s.values[preferenceStubKey(params.Cwd, params.Key)] = append(json.RawMessage(nil), params.Value...)
	return nil
}

func (s *uiPreferenceStoreStub) List(_ context.Context, cwd string) ([]PreferenceEntry, error) {
	rows := []PreferenceEntry{}
	for rawKey, value := range s.values {
		rowCwd, rowKey := splitPreferenceStubKey(rawKey)
		if rowCwd == cwd {
			rows = append(rows, PreferenceEntry{Cwd: rowCwd, Key: rowKey, Value: append(json.RawMessage(nil), value...)})
		}
	}
	return rows, nil
}

type sharedFileStoreStub struct {
	files map[string]SharedFile
}

func (s *sharedFileStoreStub) Get(_ context.Context, path string) (*SharedFile, error) {
	if s.files == nil {
		return nil, platformdb.ErrNotFound
	}
	file, ok := s.files[path]
	if !ok {
		return nil, platformdb.ErrNotFound
	}
	return &file, nil
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

func (s *configThreadServiceStub) ReadRuntimeConfigs(ctx context.Context, threadIDs []string) (map[string]map[string]any, error) {
	s.runtimeConfigIDs = append(s.runtimeConfigIDs, threadIDs...)
	if s.runtimeConfigErr != nil {
		return nil, s.runtimeConfigErr
	}
	result := make(map[string]map[string]any)
	for _, id := range threadIDs {
		result[id] = s.runtimeConfigResult
	}
	return result, nil
}
