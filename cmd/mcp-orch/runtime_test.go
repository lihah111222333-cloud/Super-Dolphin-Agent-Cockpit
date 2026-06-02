package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/tools/modelregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common/bootstrap"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

type stubBootstrapClient struct {
	startErr     error
	subscribeErr error
	started      chan struct{}
	subscribed   chan struct{}

	startCalls     int
	closeCalls     int
	subscribeCalls int
	relayCalls     int

	subscriptionID string
	topics         []string
	mode           string
}

func (s *stubBootstrapClient) InstallLogRelay() {
	s.relayCalls++
}

func (s *stubBootstrapClient) Start(context.Context) error {
	s.startCalls++
	if s.started != nil {
		select {
		case <-s.started:
		default:
			close(s.started)
		}
	}
	return s.startErr
}

func (s *stubBootstrapClient) Close() error {
	s.closeCalls++
	return nil
}

func (s *stubBootstrapClient) SubscribeHooks(_ context.Context, subscriptionID string, topics []string, _ mcp.Selector, _ json.RawMessage, mode string) (*mcp.HookSubscribeResponse, error) {
	s.subscribeCalls++
	s.subscriptionID = subscriptionID
	s.topics = append([]string(nil), topics...)
	s.mode = mode
	if s.subscribed != nil {
		select {
		case <-s.subscribed:
		default:
			close(s.subscribed)
		}
	}
	return &mcp.HookSubscribeResponse{}, s.subscribeErr
}

type fakeMCPOrchDBRow struct {
	version int
	err     error
	seen    *bool
}

func (r fakeMCPOrchDBRow) Scan(dest ...any) error {
	if r.seen != nil {
		*r.seen = true
	}
	if r.err != nil {
		return r.err
	}
	if len(dest) != 1 {
		return errors.New("fake row expected one dest")
	}
	ptr, ok := dest[0].(*int)
	if !ok {
		return errors.New("fake row dest not *int")
	}
	*ptr = r.version
	return nil
}

type fakeMCPOrchDBProbe struct {
	pingErr error
	row     pgx.Row
	pinged  bool
}

func (p *fakeMCPOrchDBProbe) Ping(context.Context) error {
	p.pinged = true
	return p.pingErr
}

func (p *fakeMCPOrchDBProbe) QueryRow(context.Context, string, ...any) pgx.Row {
	return p.row
}

func TestRegisterPoolLifecycleFailsFastOnPoolPing(t *testing.T) {
	pool := newRuntimeTestPool(t, "postgres://super_dolphin@127.0.0.1:1/super_dolphin?sslmode=disable")
	cfg := &platformconfig.Config{
		EmbeddedPostgres: contract.EmbeddedPostgresConfig{
			Enabled: true,
			Owner:   true,
			BinDir:  filepath.Join(t.TempDir(), "missing-bin"),
		},
	}
	app := fx.New(
		fx.NopLogger,
		fx.Supply(slog.Default(), pool, cfg),
		fx.Invoke(registerPoolLifecycle),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := app.Start(ctx)
	if err == nil {
		t.Fatal("app.Start() error = nil, want database ping failure")
	}
	if !strings.Contains(err.Error(), "mcp-orch database ping failed") {
		t.Fatalf("app.Start() error = %v, want mcp-orch database ping failed", err)
	}
}

func TestVerifyMCPOrchDatabaseSchemaMissingFailsFast(t *testing.T) {
	sentinel := errors.New("relation schema_migrations does not exist")
	probe := &fakeMCPOrchDBProbe{row: fakeMCPOrchDBRow{err: sentinel}}

	err := verifyMCPOrchDatabaseReady(context.Background(), probe)

	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want schema_migrations error", err)
	}
	if !probe.pinged {
		t.Fatal("verifyMCPOrchDatabaseReady() did not ping database before schema check")
	}
	if !strings.Contains(err.Error(), "mcp-orch database schema check failed") {
		t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want schema check failure", err)
	}
}

func TestVerifyMCPOrchDatabaseSchemaVersionBelowMinimumFailsFast(t *testing.T) {
	probe := &fakeMCPOrchDBProbe{row: fakeMCPOrchDBRow{version: platformdb.MinRequiredSchemaVersion - 1}}

	err := verifyMCPOrchDatabaseReady(context.Background(), probe)

	if err == nil {
		t.Fatal("verifyMCPOrchDatabaseReady() error = nil, want below-minimum schema failure")
	}
	for _, want := range []string{"mcp-orch database schema check failed", "database migration version"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want %q", err, want)
		}
	}
}

func TestVerifyMCPOrchDatabaseSchemaReadySucceeds(t *testing.T) {
	seen := false
	probe := &fakeMCPOrchDBProbe{row: fakeMCPOrchDBRow{version: platformdb.MinRequiredSchemaVersion, seen: &seen}}

	if err := verifyMCPOrchDatabaseReady(context.Background(), probe); err != nil {
		t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want nil", err)
	}
	if !probe.pinged || !seen {
		t.Fatalf("verifyMCPOrchDatabaseReady() pinged=%v scanned=%v, want both true", probe.pinged, seen)
	}
}

func newRuntimeTestPool(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		t.Fatalf("NewWithConfig() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestBootstrapRunnerSkipsStartWhenRPCAddrMissing(t *testing.T) {
	t.Parallel()

	client := &stubBootstrapClient{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- bootstrapRunner{client: client}.Run(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancel")
	}

	if client.startCalls != 0 {
		t.Fatalf("Start() calls = %d, want 0", client.startCalls)
	}
	if client.subscribeCalls != 0 {
		t.Fatalf("SubscribeHooks() calls = %d, want 0", client.subscribeCalls)
	}
	if client.closeCalls != 0 {
		t.Fatalf("Close() calls = %d, want 0", client.closeCalls)
	}
}

func TestBootstrapRunnerStartsAndSubscribesWhenRPCAddrPresent(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")

	client := &stubBootstrapClient{
		started:    make(chan struct{}),
		subscribed: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- bootstrapRunner{
			cfg:    bootstrap.Config{RPCAddr: "127.0.0.1:9123", BinaryName: "mcp-orch"},
			client: client,
		}.Run(ctx)
	}()

	waitForBootstrapSignal(t, client.started, "Start() was not called")
	waitForBootstrapSignal(t, client.subscribed, "SubscribeHooks() was not called")

	cancel()
	waitForBootstrapRunDone(t, done)
	assertBootstrapClientStartState(t, client)
	assertBootstrapSubscription(t, client)
}

func TestBootstrapRunnerFailsWhenHookSubscriptionFails(t *testing.T) {
	t.Setenv("GO_AGENT_PEER_MODE", "1")

	subscribeErr := errors.New("subscribe failed")
	client := &stubBootstrapClient{subscribeErr: subscribeErr}

	err := bootstrapRunner{
		cfg:    bootstrap.Config{RPCAddr: "127.0.0.1:9123", BinaryName: "mcp-orch"},
		client: client,
	}.Run(context.Background())
	if !errors.Is(err, subscribeErr) {
		t.Fatalf("Run() error = %v, want subscribe failed", err)
	}
	if client.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1 after hook subscribe failure", client.closeCalls)
	}
}

func waitForBootstrapSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func waitForBootstrapRunDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after cancel")
	}
}

func assertBootstrapClientStartState(t *testing.T, client *stubBootstrapClient) {
	t.Helper()
	if client.startCalls != 1 {
		t.Fatalf("Start() calls = %d, want 1", client.startCalls)
	}
	if client.subscribeCalls != 1 {
		t.Fatalf("SubscribeHooks() calls = %d, want 1", client.subscribeCalls)
	}
	if client.closeCalls != 1 {
		t.Fatalf("Close() calls = %d, want 1", client.closeCalls)
	}
}

func assertBootstrapSubscription(t *testing.T, client *stubBootstrapClient) {
	t.Helper()
	if client.subscriptionID != orchestrationHookSubscriptionID {
		t.Fatalf("subscriptionID = %q, want %q", client.subscriptionID, orchestrationHookSubscriptionID)
	}
	if client.mode != "sync" {
		t.Fatalf("mode = %q, want sync", client.mode)
	}
	if len(client.topics) != len(orchestrationHookTopics) {
		t.Fatalf("topics = %v, want %v", client.topics, orchestrationHookTopics)
	}
	for i, want := range orchestrationHookTopics {
		if client.topics[i] != want {
			t.Fatalf("topics[%d] = %q, want %q", i, client.topics[i], want)
		}
	}
}

func TestNewModelRegistryFailsWhenDefaultRegistryFails(t *testing.T) {
	t.Setenv(modelregistry.EnvRegistryPath, filepath.Join(t.TempDir(), "missing.yaml"))
	var logs bytes.Buffer

	registry, err := newModelRegistry(slog.New(slog.NewTextHandler(&logs, nil)))
	if err == nil {
		t.Fatalf("newModelRegistry() error = nil, registry = %#v", registry)
	}
	if !strings.Contains(err.Error(), "model registry load failed") {
		t.Fatalf("newModelRegistry() error = %v, want model registry load failed", err)
	}
	if logs.String() != "" {
		t.Fatalf("logs = %q, want no fallback warning", logs.String())
	}
}
