package directpeer

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

// TestDirectPeerStoreContract 覆盖授权、幂等、cursor 和撤权的持久原子语义。
func TestDirectPeerStoreContract(t *testing.T) {
	ctx := context.Background()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	request := SendRequest{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "message-1", Body: "hello"}
	assertUnauthorizedDirectPeerSend(t, ctx, db, store, request)
	first := assertAuthorizedDirectPeerSend(t, ctx, db, store, request)
	assertDirectPeerReadAndRevoke(t, ctx, store, request, first)
}

// TestDirectPeerEventHistoryAndOutbox 验证消息状态变更与事件/outbox 在同一持久域内可恢复。
func TestDirectPeerEventHistoryAndOutbox(t *testing.T) {
	ctx := context.Background()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-1", true, true); err != nil {
		t.Fatal(err)
	}
	message, err := store.Send(ctx, SendRequest{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "message-1", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := store.OwnerCapability()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkSubmittedByOwner(ctx, capability, message.MessageID, "submission-1"); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListEventsForReceiver(ctx, workspace, "receiver", 0, 10)
	if err != nil || len(events) != 3 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	assertDirectPeerEventSequence(t, events, []string{eventGrantActive, eventMessageAccepted, eventMessageSubmitted})
	assertPendingDirectPeerEvents(t, db, len(events))
}

// TestDirectPeerStoreRejectsMessageIDAsSubmissionID prevents a message key
// from being persisted as a provider receipt.
func TestDirectPeerStoreRejectsMessageIDAsSubmissionID(t *testing.T) {
	ctx := context.Background()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-1", true, true); err != nil {
		t.Fatal(err)
	}
	message, err := store.Send(ctx, SendRequest{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "message-1", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkSubmitted(ctx, message.MessageID, "submission-1"); !errors.Is(err, ErrOwnerCapability) {
		t.Fatalf("unauthenticated submission mutation error=%v", err)
	}
	if _, err := store.MarkSubmitted(ctx, message.MessageID, message.MessageID); err == nil {
		t.Fatal("message id was accepted as submission id")
	}
}

// TestDirectPeerStoreRejectsUnauthenticatedDeliveryFailure prevents arbitrary
// callers from changing an accepted message into a durable failure receipt.
func TestDirectPeerStoreRejectsUnauthenticatedDeliveryFailure(t *testing.T) {
	ctx := context.Background()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-1", true, true); err != nil {
		t.Fatal(err)
	}
	message, err := store.Send(ctx, SendRequest{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "message-1", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkDeliveryFailed(ctx, message.MessageID, "forged failure"); !errors.Is(err, ErrOwnerCapability) {
		t.Fatalf("unauthenticated failure mutation error=%v", err)
	}
	messages, err := store.ReadReceiver(ctx, workspace, "receiver", 0, 10)
	if err != nil || len(messages) != 1 || messages[0].Status != "accepted" {
		t.Fatalf("unauthenticated failure changed message=%#v err=%v", messages, err)
	}
	for _, failure := range []string{strings.Repeat("x", 513), "invalid\x00failure"} {
		if _, err := store.MarkDeliveryFailed(ctx, message.MessageID, failure); err == nil {
			t.Fatalf("invalid failure %q was accepted", failure)
		}
	}
}

// TestDirectPeerOwnerCapabilityBindsStore verifies that a capability from
// another Store cannot mutate this Store.
func TestDirectPeerOwnerCapabilityBindsStore(t *testing.T) {
	ctx := context.Background()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	otherDB := directPeerTestDatabase(t)
	other, err := NewStore(otherDB)
	if err != nil {
		t.Fatal(err)
	}
	otherCapability, err := other.OwnerCapability()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkSubmittedByOwner(ctx, otherCapability, "message", "submission"); !errors.Is(err, ErrOwnerCapability) {
		t.Fatalf("cross-store capability error=%v", err)
	}
}

// TestDirectPeerOwnerCapabilitySupportsFailure verifies the legitimate
// internal owner path writes a bounded failure state.
func TestDirectPeerOwnerCapabilitySupportsFailure(t *testing.T) {
	ctx := context.Background()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	capability, err := store.OwnerCapability()
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-1", true, true); err != nil {
		t.Fatal(err)
	}
	message, err := store.Send(ctx, SendRequest{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "message-1", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkDeliveryFailedByOwner(ctx, capability, message.MessageID, "receiver unavailable"); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ReadReceiver(ctx, workspace, "receiver", 0, 10)
	if err != nil || len(messages) != 1 || messages[0].Status != "delivery_failed" {
		t.Fatalf("owner failure mutation=%#v err=%v", messages, err)
	}
}

// TestDirectPeerEventOutboxClaimLeaseAndFence 验证租约过期接管与旧 token 不能 ACK。
func TestDirectPeerEventOutboxClaimLeaseAndFence(t *testing.T) {
	ctx := context.Background()
	db, store := newDirectPeerOutboxFixture(t, ctx)
	first := claimDirectPeerOutbox(t, ctx, store, "worker-a")
	assertDirectPeerOutboxNotDuplicated(t, ctx, store)
	expireDirectPeerOutbox(t, ctx, db, first.ID)
	second := claimDirectPeerOutboxTakeover(t, ctx, store)
	assertDirectPeerOutboxFence(t, ctx, store, first, second)
	if replay, err := store.ClaimDirectPeerEventOutbox(ctx, "worker-c", time.Minute, 10); err != nil || len(replay) != 0 {
		t.Fatalf("projected event replay=%#v err=%v", replay, err)
	}
}

func newDirectPeerOutboxFixture(t *testing.T, ctx context.Context) (*sql.DB, *Store) {
	t.Helper()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-1", true, true); err != nil {
		t.Fatal(err)
	}
	return db, store
}

func claimDirectPeerOutbox(t *testing.T, ctx context.Context, store *Store, worker string) EventOutboxItem {
	t.Helper()
	items, err := store.ClaimDirectPeerEventOutbox(ctx, worker, time.Minute, 10)
	if err != nil || len(items) != 1 || items[0].ClaimToken == "" || items[0].AttemptCount != 1 {
		t.Fatalf("first claim=%#v err=%v", items, err)
	}
	return items[0]
}

func assertDirectPeerOutboxNotDuplicated(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	if retry, err := store.ClaimDirectPeerEventOutbox(ctx, "worker-b", time.Minute, 10); err != nil || len(retry) != 0 {
		t.Fatalf("claimed event was immediately duplicated: %#v err=%v", retry, err)
	}
}

func expireDirectPeerOutbox(t *testing.T, ctx context.Context, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.ExecContext(ctx, `UPDATE orchestration_direct_peer_event_outbox SET lease_expires_at = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
}

func claimDirectPeerOutboxTakeover(t *testing.T, ctx context.Context, store *Store) EventOutboxItem {
	t.Helper()
	items, err := store.ClaimDirectPeerEventOutbox(ctx, "worker-b", time.Minute, 10)
	if err != nil || len(items) != 1 || items[0].AttemptCount != 2 {
		t.Fatalf("expired claim takeover=%#v err=%v", items, err)
	}
	return items[0]
}

func assertDirectPeerOutboxFence(t *testing.T, ctx context.Context, store *Store, first, second EventOutboxItem) {
	t.Helper()
	if second.ClaimToken == first.ClaimToken {
		t.Fatal("expired claim reused the old token")
	}
	if err := store.MarkDirectPeerEventOutboxProjected(ctx, first.ID, "worker-a", first.ClaimToken); !errors.Is(err, ErrEventOutboxFence) {
		t.Fatalf("stale claim ACK error=%v", err)
	}
	if err := store.MarkDirectPeerEventOutboxProjected(ctx, second.ID, "worker-b", second.ClaimToken); err != nil {
		t.Fatal(err)
	}
}

// TestDirectPeerEventProjectorAcksOnlyAfterSink 验证投影失败保留租约任务，成功后才 ACK。
func TestDirectPeerEventProjectorAcksOnlyAfterSink(t *testing.T) {
	ctx := context.Background()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-1", true, true); err != nil {
		t.Fatal(err)
	}
	sinkErr := errors.New("sink unavailable")
	calls := 0
	projector, err := NewEventProjector(store, func(context.Context, Event) error {
		calls++
		return sinkErr
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := projector.Process(ctx, time.Minute, 1); !errors.Is(err, sinkErr) {
		t.Fatalf("failed projection error=%v", err)
	}
	assertDirectPeerOutboxStatus(t, db, "claimed")
	if calls != 1 {
		t.Fatalf("failed sink calls=%d want=1", calls)
	}
	if _, err := db.ExecContext(ctx, `UPDATE orchestration_direct_peer_event_outbox SET lease_expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	projector.sink = func(context.Context, Event) error {
		calls++
		return nil
	}
	if err := projector.Process(ctx, time.Minute, 1); err != nil {
		t.Fatal(err)
	}
	assertDirectPeerOutboxStatus(t, db, "projected")
	if calls != 2 {
		t.Fatalf("successful replay sink calls=%d want=2", calls)
	}
}

func assertDirectPeerOutboxStatus(t *testing.T, db *sql.DB, want string) {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT status FROM orchestration_direct_peer_event_outbox`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != want {
		t.Fatalf("direct peer outbox status=%q want=%q", status, want)
	}
}

func assertDirectPeerEventSequence(t *testing.T, events []Event, wantTypes []string) {
	t.Helper()
	if len(events) != len(wantTypes) {
		t.Fatalf("event count=%d want=%d", len(events), len(wantTypes))
	}
	for index, event := range events {
		if event.Cursor <= 0 || event.EventType != wantTypes[index] || event.EventID == "" || len(event.Payload) == 0 {
			t.Fatalf("event[%d]=%#v", index, event)
		}
		if index > 0 && event.Cursor <= events[index-1].Cursor {
			t.Fatalf("event cursors are not exclusive ordered: %#v", events)
		}
	}
}

func assertPendingDirectPeerEvents(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orchestration_direct_peer_event_outbox WHERE status = 'pending'`).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != want {
		t.Fatalf("pending event outbox=%d want=%d", pending, want)
	}
}

// TestDirectPeerWireDTOsRequireJSONTags 守住所有公开 store DTO 字段的稳定 wire 名。
func TestDirectPeerWireDTOsRequireJSONTags(t *testing.T) {
	for _, producer := range []reflect.Type{reflect.TypeFor[Grant](), reflect.TypeFor[Message](), reflect.TypeFor[Event](), reflect.TypeFor[EventOutboxItem](), reflect.TypeFor[PublishedEvent](), reflect.TypeFor[SendRequest]()} {
		for field := range producer.NumField() {
			name := strings.Split(producer.Field(field).Tag.Get("json"), ",")[0]
			if name == "" || name == "-" {
				t.Fatalf("%s.%s lacks public json tag", producer.Name(), producer.Field(field).Name)
			}
		}
	}
}

func assertUnauthorizedDirectPeerSend(t *testing.T, ctx context.Context, db *sql.DB, store *Store, request SendRequest) {
	t.Helper()
	if _, err := store.Send(ctx, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unauthorized send error=%v", err)
	}
	assertDirectPeerMessageCount(t, db, 0)
}

func assertAuthorizedDirectPeerSend(t *testing.T, ctx context.Context, db *sql.DB, store *Store, request SendRequest) Message {
	t.Helper()
	grant, err := store.GrantPeer(ctx, request.WorkspaceRoot, "sender", "receiver", "grant-1", true, true)
	if err != nil || grant.Revision != 1 {
		t.Fatalf("grant=%#v err=%v", grant, err)
	}
	assertDirectPeerGrantIdempotency(t, ctx, store, request, grant)
	return assertDirectPeerMessageIdempotency(t, ctx, db, store, request, grant)
}

func assertDirectPeerGrantIdempotency(t *testing.T, ctx context.Context, store *Store, request SendRequest, grant Grant) {
	t.Helper()
	retryGrant, err := store.GrantPeer(ctx, request.WorkspaceRoot, "sender", "receiver", "grant-1", true, true)
	if err != nil || retryGrant.GrantID != grant.GrantID || retryGrant.Revision != grant.Revision {
		t.Fatalf("idempotent grant=%#v err=%v", retryGrant, err)
	}
	if _, err := store.GrantPeer(ctx, request.WorkspaceRoot, "sender", "receiver", "grant-1", true, false); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different grant request error=%v", err)
	}
	if _, err := store.GrantPeer(ctx, request.WorkspaceRoot, "sender", "receiver", "grant-2", true, true); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("new key overwrote grant identity: %v", err)
	}
}

func assertDirectPeerMessageIdempotency(t *testing.T, ctx context.Context, db *sql.DB, store *Store, request SendRequest, grant Grant) Message {
	t.Helper()
	first, err := store.Send(ctx, request)
	if err != nil || first.Cursor < 1 || first.GrantRevision != grant.Revision || !first.Fresh {
		t.Fatalf("first message=%#v err=%v", first, err)
	}
	retry, err := store.Send(ctx, request)
	if err != nil || retry.MessageID != first.MessageID || retry.Cursor != first.Cursor || retry.Fresh {
		t.Fatalf("idempotent retry=%#v err=%v", retry, err)
	}
	conflict := request
	conflict.Body = "different"
	if _, err := store.Send(ctx, conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different request error=%v", err)
	}
	assertDirectPeerMessageCount(t, db, 1)
	return first
}

func assertDirectPeerReadAndRevoke(t *testing.T, ctx context.Context, store *Store, request SendRequest, first Message) {
	t.Helper()
	workspace := request.WorkspaceRoot
	messages, err := store.ReadReceiver(ctx, workspace, "receiver", 0, 10)
	if err != nil || len(messages) != 1 || messages[0].MessageID != first.MessageID {
		t.Fatalf("read messages=%#v err=%v", messages, err)
	}
	if next, err := store.ReadReceiver(ctx, workspace, "receiver", first.Cursor, 10); err != nil || len(next) != 0 {
		t.Fatalf("exclusive cursor messages=%#v err=%v", next, err)
	}
	assertDirectPeerRevoked(t, ctx, store, request)
}

func assertDirectPeerRevoked(t *testing.T, ctx context.Context, store *Store, request SendRequest) {
	t.Helper()
	revoked, err := store.RevokePeer(ctx, request.WorkspaceRoot, request.SenderAgentID, request.ReceiverAgentID, "revoke-1")
	if err != nil || revoked.Status != "revoked" || revoked.Revision != 2 {
		t.Fatalf("revoke=%#v err=%v", revoked, err)
	}
	if after, err := store.ReadReceiver(ctx, request.WorkspaceRoot, request.ReceiverAgentID, 0, 10); err != nil || len(after) != 0 {
		t.Fatalf("revoked read exposed messages=%#v err=%v", after, err)
	}
	if _, err := store.Send(ctx, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked sender retried old key with error=%v", err)
	}
}

func directPeerTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqliteruntime.Open(t.Context(), sqliteruntime.OpenOptions{Path: filepath.Join(t.TempDir(), "orch.sqlite")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := sqliteruntime.RunEmbeddedMigrations(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertDirectPeerMessageCount(t *testing.T, db *sql.DB, expected int) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM orchestration_direct_peer_messages").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("message count=%d want=%d", count, expected)
	}
}
