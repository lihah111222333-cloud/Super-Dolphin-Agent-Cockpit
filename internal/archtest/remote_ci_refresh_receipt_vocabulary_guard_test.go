package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRemoteCIRefreshReceiptVocabularyRejectsRequiredCheckAliases prevents a
// refresh build observation from being represented as a normal CI test receipt.
func TestRemoteCIRefreshReceiptVocabularyRejectsRequiredCheckAliases(t *testing.T) {
	root := findRepoRoot(t)
	for _, relative := range []string{
		"internal/devtools/remoteci/oci_baseline_builder_protocol.go",
		"cmd/super-dolphin-gate/remote_oci_baseline_worker.go",
		"build/gate/closure/closure.go",
		"build/gate/Dockerfile",
	} {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read %s: %v", relative, err)
		}
		if violations := remoteCIRefreshReceiptLegacyVocabulary(string(contents)); len(violations) != 0 {
			t.Errorf("%s retains required-check aliases for a refresh build receipt: %v", relative, violations)
		}
	}
}

func TestRemoteCIRefreshReceiptVocabularyGuardCounterexample(t *testing.T) {
	if violations := remoteCIRefreshReceiptLegacyVocabulary("refresh_receipts refresh-build-receipt"); len(violations) != 0 {
		t.Fatalf("refresh-only vocabulary was rejected: %v", violations)
	}
	legacy := "OCIBuilderCheckReceiptArtifact check_receipts /out/check-receipts required-check-receipt REMOTE_CI_CHECK_PASS="
	if violations := remoteCIRefreshReceiptLegacyVocabulary(legacy); len(violations) != 5 {
		t.Fatalf("legacy required-check vocabulary was not fully rejected: %v", violations)
	}
}

func remoteCIRefreshReceiptLegacyVocabulary(contents string) []string {
	var violations []string
	for _, legacy := range []string{
		"OCIBuilderCheckReceiptArtifact",
		"check_receipts",
		"/out/check-receipts",
		"required-check-receipt",
		"REMOTE_CI_CHECK_PASS=",
	} {
		if strings.Contains(contents, legacy) {
			violations = append(violations, legacy)
		}
	}
	return violations
}
