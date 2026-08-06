package codemapindex

import "testing"

func TestManagedOutputsReturnsIndependentSnapshot(t *testing.T) {
	first := managedOutputs()
	first[0] = "mutated-by-caller"

	want := []string{
		"README.md",
		"docs/doc/codemap/13-archtest-boundaries.md",
		"docs/doc/codemap/README.md",
		"docs/doc/codemap/ai-index.json",
		"docs/doc/codemap/anchor-identities.json",
	}
	got := managedOutputs()
	if len(got) != len(want) {
		t.Fatalf("managedOutputs() length = %d, want %d", len(got), len(want))
	}
	for index, output := range want {
		if got[index] != output {
			t.Fatalf("managedOutputs()[%d] = %q, want %q", index, got[index], output)
		}
	}
}
