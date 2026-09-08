package directpeer

import (
	"context"
	"errors"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"sync"
	"testing"
)

// TestRegrantRevisionIsolation 新授权推进版本，旧消息与历史幂等键不能重获执行权限。
func TestRegrantRevisionIsolation(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(directPeerTestDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	grant, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-old", true, true)
	if err != nil {
		t.Fatal(err)
	}
	request := SendRequest{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "old-message", Body: "old"}
	old, err := store.Send(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := store.RevokePeer(ctx, workspace, "sender", "receiver", "revoke-old")
	if err != nil {
		t.Fatal(err)
	}
	assertRevokedGrantReplay(t, store, workspace, revoked)
	renewed, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-new", true, true)
	if err != nil {
		t.Fatalf("explicit regrant rejected: %v", err)
	}
	if renewed.GrantID == grant.GrantID || renewed.Revision != revoked.Revision+1 || renewed.Status != "active" {
		t.Fatalf("regrant=%+v", renewed)
	}
	assertHistoricalGrantKeys(t, store, workspace, renewed)
	assertRegrantMessageIsolation(t, ctx, store, request, old, renewed)
	assertOldRegrantMessageRetained(t, store, request, old)
}

// assertRegrantMessageIsolation 旧消息不可重放或读取，只有新版本消息进入当前读取面。
func assertRegrantMessageIsolation(t *testing.T, ctx context.Context, store *Store, request SendRequest, old Message, renewed Grant) {
	t.Helper()
	workspace := request.WorkspaceRoot
	if _, err := store.Send(ctx, request); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old message replay=%v, want unauthorized", err)
	}
	messages, err := store.ReadReceiver(ctx, workspace, "receiver", 0, 50)
	if err != nil || len(messages) != 0 {
		t.Fatalf("old revision visible: %v %v", messages, err)
	}
	request.IdempotencyKey, request.Body = "new-message", "new"
	fresh, err := store.Send(ctx, request)
	if err != nil || !fresh.Fresh || fresh.GrantRevision != renewed.Revision || fresh.MessageID == old.MessageID {
		t.Fatalf("new message=%+v err=%v", fresh, err)
	}
	assertOnlyCurrentRevisionMessage(t, store, workspace, fresh.MessageID)
}

// assertOnlyCurrentRevisionMessage 读取面只暴露当前授权版本消息，不重新公开旧授权消息。
func assertOnlyCurrentRevisionMessage(t *testing.T, store *Store, workspace, messageID string) {
	t.Helper()
	messages, err := store.ReadReceiver(context.Background(), workspace, "receiver", 0, 50)
	if err != nil || len(messages) != 1 || messages[0].MessageID != messageID {
		t.Fatalf("current messages=%+v err=%v", messages, err)
	}
}

// assertHistoricalGrantKeys 重放旧grant/revoke只返回当前状态，不能再修改版本或权限。
func assertHistoricalGrantKeys(t *testing.T, store *Store, workspace string, current Grant) {
	t.Helper()
	ctx := context.Background()
	replay, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-old", true, true)
	if err != nil || replay != current {
		t.Fatalf("old grant replay=%+v err=%v", replay, err)
	}
	replay, err = store.RevokePeer(ctx, workspace, "sender", "receiver", "revoke-old")
	if err != nil || replay != current {
		t.Fatalf("old revoke replay=%+v err=%v", replay, err)
	}
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-old", true, false); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("old key mutation=%v", err)
	}
}

// TestRegrantConcurrentIntentFence 并发新意图只允许一个获胜；并发旧grant/revoke重放不能修改新权限。
func TestRegrantConcurrentIntentFence(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(directPeerTestDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(ctx, workspace, "sender", "receiver", "grant-old", true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokePeer(ctx, workspace, "sender", "receiver", "revoke-old"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, key := range []string{"new-a", "new-b"} {
		workers.Go(func() {
			<-start
			_, err := store.GrantPeer(ctx, workspace, "sender", "receiver", key, true, true)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent regrant successes=%d want=1", successes)
	}
	current, err := storedb.New(store.db).GetDirectPeerGrant(ctx, storedb.GetDirectPeerGrantParams{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver"})
	if err != nil {
		t.Fatal(err)
	}
	assertConcurrentHistoricalReplays(t, store, workspace, grantFromRow(current))
}

// assertConcurrentHistoricalReplays 通过独立store实例重放历史key，证明持久幂等不依赖进程锁。
func assertConcurrentHistoricalReplays(t *testing.T, store *Store, workspace string, current Grant) {
	t.Helper()
	peer, err := NewStore(store.db)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var workers sync.WaitGroup
	workers.Go(func() {
		g, err := peer.GrantPeer(context.Background(), workspace, "sender", "receiver", "grant-old", true, true)
		if err == nil && g != current {
			err = errors.New("old grant changed current permission")
		}
		results <- err
	})
	workers.Go(func() {
		g, err := store.RevokePeer(context.Background(), workspace, "sender", "receiver", "revoke-old")
		if err == nil && g != current {
			err = errors.New("old revoke changed current permission")
		}
		results <- err
	})
	workers.Wait()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertHistoricalGrantKeys(t, store, workspace, current)
}

// assertRevokedGrantReplay 旧授权key在撤销后只能读撤销结果，不能借重试重新授权。
func assertRevokedGrantReplay(t *testing.T, store *Store, workspace string, revoked Grant) {
	t.Helper()
	replay, err := store.GrantPeer(context.Background(), workspace, "sender", "receiver", "grant-old", true, true)
	if err != nil || replay != revoked {
		t.Fatalf("revoked grant replay=%+v err=%v", replay, err)
	}
}

// assertOldRegrantMessageRetained 旧回执保留审计，撤权和重授不会改写或创建第二条消息。
func assertOldRegrantMessageRetained(t *testing.T, store *Store, request SendRequest, old Message) {
	t.Helper()
	row, err := storedb.New(store.db).GetDirectPeerMessageByKey(context.Background(), storedb.GetDirectPeerMessageByKeyParams{WorkspaceRoot: request.WorkspaceRoot, SenderAgentID: request.SenderAgentID, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		t.Fatal(err)
	}
	old.Fresh = false
	if actual := messageFromRow(row); actual != old {
		t.Fatalf("historical message changed: actual=%+v want=%+v", actual, old)
	}
}
