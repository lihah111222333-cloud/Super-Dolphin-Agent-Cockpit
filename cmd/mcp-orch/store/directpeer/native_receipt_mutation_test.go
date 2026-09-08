package directpeer

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestNativeReceiptPersistedFieldMutations 动态遍历确认字段，经真实数据库拒绝缺失、空白及JSON类型污染。
func TestNativeReceiptPersistedFieldMutations(t *testing.T) {
	store, db, message, capability, receipt := nativeReceiptFixture(t)
	submitted, err := store.RecordNativeReceipt(t.Context(), capability, message.MessageID, receipt)
	if err != nil {
		t.Fatal(err)
	}
	var original string
	query := `SELECT payload_json FROM orchestration_direct_peer_events WHERE message_id = ? AND event_type = 'message.submitted'`
	if err := db.QueryRow(query, message.MessageID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	typ := reflect.TypeOf(receipt)
	for i := 0; i < typ.NumField(); i++ {
		field := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		for name, replacement := range invalidPersistedReceiptValues() {
			t.Run(field+"/"+name, func(t *testing.T) {
				mutated := mutatePersistedReceiptField(t, original, field, replacement)
				update := `UPDATE orchestration_direct_peer_events SET payload_json = ? WHERE message_id = ? AND event_type = 'message.submitted'`
				if _, err := db.Exec(update, mutated, message.MessageID); err != nil {
					t.Fatal(err)
				}
				reopened, err := NewStore(db)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := reopened.ReadNativeReceipt(t.Context(), submitted); !errors.Is(err, ErrDeliveryOutcomeUnknown) {
					t.Fatalf("invalid persisted receipt accepted: %v", err)
				}
			})
		}
	}
	assertPendingDirectPeerEvents(t, db, 3)
}

// invalidPersistedReceiptValues 为全部字段提供必然非法的单点变异，不把合法边界误报为损坏。
func invalidPersistedReceiptValues() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"missing": nil, "null": json.RawMessage(`null`), "empty": json.RawMessage(`""`),
		"space": json.RawMessage(`" "`), "tab": json.RawMessage(`"\t"`),
		"object": json.RawMessage(`{}`), "array": json.RawMessage(`[]`),
		"boolean": json.RawMessage(`true`), "zero": json.RawMessage(`0`),
		"negative": json.RawMessage(`-1`), "fraction": json.RawMessage(`1.5`),
		"overflow": json.RawMessage(`18446744073709551616`),
	}
}

// mutatePersistedReceiptField 每次从未污染原文生成单字段变异，保证各子测试互不依赖。
func mutatePersistedReceiptField(t *testing.T, original, field string, replacement json.RawMessage) string {
	t.Helper()
	var envelope, fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(original), &envelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(envelope["provider_receipt"], &fields); err != nil {
		t.Fatal(err)
	}
	if replacement == nil {
		delete(fields, field)
	} else {
		fields[field] = replacement
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	envelope["provider_receipt"] = encoded
	result, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	return string(result)
}
