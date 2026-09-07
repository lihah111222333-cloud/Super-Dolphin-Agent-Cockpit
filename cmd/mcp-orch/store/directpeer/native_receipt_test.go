package directpeer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// nativeReceiptFixture 创建有真实 grant/accepted 事务记录的测试域。
func nativeReceiptFixture(t *testing.T) (*Store, *sql.DB, Message, OwnerCapability, contract.ProviderSubmissionReceipt) {
	t.Helper()
	db := directPeerTestDatabase(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if _, err := store.GrantPeer(t.Context(), workspace, "sender", "receiver", "grant", true, true); err != nil {
		t.Fatal(err)
	}
	message, err := store.Send(t.Context(), SendRequest{WorkspaceRoot: workspace, SenderAgentID: "sender", ReceiverAgentID: "receiver", IdempotencyKey: "message", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := store.OwnerCapability()
	if err != nil {
		t.Fatal(err)
	}
	receipt := contract.ProviderSubmissionReceipt{Provider: "codex", AgentID: "receiver", ThreadID: "thread", TurnID: "turn", ReceiptID: "receipt", LaunchSeq: 7}
	return store, db, message, capability, receipt
}

// TestNativeReceiptRoundTripFields 动态枚举生产 DTO 全字段，验证数据库载荷与新 Store 读取都无遗漏。
func TestNativeReceiptRoundTripFields(t *testing.T) {
	store, db, message, capability, receipt := nativeReceiptFixture(t)
	submitted, err := store.RecordNativeReceipt(t.Context(), capability, message.MessageID, receipt)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.ReadNativeReceipt(t.Context(), submitted)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt, got) {
		t.Fatalf("receipt roundtrip got=%+v want=%+v", got, receipt)
	}
	var raw string
	if err := db.QueryRow(`SELECT payload_json FROM orchestration_direct_peer_events WHERE message_id = ? AND event_type = 'message.submitted'`, message.MessageID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	assertNativeReceiptDynamicFields(t, raw, receipt)
	assertPendingDirectPeerEvents(t, db, 3)
}

// assertNativeReceiptDynamicFields 由反射导出字段集合并逐项删除验证，新增字段无法被手工字段清单遗漏。
func assertNativeReceiptDynamicFields(t *testing.T, raw string, receipt contract.ProviderSubmissionReceipt) {
	t.Helper()
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload["provider_receipt"], &fields); err != nil {
		t.Fatal(err)
	}
	typ, value := reflect.TypeOf(receipt), reflect.ValueOf(receipt)
	if len(fields) != typ.NumField() {
		t.Fatalf("receipt fields=%d producer=%d", len(fields), typ.NumField())
	}
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		expected, err := json.Marshal(value.Field(i).Interface())
		if err != nil {
			t.Fatal(err)
		}
		if string(fields[name]) != string(expected) {
			t.Fatalf("missing or changed producer field %s", name)
		}
		// 当前必填生产字段一旦被序列化漏写，Validate 必须拒绝。
		removed := fields[name]
		delete(fields, name)
		encoded, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		var incomplete contract.ProviderSubmissionReceipt
		if err := json.Unmarshal(encoded, &incomplete); err != nil {
			t.Fatal(err)
		}
		if err := incomplete.Validate(); err == nil {
			t.Fatalf("missing field %s accepted", name)
		}
		fields[name] = removed
	}
}

// TestNativeReceiptRejectsInvalidIdentity 锁定 capability、receiver、空字段与消息键冒充确认的拒绝路径。
func TestNativeReceiptRejectsInvalidIdentity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*contract.ProviderSubmissionReceipt, *OwnerCapability, Message)
	}{
		{"wrong-agent", func(r *contract.ProviderSubmissionReceipt, _ *OwnerCapability, _ Message) { r.AgentID = "wrong" }},
		{"empty-receipt", func(r *contract.ProviderSubmissionReceipt, _ *OwnerCapability, _ Message) {
			*r = contract.ProviderSubmissionReceipt{}
		}},
		{"no-owner", func(_ *contract.ProviderSubmissionReceipt, c *OwnerCapability, _ Message) { *c = OwnerCapability{} }},
		{"message-as-turn", func(r *contract.ProviderSubmissionReceipt, _ *OwnerCapability, m Message) { r.TurnID = m.MessageID }},
		{"message-as-receipt", func(r *contract.ProviderSubmissionReceipt, _ *OwnerCapability, m Message) { r.ReceiptID = m.MessageID }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, db, message, capability, receipt := nativeReceiptFixture(t)
			tc.mutate(&receipt, &capability, message)
			if _, err := store.RecordNativeReceipt(t.Context(), capability, message.MessageID, receipt); err == nil {
				t.Fatal("invalid native receipt accepted")
			}
			assertNativeReceiptUnchanged(t, db, message)
		})
	}
}

// assertNativeReceiptUnchanged 确认事务失败没有残留状态、submitted 事件或新增 outbox。
func assertNativeReceiptUnchanged(t *testing.T, db *sql.DB, message Message) {
	t.Helper()
	var status, submission string
	if err := db.QueryRow(`SELECT status, submission_id FROM orchestration_direct_peer_messages WHERE message_id = ?`, message.MessageID).Scan(&status, &submission); err != nil {
		t.Fatal(err)
	}
	if status != "accepted" || submission != "" {
		t.Fatalf("partial message update %s/%s", status, submission)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM orchestration_direct_peer_events WHERE message_id = ? AND event_type = 'message.submitted'`, message.MessageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("partial submitted event")
	}
	assertPendingDirectPeerEvents(t, db, 2)
}

// TestNativeReceiptLegacySubmittedIsUnknown 不将旧 submitted 字符串当作原生确认。
func TestNativeReceiptLegacySubmittedIsUnknown(t *testing.T) {
	store, _, message, capability, _ := nativeReceiptFixture(t)
	submitted, err := store.MarkSubmittedByOwner(t.Context(), capability, message.MessageID, "legacy-turn")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadNativeReceipt(t.Context(), submitted); !errors.Is(err, ErrDeliveryOutcomeUnknown) {
		t.Fatalf("legacy read error=%v", err)
	}
}

// TestNativeReceiptTransactionRollback 事件或 outbox 任一写入故障都回滚状态转换。
func TestNativeReceiptTransactionRollback(t *testing.T) {
	for _, table := range []string{"orchestration_direct_peer_events", "orchestration_direct_peer_event_outbox"} {
		t.Run(table, func(t *testing.T) {
			store, db, message, capability, receipt := nativeReceiptFixture(t)
			if _, err := db.Exec(`CREATE TRIGGER fail_native_receipt BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'injected native receipt failure'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := store.RecordNativeReceipt(t.Context(), capability, message.MessageID, receipt); err == nil {
				t.Fatal("transaction failure ignored")
			}
			assertNativeReceiptUnchanged(t, db, message)
		})
	}
}

// TestNativeReceiptReadRejectsCorruptPayload 验证持久确认损坏与调用方错误消息快照不会被 submitted 状态掩盖。
func TestNativeReceiptReadRejectsCorruptPayload(t *testing.T) {
	cases := []struct{ name, path, value string }{
		{"receipt-agent", "$.provider_receipt.agent_id", "foreign"},
		{"receipt-turn", "$.provider_receipt.turn_id", "foreign"},
		{"payload-message", "$.message_id", "foreign"},
		{"payload-submission", "$.submission_id", "foreign"},
		{"empty-thread", "$.provider_receipt.thread_id", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, db, message, capability, receipt := nativeReceiptFixture(t)
			submitted, err := store.RecordNativeReceipt(t.Context(), capability, message.MessageID, receipt)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE orchestration_direct_peer_events SET payload_json = json_set(payload_json, ?, ?) WHERE message_id = ? AND event_type = 'message.submitted'`, tc.path, tc.value, message.MessageID); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReadNativeReceipt(t.Context(), submitted); !errors.Is(err, ErrDeliveryOutcomeUnknown) {
				t.Fatalf("corrupt receipt error=%v", err)
			}
		})
	}
}

// TestNativeReceiptStoreContract 聚合原生确认持久化的确定性契约回归。
func TestNativeReceiptStoreContract(t *testing.T) {
	t.Run("roundtrip-fields", TestNativeReceiptRoundTripFields)
	t.Run("invalid-identity", TestNativeReceiptRejectsInvalidIdentity)
	t.Run("legacy-unknown", TestNativeReceiptLegacySubmittedIsUnknown)
	t.Run("transaction-rollback", TestNativeReceiptTransactionRollback)
	t.Run("corrupt-payload", TestNativeReceiptReadRejectsCorruptPayload)
}
