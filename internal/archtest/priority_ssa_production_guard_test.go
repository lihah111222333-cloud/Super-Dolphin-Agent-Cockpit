package archtest_test

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

func validatePrioritySSAFrozenMetadata(t *testing.T, baseline archtest.PrioritySSABaseline) {
	t.Helper()
	for key, violation := range baseline {
		if violation.Rule == "" || violation.File == "" || violation.Detail == "" {
			t.Fatalf("priority SSA freeze entry %q lost rule/file/detail metadata: %#v", key, violation)
		}
		if key != violation.Key() {
			t.Fatalf("priority SSA freeze key mismatch: got %q want %q", key, violation.Key())
		}
	}
}
