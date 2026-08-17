package catalog

import (
	"slices"
	"strings"
	"testing"
)

func TestParseRemoteTestCommandRequiresExplicitExactRunSelector(t *testing.T) {
	_, err := ParseRemoteTestCommand([]string{"go", "test", "./pkg"})
	if err == nil || !strings.Contains(err.Error(), "explicit exact -run") {
		t.Fatalf("ParseRemoteTestCommand(no -run) error = %v", err)
	}
}

func TestLocalHostTestCommandRequiresSingleExactSelectorAndForcesNoCache(t *testing.T) {
	workload := Workload{TimeoutSeconds: 120, Command: []string{"go", "test", "./pkg", "-run", "^TestAlpha$"}}
	command, err := LocalHostTestCommand(workload)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./scripts/test_with_guard.sh", "--host-test", "light", "./pkg", "-run", "^TestAlpha$", "-timeout", "120s", "-count", "1"}
	if !slices.Equal(command, want) {
		t.Fatalf("LocalHostTestCommand() = %#v, want %#v", command, want)
	}
	workload.Command = []string{"go", "test", "./pkg", "./other", "-run", "^TestAlpha$"}
	if _, err := LocalHostTestCommand(workload); err == nil || !strings.Contains(err.Error(), "exactly one package") {
		t.Fatalf("LocalHostTestCommand(multiple packages) error = %v", err)
	}
	workload.Command = []string{"go", "test", "./pkg", "-run", "^Test(?:Alpha|Beta)$"}
	if _, err := LocalHostTestCommand(workload); err == nil || !strings.Contains(err.Error(), "one test selector") {
		t.Fatalf("LocalHostTestCommand(multiple tests) error = %v", err)
	}
}

func TestAuthorityFlagScannersStopAtPositionalSentinel(t *testing.T) {
	args := []string{"--tree", "before", "--", "-tree=after", "--tree", "after"}
	canonical, err := CanonicalizeAuthorityFlags(args, "tree")
	if err != nil {
		t.Fatal(err)
	}
	if canonical[3] != "-tree=after" || canonical[4] != "--tree" {
		t.Fatalf("CanonicalizeAuthorityFlags crossed -- sentinel: %#v", canonical)
	}
	if err := ValidateNoDuplicateAuthorityFlags(args, "tree"); err != nil {
		t.Fatalf("ValidateNoDuplicateAuthorityFlags crossed -- sentinel: %v", err)
	}
	if err := ValidateExplicitTreeFlagArgs(args); err != nil {
		t.Fatalf("ValidateExplicitTreeFlagArgs crossed -- sentinel: %v", err)
	}
}
