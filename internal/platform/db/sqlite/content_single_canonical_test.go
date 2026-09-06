package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

type contentTextContract struct {
	kind       string
	maxLength  int
	allowEmpty bool
}

const (
	contentTextCanonical = "canonical"
	contentTextExact     = "exact"
	contentTextLowerHex  = "lower_hex"
)

func assertContentCanonicalText(t *testing.T) {
	t.Helper()
	for table, fixture := range contentStandaloneFixtures() {
		table, fixture := table, fixture
		t.Run(table, func(t *testing.T) {
			db := openContentSingleDB(t)
			disableContentRelations(t, db, table)
			insertContentMapOK(t, db, table, fixture)
			contentExecOK(t, db, "DELETE FROM "+table)
			for column, contract := range contentTableTextContracts(t, db, table) {
				assertContentTextColumn(t, db, table, column, fixture, contract)
			}
		})
	}
}

func assertContentTextColumn(
	t *testing.T, db *sql.DB, table, column string, fixture map[string]any, contract contentTextContract,
) {
	t.Helper()
	if contract.kind == contentTextExact {
		return
	}
	invalid := map[string]string{
		"nul":      fixture[column].(string) + "\x00malicious",
		"oversize": strings.Repeat("a", contract.maxLength+1),
	}
	if contract.kind == contentTextLowerHex {
		invalid["uppercase"] = strings.Repeat("A", contract.maxLength)
		invalid["non_hex"] = strings.Repeat("g", contract.maxLength)
	} else {
		invalid["whitespace"] = " " + fixture[column].(string)
		if !contract.allowEmpty {
			invalid["empty"] = ""
		}
	}
	assertContentTextValuesRejected(t, db, table, column, fixture, invalid)
}

func assertContentTextValuesRejected(
	t *testing.T,
	db *sql.DB,
	table string,
	column string,
	fixture map[string]any,
	invalid map[string]string,
) {
	t.Helper()
	for name, value := range invalid {
		name, value := name, value
		t.Run(column+"/"+name, func(t *testing.T) {
			candidate := cloneContentRow(fixture)
			candidate[column] = value
			if err := insertContentMap(db, table, candidate); err == nil {
				t.Fatalf("%s.%s accepted %s value", table, column, name)
			}
		})
	}
}

func contentTableTextContracts(t *testing.T, db *sql.DB, table string) map[string]contentTextContract {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("table_info %s: %v", table, err)
	}
	defer rows.Close()
	contracts := make(map[string]contentTextContract)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan table_info %s: %v", table, err)
		}
		if strings.EqualFold(kind, "TEXT") {
			contract, ok := contentTextContractFor(name)
			if !ok {
				t.Fatalf("%s TEXT column %s has no canonical, lower-hex, or exact contract", table, name)
			}
			contracts[name] = contract
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info %s: %v", table, err)
	}
	return contracts
}

func disableContentRelations(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("disable foreign keys for %s constraint probes: %v", table, err)
	}
	var enabled int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil {
		t.Fatalf("read foreign_keys for %s constraint probes: %v", table, err)
	}
	if enabled != 0 {
		t.Fatalf("foreign keys remain enabled for %s constraint probes", table)
	}
	for _, trigger := range contentTriggerNames(t, db, table) {
		if _, err := db.Exec("DROP TRIGGER " + trigger); err != nil {
			t.Fatalf("drop %s trigger %s for constraint probes: %v", table, trigger, err)
		}
	}
}

func contentTriggerNames(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name=?`, table)
	if err != nil {
		t.Fatalf("list %s triggers for constraint probes: %v", table, err)
	}
	var triggers []string
	for rows.Next() {
		var trigger string
		if err := rows.Scan(&trigger); err != nil {
			_ = rows.Close()
			t.Fatalf("scan %s trigger for constraint probes: %v", table, err)
		}
		triggers = append(triggers, trigger)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		t.Fatalf("iterate %s triggers for constraint probes: %v", table, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close %s trigger rows: %v", table, err)
	}
	return triggers
}

func contentTextContractFor(column string) (contentTextContract, bool) {
	exact := map[string]bool{
		"aead_algorithm": true, "aad_profile": true, "content_state": true, "key_state": true,
		"backend_kind": true, "payload_format": true, "reference_kind": true, "reference_state": true,
		"fence_kind": true, "fence_state": true,
	}
	if exact[column] {
		return contentTextContract{kind: contentTextExact}, true
	}
	return contentVariableTextContractFor(column)
}

func contentVariableTextContractFor(column string) (contentTextContract, bool) {
	switch {
	case column == "nonce_hex":
		return contentTextContract{kind: contentTextLowerHex, maxLength: 24}, true
	case strings.HasSuffix(column, "_digest") || column == "ciphertext_sha256":
		return contentTextContract{kind: contentTextLowerHex, maxLength: 64}, true
	case strings.HasSuffix(column, "_at"):
		return contentTextContract{kind: contentTextCanonical, maxLength: 64, allowEmpty: column == "promoted_at"}, true
	case column == "owner_id":
		return contentTextContract{kind: contentTextCanonical, maxLength: 256}, true
	case strings.HasSuffix(column, "_ref"):
		return contentTextContract{kind: contentTextCanonical, maxLength: 512}, true
	case strings.HasSuffix(column, "_id") || column == "media_type" || column == "key_wrap_profile":
		return contentTextContract{kind: contentTextCanonical, maxLength: 128}, true
	default:
		return contentTextContract{}, false
	}
}

func contentStandaloneFixtures() map[string]map[string]any {
	reservation := validContentReservation(32, "")
	object := validContentObject(reservation, "")
	object["created_at"], object["updated_at"] = "2035-01-01T00:00:00Z", "2035-01-01T00:00:00Z"
	receipt := validContentReceipt(object)
	reference := validContentRef(reservation)
	reference["created_at"] = "2035-03-01T00:00:00Z"
	fence := map[string]any{
		"command_id": "profile-command", "reference_id": reference["reference_id"],
		"canonical_ref": reference["canonical_ref"], "object_id": reference["object_id"],
		"object_version": int64(1), "ref_version": int64(1), "fence_kind": "non_terminal_command",
		"fence_state": "held", "command_profile_digest": strings.Repeat("a", 64),
		"source_fence_ref": "source-fence-src", "target_fence_ref": "source-fence-dst",
		"schema_version": int64(1), "created_at": "2035-03-01T00:00:00Z",
	}
	return map[string]map[string]any{
		"orchestration_content_objects":                  object,
		"orchestration_content_private_payload_receipts": receipt,
		"orchestration_content_refs":                     reference,
		"orchestration_command_content_deletion_fences":  fence,
	}
}

func cloneContentRow(row map[string]any) map[string]any {
	clone := make(map[string]any, len(row))
	for column, value := range row {
		clone[column] = value
	}
	return clone
}

func assertLegacyContentQuarantineImmutable(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`UPDATE orchestration_legacy_command_content_quarantine SET reason='legacy_missing_content_owner_ref'`,
		`DELETE FROM orchestration_legacy_command_content_quarantine`,
	} {
		if _, err := db.Exec(statement); err == nil {
			t.Fatalf("legacy CONTENT quarantine accepted mutation %q", statement)
		}
	}
}
