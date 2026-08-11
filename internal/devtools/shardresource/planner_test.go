package shardresource

import (
	"math"
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
		{kind: "node_test", want: "small"},
		{kind: "go_test", want: "small"},
	} {
		t.Run(testCase.kind, func(t *testing.T) {
			selected, err := Select(policy, Shard{
				Identity: "sha256:" + strings.Repeat("a", 64),
				Workloads: []Workload{{
					ID: "unit", Kind: testCase.kind, EstimatedDurationMS: 70_001,
					ResourceCPU: 2, ResourceMemoryGiB: 4,
				}},
			}, Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}, nil)
			if err != nil || selected.ID != testCase.want {
				t.Fatalf("Select() = %#v, %v, want %q", selected, err, testCase.want)
			}
		})
	}
}

func TestSelectKeepsPeakCPUInsidePlannedResourceTier(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "guard", Kind: "guard", EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)

	high := []Observation{testObservation(shard.Identity, context, "small", 3_900_000_000, 4<<30, now)}
	if selected, err := Select(policy, shard, context, high); err == nil || selected.VCPU > 2 {
		t.Fatalf("high Select() = %#v, %v, want same-CPU failure", selected, err)
	}

	low := make([]Observation, 0, policy.MinSamplesToDownsize)
	for index := range policy.MinSamplesToDownsize - 1 {
		low = append(low, testObservation(
			shard.Identity, context, "small", 3_900_000_000, 1<<30,
			now.Add(time.Duration(index)*time.Minute),
		))
	}
	selected, err := Select(policy, shard, context, low)
	if err != nil || selected.ID != "small" || selected.VCPU != 2 {
		t.Fatalf("early downsize Select() = %#v, %v, want small at 2C", selected, err)
	}
	low = append(low, testObservation(
		shard.Identity, context, "small", 3_900_000_000, 1<<30,
		now.Add(time.Duration(policy.MinSamplesToDownsize)*time.Minute),
	))
	selected, err = Select(policy, shard, context, low)
	if err != nil || selected.ID != "small" || selected.VCPU != 2 {
		t.Fatalf("stable downsize Select() = %#v, %v, want small at 2C", selected, err)
	}
}

func TestSelectOOMNeverCrossesPlannedResourceTier(t *testing.T) {
	policy := testPolicy()
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		name              string
		durationMS        int64
		resourceCPU       float64
		resourceMemoryGiB float64
		classID           string
		wantCPU           float64
	}{
		{name: "planned small despite slow estimate", durationMS: 70_001, resourceCPU: 2, resourceMemoryGiB: 4, classID: "small", wantCPU: 2},
		{name: "planned medium despite fast estimate", durationMS: 1_000, resourceCPU: 4, resourceMemoryGiB: 8, classID: "medium", wantCPU: 4},
		{name: "planned maximum despite medium estimate", durationMS: 10_000, resourceCPU: 8, resourceMemoryGiB: 16, classID: "maximum", wantCPU: 8},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			shard := Shard{
				Identity: "sha256:" + strings.Repeat("a", 64),
				Workloads: []Workload{{
					ID: "workload", Kind: "go_test", EstimatedDurationMS: testCase.durationMS,
					ResourceCPU: testCase.resourceCPU, ResourceMemoryGiB: testCase.resourceMemoryGiB,
				}},
			}
			observation := testObservation(shard.Identity, context, testCase.classID, 1, 1, now)
			observation.Succeeded = false
			observation.OOMKilled = true
			selected, err := Select(policy, shard, context, []Observation{observation})
			if err == nil || selected.VCPU > testCase.wantCPU {
				t.Fatalf("Select() = %#v, %v, want same-CPU failure", selected, err)
			}
		})
	}
}

func TestSelectUsesSamePlannedCPUMemoryForPeakHeadroom(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "guard", Kind: "guard", EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	observation := testObservation(shard.Identity, context, "small", 8_000_000_000, 3<<30, time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC))
	selected, err := Select(policy, shard, context, []Observation{observation})
	if err != nil || selected.ID != "small" || selected.VCPU != 2 {
		t.Fatalf("Select() = %#v, %v, want small at 2C", selected, err)
	}
}

func TestSelectFailsOnPeakMemoryHeadroomOverflow(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "guard", Kind: "guard", EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	observation := testObservation(shard.Identity, context, "small", 1, math.MaxInt64, time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC))
	if selected, err := Select(policy, shard, context, []Observation{observation}); err == nil {
		t.Fatalf("Select() = %#v, nil, want fail-fast on MaxInt64 peak memory", selected)
	}
}

func TestSelectIgnoresCalibrationAndOtherCPUSamples(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "go-test", Kind: "go_test", EstimatedDurationMS: 1_000, ResourceCPU: 4, ResourceMemoryGiB: 8}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
	observations := []Observation{
		testObservation(shard.Identity, context, policy.CalibrationResource.ID, 1, 8<<30, now),
		testObservation(shard.Identity, context, "maximum", 1, 16<<30, now.Add(time.Minute)),
	}
	selected, err := Select(policy, shard, context, observations)
	if err != nil || selected.ID != "medium" {
		t.Fatalf("Select() = %#v, %v, want planned medium unaffected by calibration/other CPU samples", selected, err)
	}
}

func TestSelectOldOOMRequiresStableSameIdentityRecovery(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "guard", Kind: "guard", EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	now := time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC)
	oldOOM := testObservation(shard.Identity, context, "small", 1, 1, now)
	oldOOM.Succeeded = false
	oldOOM.OOMKilled = true
	newSuccess := testObservation(shard.Identity, context, "small", 1, 1<<30, now.Add(time.Minute))
	for _, observations := range [][]Observation{{oldOOM, newSuccess}, {newSuccess, oldOOM}} {
		if selected, err := Select(policy, shard, context, observations); err == nil {
			t.Fatalf("Select() = %#v, nil, want fail-fast after old OOM without stable recovery", selected)
		}
	}
	sameTimestampSuccess := newSuccess
	sameTimestampSuccess.ObservedAt = now
	var wantError string
	for index, observations := range [][]Observation{{oldOOM, sameTimestampSuccess}, {sameTimestampSuccess, oldOOM}} {
		selected, err := Select(policy, shard, context, observations)
		if err == nil {
			t.Fatalf("Select() = %#v, nil, want deterministic fail-fast with equal observation timestamps", selected)
		}
		if index == 0 {
			wantError = err.Error()
		} else if err.Error() != wantError {
			t.Fatalf("Select() error = %q after permutation, want deterministic %q", err, wantError)
		}
	}
}

func TestSelectOOMFailsWhenNoLargerMemoryWithinThePlannedResourceTier(t *testing.T) {
	policy := testPolicy()
	shard := Shard{
		Identity:  "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{{ID: "guard", Kind: "guard", EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4}},
	}
	context := Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}
	observation := testObservation(shard.Identity, context, "small", 1, 1, time.Date(2026, 7, 27, 15, 0, 0, 0, time.UTC))
	observation.Succeeded = false
	observation.OOMKilled = true
	selected, err := Select(policy, shard, context, []Observation{observation})
	if err == nil || selected.VCPU > 2 {
		t.Fatalf("Select() = %#v, %v, want same-CPU fail-fast", selected, err)
	}
}

func TestPolicyRejectsNonStandardOrNonMonotonicClasses(t *testing.T) {
	for _, mutate := range []func(*Policy){
		func(policy *Policy) { policy.Classes[0].VCPU = 1 },
		func(policy *Policy) { policy.Classes[1].MemoryGiB = 2 },
		func(policy *Policy) { policy.Classes[1].ID = policy.Classes[0].ID },
		func(policy *Policy) { policy.Bootstrap.GoTest = "missing" },
		func(policy *Policy) { policy.Bootstrap.GoTest = "medium" },
		func(policy *Policy) {
			policy.Classes = append(policy.Classes, Class{ID: "extra", VCPU: 2, MemoryGiB: 16})
		},
		func(policy *Policy) { policy.CalibrationResource.ID = "medium" },
		func(policy *Policy) { policy.HeadroomPercent = 0 },
		func(policy *Policy) { policy.MinSamplesToDownsize = 0 },
		func(policy *Policy) { policy.FastWorkloadMaxDurationMS++ },
		func(policy *Policy) { policy.MediumWorkloadMaxDurationMS++ },
	} {
		policy := testPolicy()
		mutate(&policy)
		if err := policy.Validate(); err == nil {
			t.Fatalf("Validate() unexpectedly passed: %#v", policy)
		}
	}
}

func TestPolicyRejectsAdditionalNormalMemoryTiers(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*Policy)
	}{
		{name: "2C extra memory", mutate: func(policy *Policy) { policy.Classes[0].MemoryGiB = 16 }},
		{name: "4C extra memory", mutate: func(policy *Policy) { policy.Classes[1].MemoryGiB = 16 }},
		{name: "8C extra memory", mutate: func(policy *Policy) { policy.Classes[2].MemoryGiB = 32 }},
		{name: "bootstrap memory uplift", mutate: func(policy *Policy) { policy.Bootstrap.NodeTest = "medium" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			policy := testPolicy()
			testCase.mutate(&policy)
			if err := policy.Validate(); err == nil {
				t.Fatalf("Validate() unexpectedly accepted non-canonical normal resource policy: %#v", policy)
			}
		})
	}
}

func TestSelectRejectsShardThatMixesPersistedResourceTiers(t *testing.T) {
	policy := testPolicy()
	_, err := Select(policy, Shard{
		Identity: "sha256:" + strings.Repeat("a", 64),
		Workloads: []Workload{
			{ID: "small", Kind: "guard", EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4},
			{ID: "medium", Kind: "go_test", EstimatedDurationMS: 10_000, ResourceCPU: 4, ResourceMemoryGiB: 8},
		},
	}, Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}, nil)
	if err == nil {
		t.Fatal("Select() accepted a shard that mixes 2C and 4C workloads")
	}
}

func TestPolicyAcceptsNormalMediumAndDistinctCalibrationClass(t *testing.T) {
	policy := testPolicy()
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate() rejected the canonical normal/calibration resources: %v", err)
	}

	normal, err := policy.ResolveClass("medium")
	if err != nil {
		t.Fatalf("ResolveClass(%q) = %#v, %v, want normal 4C/8GiB", "medium", normal, err)
	}
	assertFourCoreEightGiBClass(t, "normal medium", normal, "medium")

	calibration, err := policy.ResolveCalibrationClass()
	if err != nil {
		t.Fatalf("ResolveCalibrationClass() = %#v, %v, want distinct calibration 4C/8GiB", calibration, err)
	}
	assertDistinctCalibrationClass(t, normal, calibration)
	assertCalibrationResourceDriftRejected(t, policy)
}

func assertFourCoreEightGiBClass(t *testing.T, name string, class Class, wantID string) {
	t.Helper()
	if class.ID != wantID {
		t.Fatalf("%s class ID = %q, want %q", name, class.ID, wantID)
	}
	if class.VCPU != 4 {
		t.Fatalf("%s class vCPU = %g, want 4", name, class.VCPU)
	}
	if class.MemoryGiB != 8 {
		t.Fatalf("%s class memory = %g GiB, want 8 GiB", name, class.MemoryGiB)
	}
}

func assertDistinctCalibrationClass(t *testing.T, normal, calibration Class) {
	t.Helper()
	if calibration.ID == "medium" {
		t.Fatalf("calibration class ID = %q, must not reuse normal medium", calibration.ID)
	}
	if calibration.ID == normal.ID {
		t.Fatalf("calibration class ID = %q, must differ from normal %q", calibration.ID, normal.ID)
	}
	if calibration.VCPU != 4 {
		t.Fatalf("calibration class vCPU = %g, want 4", calibration.VCPU)
	}
	if calibration.MemoryGiB != 8 {
		t.Fatalf("calibration class memory = %g GiB, want 8 GiB", calibration.MemoryGiB)
	}
}

func assertCalibrationResourceDriftRejected(t *testing.T, policy Policy) {
	t.Helper()
	for _, mutate := range []func(*Policy){
		func(invalid *Policy) { invalid.CalibrationResource.ID = "medium" },
		func(invalid *Policy) { invalid.CalibrationResource.VCPU = 2 },
		func(invalid *Policy) { invalid.CalibrationResource.MemoryGiB = 16 },
	} {
		invalid := policy
		mutate(&invalid)
		if err := invalid.Validate(); err == nil {
			t.Fatalf("Validate() accepted invalid calibration resource: %#v", invalid.CalibrationResource)
		}
	}
}

func TestSelectUsesPersistedWorkloadResourceIdentityAndNeverCalibration(t *testing.T) {
	policy := testPolicy()
	for _, testCase := range []struct {
		name              string
		durationMS        int64
		resourceCPU       float64
		resourceMemoryGiB float64
		want              string
	}{
		{name: "small resource identity wins over slow duration", durationMS: 70_001, resourceCPU: 2, resourceMemoryGiB: 4, want: "small"},
		{name: "medium resource identity wins over fast duration", durationMS: 1_000, resourceCPU: 4, resourceMemoryGiB: 8, want: "medium"},
		{name: "maximum resource identity wins over medium duration", durationMS: 10_000, resourceCPU: 8, resourceMemoryGiB: 16, want: "maximum"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			selected, err := Select(policy, Shard{
				Identity: "sha256:" + strings.Repeat("a", 64),
				Workloads: []Workload{{
					ID: "guard", Kind: "guard", EstimatedDurationMS: testCase.durationMS,
					ResourceCPU: testCase.resourceCPU, ResourceMemoryGiB: testCase.resourceMemoryGiB,
				}},
			}, Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)}, nil)
			if err != nil || selected.ID != testCase.want || selected.ID == policy.CalibrationResource.ID {
				t.Fatalf("duration %d Select() = %#v, %v, want %q normal class", testCase.durationMS, selected, err, testCase.want)
			}
		})
	}
}

func testPolicy() Policy {
	return Policy{
		Classes: []Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "medium", VCPU: 4, MemoryGiB: 8},
			{ID: "maximum", VCPU: 8, MemoryGiB: 16},
		},
		Bootstrap:                 BootstrapClasses{Guard: "small", NodeTest: "small", GoTest: "small"},
		CalibrationResource:       Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
		FastWorkloadMaxDurationMS: 5_000, MediumWorkloadMaxDurationMS: 70_000,
		HeadroomPercent: 25, MinSamplesToDownsize: 5,
	}
}

func testObservation(identity string, context Context, classID string, cpuNanoCores int64, memoryBytes int64, observedAt time.Time) Observation {
	return Observation{
		ShardIdentity: identity, Runner: context.Runner, Toolchain: context.Toolchain,
		ClassID: classID, PeakCPUNanoCores: cpuNanoCores, PeakMemoryBytes: memoryBytes,
		Succeeded: true, ObservedAt: observedAt,
	}
}
