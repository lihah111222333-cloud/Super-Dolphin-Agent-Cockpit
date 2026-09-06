//go:build e2e

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone"
	agentstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/agent"
	directpeerstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/directpeer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/tools"
	mcpcommon "github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TestStandaloneDirectPeerPersistsAcrossRestart 验证真实 registry/store 跨 app 重启的授权、幂等消息和 cursor。
func TestStandaloneDirectPeerPersistsAcrossRestart(t *testing.T) {
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: requireExecutable(t), Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}}}
	db := directPeerStandaloneDatabase(t, state)
	persistDirectPeerAgents(t, db, workspace)
	first := startDirectPeerApp(t, cfg, db)
	grantDirectPeerThroughRegistry(t, first, workspace)
	firstMessage := sendDirectPeerThroughStore(t, first, workspace, "sender", "receiver", "message-1", "hello")
	assertReadDirectPeerThroughRegistry(t, first, workspace, "receiver", 0, firstMessage)
	stopStandaloneTestApp(t, first.app)

	second := startDirectPeerApp(t, cfg, db)
	retry := sendDirectPeerThroughStore(t, second, workspace, "sender", "receiver", "message-1", "hello")
	if retry != firstMessage {
		t.Fatalf("restart retry message=%q want=%q", retry, firstMessage)
	}
	assertReadDirectPeerThroughRegistry(t, second, workspace, "receiver", 0, firstMessage)
	revokeDirectPeerThroughRegistry(t, second, workspace)
	assertReadDirectPeerEmptyThroughRegistry(t, second, workspace)
	stopStandaloneTestApp(t, second.app)
	third := startDirectPeerApp(t, cfg, db)
	assertReadDirectPeerEmptyThroughRegistry(t, third, workspace)
	stopStandaloneTestApp(t, third.app)
}

// TestStandaloneDirectPeerSupportsBidirectionalConversation 验证双向授权下两个 root task 可独立往返对话。
func TestStandaloneDirectPeerSupportsBidirectionalConversation(t *testing.T) {
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: requireExecutable(t), Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}}}
	db := directPeerStandaloneDatabase(t, state)
	persistDirectPeerAgents(t, db, workspace)
	app := startDirectPeerApp(t, cfg, db)
	grantDirectPeerDirectionThroughRegistry(t, app, workspace, "sender", "receiver", "grant-sender")
	grantDirectPeerDirectionThroughRegistry(t, app, workspace, "receiver", "sender", "grant-receiver")
	senderMessage := sendDirectPeerThroughStore(t, app, workspace, "sender", "receiver", "conversation-1", "hello receiver")
	receiverMessage := sendDirectPeerThroughStore(t, app, workspace, "receiver", "sender", "conversation-1", "hello sender")
	assertReadDirectPeerThroughRegistry(t, app, workspace, "receiver", 0, senderMessage)
	assertReadDirectPeerThroughRegistry(t, app, workspace, "sender", 0, receiverMessage)
	assertReadDirectPeerEventsThroughRegistry(t, app, workspace, "receiver", []string{"grant.active", "message.accepted"})
	assertReadDirectPeerEventsThroughRegistry(t, app, workspace, "sender", []string{"grant.active", "message.accepted"})
	stopStandaloneTestApp(t, app.app)
}

// TestStandaloneDirectPeerOutboxProjectsEvent 验证 standalone runner 消费 outbox 后发布 durable 事件通知。
func TestStandaloneDirectPeerOutboxProjectsEvent(t *testing.T) {
	workspace, db, notifications, app := newDirectPeerProjectionApp(t)
	defer stopStandaloneTestApp(t, app)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := directpeerstore.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "projection-grant", true, true)
	if err != nil {
		t.Fatal(err)
	}
	awaitDirectPeerProjection(t, ctx, notifications, grant.GrantID)
	assertDirectPeerOutboxProjected(t, db)
}

func newDirectPeerProjectionApp(t *testing.T) (string, *sql.DB, chan directpeerstore.PublishedEvent, *fx.App) {
	t.Helper()
	workspace, state := t.TempDir(), t.TempDir()
	cfg := standalone.Config{SchemaVersion: 1, PrincipalRef: "operator/test", ScopeRef: "workspace/test", WorkspaceRoot: workspace, StateDir: state,
		Listen: "127.0.0.1:0", CredentialFile: filepath.Join(state, "unused-token"),
		Providers: []standalone.Provider{{Name: "codex", Executable: requireExecutable(t), Home: t.TempDir(), Models: []string{"test-model"}, Sandbox: "read-only"}}}
	db := directPeerStandaloneDatabase(t, state)
	notifications := make(chan directpeerstore.PublishedEvent, 1)
	readyDir := t.TempDir()
	if err := os.Chmod(readyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	app := newStandaloneTestApp(t, cfg, "test-only-operator-token-value", filepath.Join(readyDir, "ready.json"), db,
		fx.Invoke(func(dispatcher *event.Dispatcher) {
			event.Subscribe(dispatcher, func(notification directpeerstore.PublishedEvent) { notifications <- notification })
		}))
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return workspace, db, notifications, app
}

func awaitDirectPeerProjection(t *testing.T, ctx context.Context, notifications <-chan directpeerstore.PublishedEvent, grantID string) {
	t.Helper()
	select {
	case notification := <-notifications:
		if notification.Event.EventID == "" || notification.Event.GrantID != grantID || notification.Event.EventType != "grant.active" {
			t.Fatalf("projected notification=%#v", notification)
		}
	case <-ctx.Done():
		t.Fatalf("direct peer event projection timeout: %v", ctx.Err())
	}
}

func assertDirectPeerOutboxProjected(t *testing.T, db *sql.DB) {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM orchestration_direct_peer_event_outbox ORDER BY id DESC LIMIT 1`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "projected" {
		t.Fatalf("direct peer event outbox status=%q want projected", status)
	}
}

func revokeDirectPeerThroughRegistry(t *testing.T, app directPeerTestApp, workspace string) {
	t.Helper()
	callDirectPeerRegistry(t, app.registry, "manage_direct_peer", mcpcommon.ToolScope{CWD: workspace},
		`{"action":"revoke","sender_agent_id":"sender","receiver_agent_id":"receiver","idempotency_key":"revoke-1"}`)
}

func assertReadDirectPeerEmptyThroughRegistry(t *testing.T, app directPeerTestApp, workspace string) {
	t.Helper()
	result := callDirectPeerRegistry(t, app.registry, "read_direct_peer", mcpcommon.ToolScope{CWD: workspace, AgentID: "receiver"},
		`{"after_cursor":0,"limit":10}`)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) != 0 {
		t.Fatalf("revoked direct peer messages=%s err=%v", raw, err)
	}
}

type directPeerTestApp struct {
	app      *fx.App
	registry tools.Registry
	store    *directpeerstore.Store
}

func startDirectPeerApp(t *testing.T, cfg standalone.Config, db *sql.DB) directPeerTestApp {
	t.Helper()
	var registry tools.Registry
	var store *directpeerstore.Store
	readyDir := t.TempDir()
	if err := os.Chmod(readyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	app := newStandaloneTestApp(t, cfg, "test-only-operator-token-value", filepath.Join(readyDir, "ready.json"), db, fx.Populate(&registry, &store))
	if err := app.Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return directPeerTestApp{app: app, registry: registry, store: store}
}

func grantDirectPeerThroughRegistry(t *testing.T, app directPeerTestApp, workspace string) {
	t.Helper()
	grantDirectPeerDirectionThroughRegistry(t, app, workspace, "sender", "receiver", "grant-1")
}

func grantDirectPeerDirectionThroughRegistry(t *testing.T, app directPeerTestApp, workspace, sender, receiver, key string) {
	t.Helper()
	input, err := json.Marshal(map[string]any{"action": "grant", "sender_agent_id": sender, "receiver_agent_id": receiver,
		"idempotency_key": key, "can_send": true, "can_read": true})
	if err != nil {
		t.Fatal(err)
	}
	callDirectPeerRegistry(t, app.registry, "manage_direct_peer", mcpcommon.ToolScope{CWD: workspace}, string(input))
}

func sendDirectPeerThroughStore(t *testing.T, app directPeerTestApp, workspace, sender, receiver, key, body string) string {
	t.Helper()
	message, err := app.store.Send(t.Context(), directpeerstore.SendRequest{WorkspaceRoot: workspace, SenderAgentID: sender,
		ReceiverAgentID: receiver, IdempotencyKey: key, Body: body})
	if err != nil || message.MessageID == "" {
		t.Fatalf("store send=%#v err=%v", message, err)
	}
	return message.MessageID
}

func assertReadDirectPeerThroughRegistry(t *testing.T, app directPeerTestApp, workspace, receiver string, cursor int64, messageID string) {
	t.Helper()
	result := callDirectPeerRegistry(t, app.registry, "read_direct_peer", mcpcommon.ToolScope{CWD: workspace, AgentID: receiver},
		`{"after_cursor":0,"limit":10}`)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var messages []struct {
		MessageID string `json:"message_id"`
		Cursor    int64  `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &messages); err != nil || len(messages) != 1 || messages[0].MessageID != messageID || messages[0].Cursor <= cursor {
		t.Fatalf("read messages=%s err=%v", raw, err)
	}
}

func assertReadDirectPeerEventsThroughRegistry(t *testing.T, app directPeerTestApp, workspace, receiver string, wantTypes []string) {
	t.Helper()
	result := callDirectPeerRegistry(t, app.registry, "read_direct_peer_events", mcpcommon.ToolScope{CWD: workspace, AgentID: receiver},
		`{"after_cursor":0,"limit":10}`)
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var events []struct {
		EventType string `json:"event_type"`
		Cursor    int64  `json:"cursor"`
	}
	if err := json.Unmarshal(raw, &events); err != nil || len(events) != len(wantTypes) {
		t.Fatalf("read events=%s err=%v", raw, err)
	}
	for index, event := range events {
		if event.EventType != wantTypes[index] || event.Cursor <= 0 {
			t.Fatalf("event[%d]=%#v want type=%q", index, event, wantTypes[index])
		}
	}
}

func callDirectPeerRegistry(t *testing.T, registry tools.Registry, name string, scope mcpcommon.ToolScope, input string) any {
	t.Helper()
	definition, ok := registry.Lookup(name)
	if !ok {
		t.Fatalf("tool %s missing", name)
	}
	result, err := definition.Handler(mcpcommon.WithToolScope(context.Background(), scope), json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func directPeerStandaloneDatabase(t *testing.T, state string) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(state, "orch.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func persistDirectPeerAgents(t *testing.T, db *sql.DB, workspace string) {
	t.Helper()
	store := agentstore.NewSessionStore(db)
	for _, agentID := range []string{"sender", "receiver"} {
		if err := store.CreateSession(t.Context(), standalone.SessionRecord{AgentID: agentID, ThreadID: agentID + "-thread",
			Provider: "codex", Name: agentID, CWD: workspace, Model: "test-model", CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
}

func requireExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
