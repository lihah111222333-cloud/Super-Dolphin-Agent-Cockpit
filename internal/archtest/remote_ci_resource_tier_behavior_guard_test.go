package archtest

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// TestRemoteCIResourceTierSelectorUsesPersistedResourceIdentity 守卫观测只能在计划固化的 CPU 档内调整内存。
func TestRemoteCIResourceTierSelectorUsesPersistedResourceIdentity(t *testing.T) {
	policy := remoteCIResourceTierBehaviorGuardPolicy()
	identity := "sha256:" + strings.Repeat("a", 64)
	context := shardresource.Context{
		Runner:    "sha256:" + strings.Repeat("b", 64),
		Toolchain: "sha256:" + strings.Repeat("c", 64),
	}

	for _, testCase := range []struct {
		name              string
		durationMS        int64
		resourceCPU       float64
		resourceMemoryGiB float64
		observedClass     string
		peak              bool
		wantCPU           float64
	}{
		{name: "planned small despite slow duration", durationMS: 70_001, resourceCPU: 2, resourceMemoryGiB: 4, observedClass: "small", peak: true, wantCPU: 2},
		{name: "planned small oom despite slow duration", durationMS: 70_001, resourceCPU: 2, resourceMemoryGiB: 4, observedClass: "small", wantCPU: 2},
		{name: "planned medium despite fast duration", durationMS: 1_000, resourceCPU: 4, resourceMemoryGiB: 8, observedClass: "medium", peak: true, wantCPU: 4},
		{name: "planned medium oom despite fast duration", durationMS: 1_000, resourceCPU: 4, resourceMemoryGiB: 8, observedClass: "medium", wantCPU: 4},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			observation := shardresource.Observation{
				ShardIdentity: identity,
				Runner:        context.Runner,
				Toolchain:     context.Toolchain,
				ClassID:       testCase.observedClass,
				ObservedAt:    time.Unix(1, 0).UTC(),
				Succeeded:     testCase.peak,
				OOMKilled:     !testCase.peak,
			}
			if testCase.peak {
				// CPU 峰值故意超过固定档位，但内存加余量后仍可由同一 CPU 档覆盖。
				observation.PeakCPUNanoCores = 3_900_000_000
				observation.PeakMemoryBytes = 3 << 30
			} else {
				observation.PeakCPUNanoCores = 1
				observation.PeakMemoryBytes = 1
			}

			selected, err := shardresource.Select(policy, shardresource.Shard{
				Identity: identity,
				Workloads: []shardresource.Workload{{
					ID:                  "workload",
					Kind:                "guard",
					EstimatedDurationMS: testCase.durationMS,
					ResourceCPU:         testCase.resourceCPU,
					ResourceMemoryGiB:   testCase.resourceMemoryGiB,
				}},
			}, context, []shardresource.Observation{observation})
			if err != nil {
				if testCase.peak {
					t.Fatalf("Select() returned an unexpected peak observation error: %v", err)
				}
				// 同 CPU 档没有更大内存时允许 fail-fast，但禁止跨越 CPU 上限。
				return
			}
			if selected.VCPU != testCase.wantCPU {
				t.Fatalf("Select() returned %g vCPU after %s, want the %g vCPU planned-resource ceiling", selected.VCPU, testCase.name, testCase.wantCPU)
			}
		})
	}
}

func remoteCIResourceTierBehaviorGuardPolicy() shardresource.Policy {
	return shardresource.Policy{
		Classes: []shardresource.Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "medium", VCPU: 4, MemoryGiB: 8},
			{ID: "maximum", VCPU: 8, MemoryGiB: 16},
		},
		Bootstrap: shardresource.BootstrapClasses{
			Guard: "small", NodeTest: "small", GoTest: "small",
		},
		CalibrationResource:         shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
		FastWorkloadMaxDurationMS:   cicontract.FastWorkloadResourceDuration.Milliseconds(),
		MediumWorkloadMaxDurationMS: cicontract.MediumWorkloadResourceDuration.Milliseconds(),
		HeadroomPercent:             25,
		MinSamplesToDownsize:        5,
	}
}
