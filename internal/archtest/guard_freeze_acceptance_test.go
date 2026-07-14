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
	want := map[string]string{
		"Owner":             "owner",
		"Reason":            "reason",
		"ReviewedAt":        "reviewed_at",
		"ReviewBy":          "review_by",
		"FailFirstEvidence": "fail_first_evidence",
		"EvidenceSHA256":    "evidence_sha256",
	}
	if typ.NumField() != len(want) {
		t.Fatalf("GuardFreezeAcceptance field count = %d, want %d", typ.NumField(), len(want))
	}
	for fieldName, jsonName := range want {
		field, ok := typ.FieldByName(fieldName)
		if !ok {
			t.Errorf("GuardFreezeAcceptance missing field %s", fieldName)
			continue
		}
		if got, _, _ := strings.Cut(field.Tag.Get("json"), ","); got != jsonName {
			t.Errorf("GuardFreezeAcceptance.%s json tag = %q, want %q", fieldName, got, jsonName)
		}
	}
}

func TestGuardFreezeSnapshotFieldRegistry(t *testing.T) {
	typ := reflect.TypeFor[GuardFreezeSnapshot]()
	want := map[string]string{"Metrics": "metrics", "PrioritySSA": "priority_ssa"}
	if typ.NumField() != len(want) {
		t.Fatalf("GuardFreezeSnapshot field count = %d, want %d", typ.NumField(), len(want))
	}
	for fieldName, jsonName := range want {
		field, ok := typ.FieldByName(fieldName)
		if !ok {
			t.Errorf("GuardFreezeSnapshot missing field %s", fieldName)
			continue
		}
		if got, _, _ := strings.Cut(field.Tag.Get("json"), ","); got != jsonName {
			t.Errorf("GuardFreezeSnapshot.%s json tag = %q, want %q", fieldName, got, jsonName)
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
		{name: "absolute evidence", mutate: func(value *GuardFreezeAcceptance) { value.FailFirstEvidence = "/tmp/evidence.txt" }, wantErr: "repository-relative"},
		{name: "parent evidence", mutate: func(value *GuardFreezeAcceptance) { value.FailFirstEvidence = "../evidence.txt" }, wantErr: "normalized"},
		{name: "invalid evidence hash", mutate: func(value *GuardFreezeAcceptance) { value.EvidenceSHA256 = "ABC" }, wantErr: "evidence_sha256"},
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

func TestValidateGuardFreezeAcceptanceRejectsFutureAndLongReview(t *testing.T) {
	acceptance := validGuardFreezeAcceptance()
	now := time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)
	acceptance.ReviewedAt = "2026-07-14T00:06:00Z"
	if err := validateGuardFreezeAcceptanceDatesAt(acceptance, now); err == nil || !strings.Contains(err.Error(), "future") {
		t.Fatalf("future review error = %v, want future", err)
	}
	acceptance.ReviewedAt = "2026-07-14T00:00:00Z"
	acceptance.ReviewBy = "2026-10-13"
	if err := validateGuardFreezeAcceptanceDatesAt(acceptance, now); err == nil || !strings.Contains(err.Error(), "90 days") {
		t.Fatalf("long review error = %v, want 90 days", err)
	}
}

func TestGuardFreezeJSONRejectsUnknownAndMissingAcceptanceFields(t *testing.T) {
	valid := NewEmptyGuardFreeze(validGuardFreezeAcceptance())
	body, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "unknown top-level", body: strings.TrimSuffix(string(body), "}") + `,"shadow_source":{}}`, wantErr: "unknown field"},
		{name: "missing acceptance", body: `{"version":3,"approved":{"metrics":{"production":{},"tests":{}},"priority_ssa":{}},"metrics":{"production":{},"tests":{}},"priority_ssa":{}}`, wantErr: "acceptance"},
		{name: "trailing json", body: string(body) + `{}`, wantErr: "trailing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeGuardFreeze([]byte(test.body)); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("decodeGuardFreeze() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestGuardFreezeEvidenceAndRoundTrip(t *testing.T) {
	_, freezePath, freeze := newBoundGuardFreezeFixture(t)
	shrunk := freeze
	shrunk.Metrics.Tests = Baseline{}
	if err := SaveGuardFreeze(freezePath, shrunk); err != nil {
		t.Fatalf("SaveGuardFreeze() shrink error = %v", err)
	}
	loaded, err := LoadGuardFreeze(freezePath)
	if err != nil {
		t.Fatalf("LoadGuardFreeze() error = %v", err)
	}
	if !reflect.DeepEqual(loaded.Data, shrunk) {
		t.Fatalf("roundtrip mismatch: got %#v want %#v", loaded.Data, shrunk)
	}
}

func TestGuardFreezeRejectsUnapprovedExpansion(t *testing.T) {
	_, freezePath, freeze := newBoundGuardFreezeFixture(t)
	expanded := freeze
	expanded.Metrics.Tests = Baseline{"unapproved_test.go": {}}
	if err := SaveGuardFreeze(freezePath, expanded); err == nil || !strings.Contains(err.Error(), "approved snapshot") {
		t.Fatalf("SaveGuardFreeze() expansion error = %v, want approved snapshot", err)
	}
	reapprovedWithoutEvidence := expanded
	reapprovedWithoutEvidence.Approved = currentGuardFreezeSnapshot(reapprovedWithoutEvidence)
	if err := SaveGuardFreeze(freezePath, reapprovedWithoutEvidence); err == nil || !strings.Contains(err.Error(), "snapshot_sha256") {
		t.Fatalf("SaveGuardFreeze() reapproval error = %v, want snapshot_sha256", err)
	}
}

func newBoundGuardFreezeFixture(t *testing.T) (string, string, GuardFreeze) {
	t.Helper()
	root := t.TempDir()
	freezePath := filepath.Join(root, "internal", "archtest", "freeze_baseline.json")
	if err := os.MkdirAll(filepath.Dir(freezePath), 0o755); err != nil {
		t.Fatal(err)
	}
	freeze := NewEmptyGuardFreeze(validGuardFreezeAcceptance())
	freeze.Metrics.Tests["legacy_test.go"] = FileMetrics{}
	freeze.Approved = currentGuardFreezeSnapshot(freeze)
	if err := SaveGuardFreeze(freezePath, freeze); err == nil || !strings.Contains(err.Error(), "fail-first evidence") {
		t.Fatalf("SaveGuardFreeze() missing evidence error = %v", err)
	}
	freeze.Acceptance = writeGuardFreezeEvidence(t, root, freeze)
	if err := SaveGuardFreeze(freezePath, freeze); err != nil {
		t.Fatalf("SaveGuardFreeze() error = %v", err)
	}
	return root, freezePath, freeze
}

func TestGuardFreezeEvidenceRequiresExactUniqueFields(t *testing.T) {
	acceptance := validGuardFreezeAcceptance()
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "exit ten", body: validGuardFreezeEvidence(acceptance, "expected_exit: 10"), wantErr: "expected_exit"},
		{name: "prefixed source", body: strings.Replace(validGuardFreezeEvidence(acceptance, "expected_exit: 1"), "source_head:", "not_source_head:", 1), wantErr: "unknown field"},
		{name: "empty command", body: strings.Replace(validGuardFreezeEvidence(acceptance, "expected_exit: 1"), "command: go test ./...", "command:", 1), wantErr: "command"},
		{name: "duplicate source", body: validGuardFreezeEvidence(acceptance, "expected_exit: 1") + "source_head: other\n", wantErr: "duplicate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateGuardFreezeEvidenceBody([]byte(test.body), acceptance, "fixture-head", strings.Repeat("b", 64))
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateGuardFreezeEvidenceBody() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestGuardFreezeEvidenceRejectsParentSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "evidence.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs", "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := readGuardFreezeEvidence(root, "docs/linked/evidence.txt"); err == nil || !strings.Contains(err.Error(), "repository") {
		t.Fatalf("readGuardFreezeEvidence() error = %v, want repository containment failure", err)
	}
}

func writeGuardFreezeEvidence(t *testing.T, root string, freeze GuardFreeze) GuardFreezeAcceptance {
	t.Helper()
	evidencePath := filepath.Join(root, filepath.FromSlash(freeze.Acceptance.FailFirstEvidence))
	if err := os.MkdirAll(filepath.Dir(evidencePath), 0o755); err != nil {
		t.Fatal(err)
	}
	snapshotSHA256, err := guardFreezeSnapshotSHA256(freeze)
	if err != nil {
		t.Fatal(err)
	}
	evidence := validGuardFreezeEvidence(freeze.Acceptance, "expected_exit: 1")
	evidence = strings.Replace(evidence, strings.Repeat("b", 64), snapshotSHA256, 1)
	if err := os.WriteFile(evidencePath, []byte(evidence), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := BindGuardFreezeAcceptance(root, "other-head", freeze); err == nil || !strings.Contains(err.Error(), "source_head") {
		t.Fatalf("stale evidence bind error = %v, want source_head", err)
	}
	changedFreeze := freeze
	changedFreeze.Approved.Metrics.Tests = nil
	if _, err := BindGuardFreezeAcceptance(root, "fixture-head", changedFreeze); err == nil || !strings.Contains(err.Error(), "snapshot_sha256") {
		t.Fatalf("stale snapshot bind error = %v, want snapshot_sha256", err)
	}
	bound, err := BindGuardFreezeAcceptance(root, "fixture-head", freeze)
	if err != nil {
		t.Fatalf("BindGuardFreezeAcceptance() error = %v", err)
	}
	return bound
}

func validGuardFreezeEvidence(acceptance GuardFreezeAcceptance, expectedExit string) string {
	return "source_head: fixture-head\nreviewed_at: " + acceptance.ReviewedAt +
		"\nsnapshot_sha256: " + strings.Repeat("b", 64) +
		"\nworking_directory: .\ncommand: go test ./...\n" + expectedExit +
		"\nobserved_failure: missing acceptance\n"
}

func validGuardFreezeAcceptance() GuardFreezeAcceptance {
	reviewedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	return GuardFreezeAcceptance{
		Owner:             "repository-maintainers",
		Reason:            "reviewed guard debt",
		ReviewedAt:        reviewedAt.Format(time.RFC3339),
		ReviewBy:          reviewedAt.AddDate(0, 1, 0).Format(time.DateOnly),
		FailFirstEvidence: "docs/guards/code-size-freeze-v3-fail-first.txt",
		EvidenceSHA256:    strings.Repeat("a", 64),
	}
}
