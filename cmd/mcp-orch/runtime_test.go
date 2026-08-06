package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools/modelregistry"
	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/bootstrap"
	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	_ "modernc.org/sqlite"
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

func (s *stubBootstrapClient) InstallLogRelay(*pkglogger.Runtime) {
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

func TestVerifyMCPOrchDatabasePingFailsFast(t *testing.T) {
	db := newRuntimeSQLiteDB(t)
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	err := verifyMCPOrchDatabaseReady(context.Background(), db)
	if err == nil || !strings.Contains(err.Error(), "mcp-orch database ping failed") {
		t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want ping failure", err)
	}
}

func TestVerifyMCPOrchDatabaseSchemaMissingFailsFast(t *testing.T) {
	db := newRuntimeSQLiteDB(t)

	err := verifyMCPOrchDatabaseReady(context.Background(), db)

	if err == nil || !strings.Contains(err.Error(), "mcp-orch database schema check failed") {
		t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want schema check failure", err)
	}
}

func TestVerifyMCPOrchDatabaseSchemaVersionBelowMinimumFailsFast(t *testing.T) {
	db := newRuntimeSQLiteDB(t)
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1)`, platformdb.MinRequiredSchemaVersion-1); err != nil {
		t.Fatalf("insert schema_migrations: %v", err)
	}

	err := verifyMCPOrchDatabaseReady(context.Background(), db)

	if err == nil {
		t.Fatal("verifyMCPOrchDatabaseReady() error = nil, want below-minimum schema failure")
	}
	for _, want := range []string{"mcp-orch database schema check failed", "database migration version"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("verifyMCPOrchDatabaseReady() error = %v, want %q", err, want)
		}
	}
}

func newRuntimeSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func runtimeSQLiteMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "internal", "platform", "db", "sqlite", "migrations"))
	if err != nil {
		t.Fatalf("resolve sqlite migrations dir: %v", err)
	}
	return dir
}

func TestNewLoggerFallbackWarnsToStderr(t *testing.T) {
	var stderr bytes.Buffer
	openErr := errors.New("open denied")

	newLoggerWithOpenFile(pkglogger.NewRuntime(pkglogger.RuntimeConfig{}), nil, func(string, int, os.FileMode) (*os.File, error) {
		return nil, openErr
	}, &stderr)

	if got := stderr.String(); !strings.Contains(got, "mcp-orch logger fallback to stderr") {
		t.Fatalf("stderr = %q, want fallback warning", got)
	}
}

func TestNewLoggerFallbackDoesNotWriteStdout(t *testing.T) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	originalStdout := os.Stdout
	os.Stdout = stdoutW
	t.Cleanup(func() {
		os.Stdout = originalStdout
		_ = stdoutR.Close()
	})

	var stderr bytes.Buffer
	newLoggerWithOpenFile(pkglogger.NewRuntime(pkglogger.RuntimeConfig{}), nil, func(string, int, os.FileMode) (*os.File, error) {
		return nil, errors.New("open denied")
	}, &stderr)

	os.Stdout = originalStdout
	if err := stdoutW.Close(); err != nil {
		t.Fatalf("close captured stdout writer: %v", err)
	}
	stdoutBytes, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	if len(stdoutBytes) != 0 {
		t.Fatalf("stdout = %q, want empty", string(stdoutBytes))
	}
	if got := stderr.String(); !strings.Contains(got, "mcp-orch logger fallback to stderr") {
		t.Fatalf("stderr = %q, want fallback warning", got)
	}
}

func TestNewLoggerOpenSuccessSkipsFallbackWarning(t *testing.T) {
	logFile, err := os.OpenFile(filepath.Join(t.TempDir(), "mcp-orch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open temp log file: %v", err)
	}
	t.Cleanup(func() { _ = logFile.Close() })

	var stderr bytes.Buffer
	newLoggerWithOpenFile(pkglogger.NewRuntime(pkglogger.RuntimeConfig{}), nil, func(string, int, os.FileMode) (*os.File, error) {
		return logFile, nil
	}, &stderr)

	if got := stderr.String(); strings.Contains(got, "mcp-orch logger fallback to stderr") {
		t.Fatalf("stderr = %q, want no fallback warning", got)
	}
}

func TestBootstrapRunnerSkipsStartWhenRPCAddrMissing(t *testing.T) {
	t.Parallel()

	client := &stubBootstrapClient{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		done <- bootstrapRunner{client: client}.Run(ctx)
	})

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

	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		done <- bootstrapRunner{
			cfg:    bootstrap.Config{RPCAddr: "127.0.0.1:9123", BinaryName: "mcp-orch", Metrics: platformmetrics.NewBootstrapMetrics()},
			client: client,
		}.Run(ctx)
	})

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
		cfg:    bootstrap.Config{RPCAddr: "127.0.0.1:9123", BinaryName: "mcp-orch", Metrics: platformmetrics.NewBootstrapMetrics()},
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
