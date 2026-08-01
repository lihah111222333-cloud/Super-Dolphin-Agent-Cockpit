package shardresource

import (
	"strings"
	"testing"
	"time"
)

func TestSelectUsesConfiguredBootstrapClassWithoutComparableObservations(t *testing.T) {
	policy := testPolicy()
	for _, testCase := range []struct {
		kind string
		want string
	}{
		{kind: "guard", want: "small"},
		{kind: "node_test", want: "standard"},
		{kind: "go_test", want: "compile"},
	} {
		t.Run(testCase.kind, func(t *testing.T) {
			selected, err := Select(policy, Shard{
				Identity:  "sha256:" + strings.Repeat("a", 64),
				Workloads: []Workload{{ID: "unit", Kind: testCase.kind}},
			}, Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}, nil)
			if err != nil || selected.ID != testCase.want {
				t.Fatalf("Select() = %#v, %v, want %q", selected, err, testCase.want)
			}
		})
	}
}

func TestSelectUpsizesImmediatelyAndDownsizesOnlyAfterStableSamples(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "go", Kind: "go_test"}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)

	high := []Observation{testObservation(shard.Identity, context, "compile", 3_900_000_000, 15<<30, now)}
	selected, err := Select(policy, shard, context, high)
	if err != nil || selected.ID != "maximum" {
		t.Fatalf("high Select() = %#v, %v, want maximum", selected, err)
	}

	low := make([]Observation, 0, policy.MinSamplesToDownsize)
	for index := range policy.MinSamplesToDownsize - 1 {
		low = append(low, testObservation(
			shard.Identity, context, "compile", 900_000_000, 2<<30,
			now.Add(time.Duration(index)*time.Minute),
		))
	}
	selected, err = Select(policy, shard, context, low)
	if err != nil || selected.ID != "compile" {
		t.Fatalf("early downsize Select() = %#v, %v, want compile", selected, err)
	}
	low = append(low, testObservation(
		shard.Identity, context, "compile", 900_000_000, 2<<30,
		now.Add(time.Duration(policy.MinSamplesToDownsize)*time.Minute),
	))
	selected, err = Select(policy, shard, context, low)
	if err != nil || selected.ID != "small" {
		t.Fatalf("stable downsize Select() = %#v, %v, want small", selected, err)
	}
}

func TestSelectMovesOneClassAfterOOMAndCapsAtMaximum(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "go", Kind: "go_test"}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		classID string
		want    string
	}{
		{classID: "compile", want: "large"},
		{classID: "maximum", want: "maximum"},
	} {
		t.Run(testCase.classID, func(t *testing.T) {
			observation := testObservation(shard.Identity, context, testCase.classID, 1, 1, now)
			observation.Succeeded = false
			observation.OOMKilled = true
			selected, err := Select(policy, shard, context, []Observation{observation})
			if err != nil || selected.ID != testCase.want {
				t.Fatalf("Select() = %#v, %v, want %q", selected, err, testCase.want)
			}
		})
	}
}

func TestPolicyRejectsNonStandardOrNonMonotonicClasses(t *testing.T) {
	for _, mutate := range []func(*Policy){
		func(policy *Policy) { policy.Classes[0].VCPU = 1 },
		func(policy *Policy) { policy.Classes[1].MemoryGiB = 2 },
		func(policy *Policy) { policy.Classes[1].ID = policy.Classes[0].ID },
		func(policy *Policy) { policy.Bootstrap.GoTest = "missing" },
		func(policy *Policy) { policy.HeadroomPercent = 0 },
		func(policy *Policy) { policy.MinSamplesToDownsize = 0 },
	} {
		policy := testPolicy()
		mutate(&policy)
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate() unexpectedly passed: %#v", policy)
		}
	}
}

func testPolicy() Policy {
	return Policy{
		Classes: []Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "standard", VCPU: 4, MemoryGiB: 8},
			{ID: "compile", VCPU: 4, MemoryGiB: 16},
			{ID: "large", VCPU: 8, MemoryGiB: 16},
			{ID: "maximum", VCPU: 8, MemoryGiB: 32},
		},
		Bootstrap:            BootstrapClasses{Guard: "small", NodeTest: "standard", GoTest: "compile"},
		HeadroomPercent:      25,
		MinSamplesToDownsize: 5,
	}
}

func testObservation(identity string, context Context, classID string, cpuNanoCores int64, memoryBytes int64, observedAt time.Time) Observation {
	return Observation{
		ShardIdentity: identity, Runner: context.Runner, Toolchain: context.Toolchain,
		ClassID: classID, PeakCPUNanoCores: cpuNanoCores, PeakMemoryBytes: memoryBytes,
		Succeeded: true, ObservedAt: observedAt,
	}
}
