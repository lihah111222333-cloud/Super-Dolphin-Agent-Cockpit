package terminaloutcome

import (
	"context"
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestTerminalOutcomeInsertLoadOutboxRoundTripPreservesSeparatedFields(t *testing.T) {
	store, _ := newTestStore(t)
	commit := terminalCommitFixture("terminal:field-roundtrip")
	commit.PrivateDAG = &contract.OwnerScopedDAGPayload{
		OwnerAgentID: commit.Identity.AgentID, PublicThreadID: commit.Identity.PublicThreadID,
		ProviderTurnID: commit.Identity.ProviderTurnID, Result: "owner artifact",
	}
	commit = activateCommitFixture(t, store, commit)
	if _, err := store.CommitTerminalOutcome(context.Background(), commit); err != nil {
		t.Fatalf("CommitTerminalOutcome() error = %v", err)
	}
	public, err := store.GetPublicTerminalOutcome(context.Background(), commit.Identity.AgentID)
	if err != nil {
		t.Fatalf("GetPublicTerminalOutcome() error = %v", err)
	}
	if public.PrivateDAG != nil || !reflect.DeepEqual(public, publicCommit(commit)) {
		t.Fatalf("public roundtrip = %#v, want private-free canonical commit", public)
	}
	items, err := store.ClaimTerminalOutcomeOutbox(context.Background(), "field-roundtrip", time.Minute, 1)
	if err != nil || len(items) != 1 || !reflect.DeepEqual(items[0].PrivateDAG, commit.PrivateDAG) ||
		!reflect.DeepEqual(items[0].Outcome, publicCommit(commit)) {
		t.Fatalf("outbox roundtrip = %#v, %v", items, err)
	}
}

func TestTerminalOutcomeFieldGuardProducerMapperAndSchemaStayClosed(t *testing.T) {
	identityFields := reflectedJSONFields(t, reflect.TypeOf(contract.CanonicalTerminalIdentity{}))
	commitFields := reflectedJSONFields(t, reflect.TypeOf(contract.TerminalOutcomeCommit{}))
	recursiveCommitFields := recursiveJSONFields(t, reflect.TypeOf(contract.TerminalOutcomeCommit{}), "commit")
	publicFields := recursiveJSONFields(t, reflect.TypeOf(contract.PublicOutcome{}), "publicOutcome")
	privateFields := recursiveJSONFields(t, reflect.TypeOf(contract.OwnerScopedDAGPayload{}), "privateDAG")
	assertExactFieldSet(t, "canonical identity registry", identityFields, terminalIdentityFieldRegistry())
	assertExactFieldSet(t, "terminal commit payload registry", commitFields, terminalCommitPayloadFieldRegistry())
	assertExactFieldSet(t, "recursive terminal commit registry", recursiveCommitFields, terminalCommitRecursiveRegistry())
	assertExactFieldSet(t, "recursive public outcome registry", publicFields, publicOutcomeRecursiveRegistry())
	assertExactFieldSet(t, "owner-scoped private DAG registry", privateFields, privateDAGRecursiveRegistry())

	source, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatalf("read terminal outcome store source: %v", err)
	}
	for mapper, required := range terminalMapperFieldRegistry() {
		selectors := selectorNamesInFunctions(t, source, mapper)
		for _, goField := range required {
			if !selectors[goField] {
				t.Fatalf("terminal mapper %s missing field %s", mapper, goField)
			}
		}
	}

	_, db := newTestStore(t)
	for table, registry := range terminalTableColumnRegistries() {
		assertExactFieldSet(t, table+" columns", sqliteColumnSet(t, db, table), registry)
	}
}

func terminalCommitRecursiveRegistry() map[string]string {
	registry := map[string]string{
		"commit.schemaVersion": "SchemaVersion", "commit.projectionKind": "ProjectionKind",
		"commit.identity": "CanonicalTerminalIdentity", "commit.publicOutcome": "PublicOutcome",
		"commit.publicReport": "PublicReport", "commit.occurredAt": "OccurredAt",
	}
	for field, evidence := range terminalIdentityFieldRegistry() {
		registry["commit.identity."+field] = evidence
	}
	for field, evidence := range publicOutcomeRecursiveRegistry() {
		registry["commit."+field] = evidence
	}
	return registry
}

func reflectedJSONFields(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()
	fields := make(map[string]bool, typ.NumField())
	for index := range typ.NumField() {
		field := typ.Field(index)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "" {
			t.Fatalf("%s.%s has no enumerable json field", typ.Name(), field.Name)
		}
		if tag == "-" {
			continue
		}
		if fields[tag] {
			t.Fatalf("%s has duplicate json field %q", typ.Name(), tag)
		}
		fields[tag] = true
	}
	return fields
}

func recursiveJSONFields(t *testing.T, typ reflect.Type, prefix string) map[string]bool {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	fields := map[string]bool{}
	var walk func(reflect.Type, string)
	walk = func(current reflect.Type, path string) {
		for current.Kind() == reflect.Pointer {
			current = current.Elem()
		}
		if current.Kind() != reflect.Struct || current == reflect.TypeOf(time.Time{}) {
			return
		}
		for index := range current.NumField() {
			field := current.Field(index)
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag == "" || tag == "-" {
				continue
			}
			child := path + "." + tag
			if fields[child] {
				t.Fatalf("duplicate recursive JSON path %s", child)
			}
			fields[child] = true
			walk(field.Type, child)
		}
	}
	walk(typ, prefix)
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

func selectorNamesInFunctions(t *testing.T, source []byte, names string) map[string]bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "store.go", source, 0)
	if err != nil {
		t.Fatalf("parse terminal outcome store: %v", err)
	}
	allowed := map[string]bool{}
	for _, name := range strings.Split(names, ",") {
		allowed[strings.TrimSpace(name)] = true
	}
	selectors := map[string]bool{}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || !allowed[fn.Name.Name] {
			continue
		}
		ast.Inspect(fn, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				selectors[selector.Sel.Name] = true
			}
			return true
		})
	}
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
		"headVersion":         "HeadVersion",
	}
}

func publicOutcomeRecursiveRegistry() map[string]string {
	return map[string]string{
		"publicOutcome.kind": "Kind", "publicOutcome.code": "Code", "publicOutcome.summary": "Summary",
		"publicOutcome.publicError": "PublicError", "publicOutcome.publicError.code": "Code",
		"publicOutcome.publicError.title": "Title", "publicOutcome.publicError.message": "Message",
		"publicOutcome.publicError.diagnosticId":    "DiagnosticID",
		"publicOutcome.publicError.retryable":       "Retryable",
		"publicOutcome.publicError.recoveryActions": "RecoveryActions",
		"publicOutcome.completedAt":                 "CompletedAt",
	}
}

func privateDAGRecursiveRegistry() map[string]string {
	return map[string]string{
		"privateDAG.ownerAgentId": "OwnerAgentID", "privateDAG.publicThreadId": "PublicThreadID",
		"privateDAG.providerTurnId": "ProviderTurnID", "privateDAG.result": "Result",
	}
}

func terminalMapperFieldRegistry() map[string][]string {
	identity := []string{"Capability", "AgentID", "PublicThreadID", "ProviderTurnID", "SessionID", "Generation", "ExpectedActiveState"}
	return map[string][]string{
		"insertCurrentHeadTx,activateTerminalHeadTx": append(append([]string{}, identity...), "Version"),
		"sealCurrentHeadTx":                          append(append([]string{}, identity...), "EventID", "TerminalIdentity", "HeadVersion"),
		"insertPublicHistoryTx":                      {"TerminalIdentity", "EventID", "AgentID", "HeadVersion", "PublicOutcome", "PublicReport"},
		"loadHistoryByIdentityTx":                    {"SchemaVersion", "ProjectionKind", "Identity", "PublicOutcome", "PublicReport", "OccurredAt"},
		"insertOutboxTx,decodeOutboxCandidate":       {"TerminalIdentity", "EventID", "PrivateDAG", "Outcome"},
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

func terminalTableColumnRegistries() map[string]map[string]string {
	return map[string]map[string]string{
		"terminal_outcome_current_heads": {
			"agent_id": "AgentID", "capability": "Capability", "public_thread_id": "PublicThreadID",
			"provider_turn_id": "ProviderTurnID", "session_id": "SessionID", "generation": "Generation",
			"expected_active_state": "ExpectedActiveState", "version": "HeadVersion", "state": "CAS state",
			"terminal_event_id": "EventID", "terminal_identity": "TerminalIdentity",
			"activated_at": "ActivatedAt", "updated_at": "OccurredAt",
		},
		"public_terminal_outcome_history": {
			"terminal_identity": "TerminalIdentity", "event_id": "EventID", "agent_id": "AgentID",
			"head_version": "HeadVersion", "schema_version": "SchemaVersion", "projection_kind": "ProjectionKind",
			"public_thread_id": "PublicThreadID", "provider_turn_id": "ProviderTurnID", "session_id": "SessionID",
			"generation": "Generation", "expected_active_state": "ExpectedActiveState",
			"public_outcome_json": "PublicOutcome", "public_report": "PublicReport", "occurred_at": "OccurredAt",
		},
		"terminal_outcome_private_dag_payloads": {
			"id": "private identity", "terminal_identity": "TerminalIdentity", "owner_agent_id": "OwnerAgentID",
			"public_thread_id": "PublicThreadID", "provider_turn_id": "ProviderTurnID",
			"payload_json": "OwnerScopedDAGPayload", "created_at": "OccurredAt",
		},
		"terminal_outcome_outbox_v2": {
			"id": "outbox identity", "terminal_identity": "TerminalIdentity", "event_id": "EventID",
			"public_payload_json": "TerminalOutcomeCommit", "private_dag_payload_id": "PrivateDAG",
			"status": "claim lifecycle", "claimed_by": "worker fence", "claim_token": "claim epoch",
			"lease_expires_at": "absolute lease", "attempt_count": "delivery attempts",
			"last_error": "fixed poison detail", "projected_at": "projection ack", "created_at": "OccurredAt",
		},
	}
}
