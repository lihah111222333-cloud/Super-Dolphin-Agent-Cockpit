package orchestrationcommand

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func testLifecycleAttemptIndexMatchesStateMachine(t *testing.T) {
	fixture := newLifecycleFixture(t, "lifecycle-index-guard")
	indexSQL := loadLifecycleAttemptIndexSQL(t, fixture.db)
	got := parseLifecycleAttemptIndexTriggers(t, indexSQL)
	want := make(map[Trigger]bool)
	for _, rule := range phaseTransitions() {
		if startsAttempt(rule.Trigger) {
			want[rule.Trigger] = true
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partial unique attempt trigger set = %v, want state-machine set %v", got, want)
	}
	assertLifecycleAttemptIndexIsUniquePartial(t, fixture.db)
}

func loadLifecycleAttemptIndexSQL(t *testing.T, db *sql.DB) string {
	t.Helper()
	const query = `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'idx_orchestration_command_journal_new_attempt'`
	var indexSQL string
	if err := db.QueryRowContext(t.Context(), query).Scan(&indexSQL); err != nil {
		t.Fatalf("load lifecycle attempt index SQL error = %v", err)
	}
	return indexSQL
}

func parseLifecycleAttemptIndexTriggers(t *testing.T, indexSQL string) map[Trigger]bool {
	t.Helper()
	body := lifecycleAttemptIndexTriggerBody(t, indexSQL)
	parts := strings.Split(body, ",")
	triggers := make(map[Trigger]bool, len(parts))
	for _, part := range parts {
		trigger := parseLifecycleAttemptTriggerLiteral(t, strings.TrimSpace(part), indexSQL)
		if triggers[trigger] {
			t.Fatalf("lifecycle attempt index trigger literal is duplicated: %q", trigger)
		}
		triggers[trigger] = true
	}
	return triggers
}

func lifecycleAttemptIndexTriggerBody(t *testing.T, indexSQL string) string {
	t.Helper()
	compact := strings.Join(strings.Fields(indexSQL), " ")
	lower := strings.ToLower(compact)
	const prefix = "where trigger in ("
	start := strings.Index(lower, prefix)
	if start < 0 {
		t.Fatalf("lifecycle attempt index SQL has no exact WHERE trigger IN clause: %s", indexSQL)
	}
	bodyStart := start + len(prefix)
	rest := compact[bodyStart:]
	close := strings.Index(rest, ")")
	if close < 0 || strings.TrimSpace(rest[close+1:]) != "" {
		t.Fatalf("lifecycle attempt index SQL has malformed trigger clause: %s", indexSQL)
	}
	return rest[:close]
}

func parseLifecycleAttemptTriggerLiteral(t *testing.T, value, indexSQL string) Trigger {
	t.Helper()
	if len(value) < 2 || value[0] != '\'' || value[len(value)-1] != '\'' {
		t.Fatalf("lifecycle attempt index trigger literal is not single quoted: %q in %s", value, indexSQL)
	}
	trigger := Trigger(value[1 : len(value)-1])
	if trigger == "" || strings.ContainsRune(string(trigger), '\'') {
		t.Fatalf("lifecycle attempt index trigger literal is invalid: %q", value)
	}
	return trigger
}

func assertLifecycleAttemptIndexIsUniquePartial(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "PRAGMA index_list('orchestration_command_journal')")
	if err != nil {
		t.Fatalf("inspect lifecycle journal indexes error = %v", err)
	}
	defer rows.Close()
	var found bool
	for rows.Next() {
		var sequence int
		var name, origin string
		var unique, partial int
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan lifecycle journal indexes error = %v", err)
		}
		if name == "idx_orchestration_command_journal_new_attempt" {
			found = true
			if unique != 1 || partial != 1 {
				t.Fatalf("lifecycle attempt index flags = unique %d partial %d, want 1/1", unique, partial)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate lifecycle journal indexes error = %v", err)
	}
	if !found {
		t.Fatal("lifecycle attempt partial unique index is missing")
	}
}
