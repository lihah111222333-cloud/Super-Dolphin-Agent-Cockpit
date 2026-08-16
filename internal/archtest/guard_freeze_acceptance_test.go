package archtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGuardFreezeAcceptanceFieldRegistry(t *testing.T) {
	typ := reflect.TypeFor[GuardFreezeAcceptance]()
	want := map[string]string{"Owner": "owner", "Reason": "reason", "ReviewedAt": "reviewed_at", "ReviewBy": "review_by"}
	if typ.NumField() != len(want) {
		t.Fatalf("GuardFreezeAcceptance field count = %d, want %d", typ.NumField(), len(want))
	}
	for fieldName, jsonName := range want {
		field, ok := typ.FieldByName(fieldName)
		if !ok {
			t.Fatalf("GuardFreezeAcceptance missing field %s", fieldName)
		}
		if got, _, _ := strings.Cut(field.Tag.Get("json"), ","); got != jsonName {
			t.Errorf("GuardFreezeAcceptance.%s json tag = %q, want %q", fieldName, got, jsonName)
		}
	}
}

func TestValidateGuardFreezeAcceptance(t *testing.T) {
	valid := validGuardFreezeAcceptance()
	tests := []struct {
		name    string
		mutate  func(*GuardFreezeAcceptance)
		wantErr string
	}{
		{name: "missing owner", mutate: func(value *GuardFreezeAcceptance) { value.Owner = "" }, wantErr: "owner"},
		{name: "blank reason", mutate: func(value *GuardFreezeAcceptance) { value.Reason = "  " }, wantErr: "reason"},
		{name: "invalid reviewed at", mutate: func(value *GuardFreezeAcceptance) { value.ReviewedAt = "2026-07-14" }, wantErr: "reviewed_at"},
		{name: "non utc reviewed at", mutate: func(value *GuardFreezeAcceptance) { value.ReviewedAt = "2026-07-14T10:00:00+09:00" }, wantErr: "UTC"},
		{name: "invalid review by", mutate: func(value *GuardFreezeAcceptance) { value.ReviewBy = "2026/10/12" }, wantErr: "review_by"},
		{name: "review not after acceptance", mutate: func(value *GuardFreezeAcceptance) { value.ReviewBy = "2026-07-14" }, wantErr: "after reviewed_at"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid
			test.mutate(&value)
			err := ValidateGuardFreezeAcceptance(value)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateGuardFreezeAcceptance() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
	if err := ValidateGuardFreezeAcceptance(valid); err != nil {
		t.Fatalf("valid acceptance rejected: %v", err)
	}
}

func TestValidateGuardFreezeAcceptanceRejectsExpiredReview(t *testing.T) {
	acceptance := validGuardFreezeAcceptance()
	err := validateGuardFreezeAcceptanceDatesAt(acceptance, time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired review error = %v, want expired", err)
	}
}

func TestGuardFreezeJSONRejectsRemovedExternalEvidenceFields(t *testing.T) {
	valid := NewEmptyGuardFreeze(validGuardFreezeAcceptance())
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(body), `"review_by":`, `"fail_first_evidence":"proof.txt","evidence_sha256":"abc","review_by":`, 1)
	if _, err := decodeGuardFreeze([]byte(mutated)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("decodeGuardFreeze() error = %v, want removed evidence fields rejected", err)
	}
}

func TestGuardFreezeRoundTripAndRejectsExpansion(t *testing.T) {
	root := t.TempDir()
	freezePath := filepath.Join(root, "internal", "archtest", "freeze_baseline.json")
	if err := os.MkdirAll(filepath.Dir(freezePath), 0o755); err != nil {
		t.Fatal(err)
	}
	freeze := NewEmptyGuardFreeze(validGuardFreezeAcceptance())
	freeze.Metrics.Tests["legacy_test.go"] = FileMetrics{}
	freeze.Approved = currentGuardFreezeSnapshot(freeze)
	if err := SaveGuardFreeze(freezePath, freeze); err != nil {
		t.Fatalf("SaveGuardFreeze() error = %v", err)
	}
	loaded, err := LoadGuardFreeze(freezePath)
	if err != nil {
		t.Fatalf("LoadGuardFreeze() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.Data, freeze) {
		t.Fatalf("roundtrip mismatch: got %#v want %#v", loaded.Data, freeze)
	}
	expanded := freeze
	expanded.Metrics.Tests = Baseline{"unapproved_test.go": {}}
	if err := SaveGuardFreeze(freezePath, expanded); err == nil || !strings.Contains(err.Error(), "approved snapshot") {
		t.Fatalf("SaveGuardFreeze() expansion error = %v, want approved snapshot", err)
	}
}

func validGuardFreezeAcceptance() GuardFreezeAcceptance {
	reviewedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	return GuardFreezeAcceptance{
		Owner: "repository-maintainers", Reason: "reviewed guard debt",
		ReviewedAt: reviewedAt.Format(time.RFC3339), ReviewBy: reviewedAt.AddDate(0, 1, 0).Format(time.DateOnly),
	}
}
