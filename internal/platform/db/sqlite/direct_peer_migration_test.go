package sqlite

import (
	"context"
	"testing"
)

// TestDirectPeerMigrationReaches133AndConstrainsIdentity 验证完整迁移链及direct-peer事件约束。
func TestDirectPeerMigrationReaches133AndConstrainsIdentity(t *testing.T) {
	db := openMigrationTestDB(t)
	if err := RunMigrations(context.Background(), db, "migrations"); err != nil {
		t.Fatal(err)
	}
	assertMaxMigrationVersion(t, db, 136)
	assertMigrationMarkerCount(t, db, "124_orchestration_direct_peer.sql", 1)
	assertMigrationMarkerCount(t, db, "125_orchestration_direct_peer_events.sql", 1)
	mustExec(t, db, `INSERT INTO orchestration_direct_peer_grants
		(grant_id,workspace_root,sender_agent_id,receiver_agent_id,idempotency_key,request_digest,can_send,can_read,status,revision,created_at,updated_at)
		VALUES ('g','/workspace','sender','receiver','grant-key',printf('%064d',0),1,1,'active',1,1,1)`)
	if _, err := db.Exec(`INSERT INTO orchestration_direct_peer_grants
		(grant_id,workspace_root,sender_agent_id,receiver_agent_id,idempotency_key,request_digest,can_send,can_read,status,revision,created_at,updated_at)
		VALUES ('g2','/workspace','sender','receiver','grant-key-2',printf('%064d',0),1,1,'active',1,1,1)`); err == nil {
		t.Fatal("duplicate directed grant identity accepted")
	}
	if _, err := db.Exec(`INSERT INTO orchestration_direct_peer_messages
		(message_id,idempotency_key,request_digest,workspace_root,sender_agent_id,receiver_agent_id,grant_revision,body,status,created_at)
		VALUES ('m','k','short','/workspace','sender','receiver',1,'hello','accepted',1)`); err == nil {
		t.Fatal("invalid request digest accepted")
	}
	if _, err := db.Exec(`INSERT INTO orchestration_direct_peer_events
		(event_id,event_type,workspace_root,grant_id,message_id,sender_agent_id,receiver_agent_id,grant_revision,payload_json,created_at)
		VALUES ('event','message.accepted','/workspace','','m','sender','receiver',1,'{}',1)`); err == nil {
		t.Fatal("direct peer message event without matching message was accepted")
	}
}
