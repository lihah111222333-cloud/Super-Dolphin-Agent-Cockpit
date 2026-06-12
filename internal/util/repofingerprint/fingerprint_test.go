package repofingerprint

import "testing"

func TestComputeReturns128BitHex(t *testing.T) {
	t.Parallel()
	a, err := Compute("/tmp/repo-a")
	if err != nil {
		t.Fatalf("Compute error = %v", err)
	}
	b, err := Compute("/tmp/repo-b")
	if err != nil {
		t.Fatalf("Compute error = %v", err)
	}
	if a == b {
		t.Fatalf("different paths produced same fingerprint %q", a)
	}
	if len(a) != 32 || !IsValid(a) {
		t.Fatalf("fingerprint = %q, want 32 lower hex chars", a)
	}
	again, err := Compute("/tmp/repo-a")
	if err != nil {
		t.Fatalf("Compute repeat error = %v", err)
	}
	if again != a {
		t.Fatalf("not deterministic: %q vs %q", a, again)
	}
}

func TestComputeEmpty(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "   "} {
		got, err := Compute(in)
		if err != nil {
			t.Fatalf("Compute(%q) error = %v", in, err)
		}
		if got != "" {
			t.Fatalf("Compute(%q) = %q, want empty", in, got)
		}
	}
}

func TestIsValid(t *testing.T) {
	t.Parallel()
	if !IsValid("0123456789abcdef0123456789abcdef") {
		t.Fatal("valid 128-bit hex rejected")
	}
	for _, fp := range []string{"", "0123", "0123456789abcdef0123456789abcdeg", "0123456789ABCDEF0123456789ABCDEF"} {
		if IsValid(fp) {
			t.Fatalf("IsValid(%q) = true, want false", fp)
		}
	}
}
