package cicontract

import "testing"

func TestNormalResourceMapperAdaptersUseCanonicalIdentity(t *testing.T) {
	assertNormalResourceTupleAdapter(t)
	assertNormalResourceClassAdapter(t)
	assertNormalResourceClassProjection(t)
}

func assertNormalResourceTupleAdapter(t *testing.T) {
	t.Helper()
	cpu, memoryGiB, err := NormalResourceTupleForTier(WorkloadResourceTierFast)
	if err != nil || cpu != 2 || memoryGiB != 4 {
		t.Fatalf("NormalResourceTupleForTier(fast) = (%g, %g, %v), want (2, 4, nil)", cpu, memoryGiB, err)
	}
	if _, _, err := NormalResourceTupleForTier(0); err == nil {
		t.Fatal("NormalResourceTupleForTier(unknown) accepted unsupported tier")
	}
}

func assertNormalResourceClassAdapter(t *testing.T) {
	t.Helper()
	classID, err := NormalResourceClassForTier(WorkloadResourceTierSlow)
	if err != nil || classID != "maximum" {
		t.Fatalf("NormalResourceClassForTier(slow) = (%q, %v), want (maximum, nil)", classID, err)
	}
	if classID, err := NormalResourceClassForTier(0); err == nil || err.Error() != "remote CI normal resource tier 0 is unsupported" {
		t.Fatalf("NormalResourceClassForTier(unknown) = (%q, %v), want exact unsupported-tier error", classID, err)
	}
}

func assertNormalResourceClassProjection(t *testing.T) {
	t.Helper()

	for _, testCase := range []struct {
		name           string
		classID        string
		cpu, memoryGiB float64
		want           string
		wantError      bool
	}{
		{name: "class from tuple", cpu: 8, memoryGiB: 16, want: "maximum"},
		{name: "validated class", classID: "medium", cpu: 4, memoryGiB: 8, want: "medium"},
		{name: "class tuple mismatch", classID: "medium", cpu: 2, memoryGiB: 4, wantError: true},
		{name: "unsupported tuple", cpu: 2, memoryGiB: 8, wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assertNormalResourceClassProjectionCase(t, testCase.classID, testCase.cpu, testCase.memoryGiB, testCase.want, testCase.wantError)
		})
	}
}

func assertNormalResourceClassProjectionCase(t *testing.T, classID string, cpu, memoryGiB float64, want string, wantError bool) {
	t.Helper()
	got, err := NormalResourceClassProjection(classID, cpu, memoryGiB)
	if wantError {
		if err == nil {
			t.Fatalf("NormalResourceClassProjection() = %q, want error", got)
		}
		return
	}
	if err != nil || got != want {
		t.Fatalf("NormalResourceClassProjection() = (%q, %v), want (%q, nil)", got, err, want)
	}
}
