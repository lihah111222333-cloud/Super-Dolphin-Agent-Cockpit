package terminaloutcome

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestTerminalOutcomeFieldGuardProducerMapperAndSchemaStayClosed(t *testing.T) {
	identityFields := reflectedJSONFields(t, reflect.TypeOf(contract.CanonicalTerminalIdentity{}))
	commitFields := reflectedJSONFields(t, reflect.TypeOf(contract.TerminalOutcomeCommit{}))
	assertExactFieldSet(t, "canonical identity registry", identityFields, terminalIdentityFieldRegistry())
	assertExactFieldSet(t, "terminal commit payload registry", commitFields, terminalCommitPayloadFieldRegistry())

	source, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("read terminal outcome store source: %v", err)
	}
	selectors := selectorNames(t, source)
	for jsonField, goField := range terminalIdentityFieldRegistry() {
		if !selectors[goField] {
			t.Fatalf("terminal identity mapper missing field %s (%s)", jsonField, goField)
		}
	}

	_, db := newTestStore(t)
	assertExactFieldSet(t, "terminal_outcome_heads columns",
		sqliteColumnSet(t, db, "terminal_outcome_heads"), terminalOutcomeHeadColumnRegistry())
	assertExactFieldSet(t, "public_terminal_outcomes columns",
		sqliteColumnSet(t, db, "public_terminal_outcomes"), publicTerminalOutcomeColumnRegistry())
	assertExactFieldSet(t, "terminal_outcome_outbox columns",
		sqliteColumnSet(t, db, "terminal_outcome_outbox"), terminalOutcomeOutboxColumnRegistry())
}

func reflectedJSONFields(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool, typ.NumField())
	for index := range typ.NumField() {
		field := typ.Field(index)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			t.Fatalf("%s.%s has no enumerable json field", typ.Name(), field.Name)
		}
		if fields[tag] {
			t.Fatalf("%s has duplicate json field %q", typ.Name(), tag)
		}
		fields[tag] = true
	}
	return fields
}

func assertExactFieldSet(t *testing.T, name string, producer map[string]bool, registry map[string]string) {
	t.Helper()
	var missing, stale []string
	for field := range producer {
		if strings.TrimSpace(registry[field]) == "" {
			missing = append(missing, field)
		}
	}
	for field, evidence := range registry {
		if !producer[field] || strings.TrimSpace(evidence) == "" {
			stale = append(stale, field)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) != 0 || len(stale) != 0 {
		t.Fatalf("%s drift: missing=%v stale_or_unreasoned=%v", name, missing, stale)
	}
}

func selectorNames(t *testing.T, source []byte) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "store.go", source, 0)
	if err != nil {
		t.Fatalf("parse terminal outcome store: %v", err)
	}
	selectors := make(map[string]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok {
			selectors[selector.Sel.Name] = true
		}
		return true
	})
	return selectors
}

func sqliteColumnSet(t *testing.T, db interface {
	Query(string, ...any) (*sql.Rows, error)
}, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("inspect %s columns: %v", table, err)
	}
	defer rows.Close()
	fields := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan %s column: %v", table, err)
		}
		fields[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s columns: %v", table, err)
	}
	return fields
}

func terminalIdentityFieldRegistry() map[string]string {
	return map[string]string{
		"capability":          "Capability",
		"agentId":             "AgentID",
		"publicThreadId":      "PublicThreadID",
		"providerTurnId":      "ProviderTurnID",
		"sessionId":           "SessionID",
		"generation":          "Generation",
		"eventId":             "EventID",
		"terminalIdentity":    "TerminalIdentity",
		"expectedActiveState": "ExpectedActiveState",
	}
}

func terminalCommitPayloadFieldRegistry() map[string]string {
	return map[string]string{
		"schemaVersion":  "strict v2 decoder",
		"projectionKind": "strict projector route",
		"identity":       "CanonicalTerminalIdentity validator",
		"publicOutcome":  "PublicOutcome validator",
		"publicReport":   "public-only report",
		"occurredAt":     "canonical timestamp",
	}
}

func terminalOutcomeHeadColumnRegistry() map[string]string {
	return map[string]string{
		"agent_id": "AgentID", "capability": "Capability", "public_thread_id": "PublicThreadID",
		"provider_turn_id": "ProviderTurnID", "session_id": "SessionID",
		"generation": "Generation", "event_id": "EventID",
		"terminal_identity": "TerminalIdentity", "expected_active_state": "ExpectedActiveState",
		"state": "CAS state", "updated_at": "OccurredAt",
	}
}

func publicTerminalOutcomeColumnRegistry() map[string]string {
	return map[string]string{
		"agent_id": "AgentID", "schema_version": "SchemaVersion",
		"projection_kind": "ProjectionKind", "public_thread_id": "PublicThreadID",
		"provider_turn_id": "ProviderTurnID", "session_id": "SessionID",
		"generation": "Generation", "event_id": "EventID",
		"terminal_identity": "TerminalIdentity", "public_outcome_json": "PublicOutcome",
		"public_report": "PublicReport", "occurred_at": "OccurredAt",
	}
}

func terminalOutcomeOutboxColumnRegistry() map[string]string {
	return map[string]string{
		"id": "outbox identity", "event_id": "EventID", "payload_json": "TerminalOutcomeCommit",
		"status": "claim lifecycle", "claimed_by": "worker fence", "claimed_at": "lease",
		"projected_at": "projection ack", "created_at": "OccurredAt",
	}
}
