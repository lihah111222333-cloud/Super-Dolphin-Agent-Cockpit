package remoteci

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// TestRemoteResourcePlanFieldContract 锁定计划资源字段及其到 ECI 选择器的完整映射。
func TestRemoteResourcePlanFieldContract(t *testing.T) {
	t.Parallel()
	planType := reflect.TypeFor[gate.PlannedWorkload]()
	selectorType := reflect.TypeFor[shardresource.Workload]()
	for _, fieldName := range []string{"ResourceCPU", "ResourceMemoryGiB"} {
		planField, ok := planType.FieldByName(fieldName)
		if !ok || planField.Tag.Get("json") == "" {
			t.Fatalf("planned workload must persist %s with a JSON identity", fieldName)
		}
		if _, ok := selectorType.FieldByName(fieldName); !ok {
			t.Fatalf("resource selector workload must consume planned field %s", fieldName)
		}
	}

	policy := shardresource.Policy{
		Classes: []shardresource.Class{
			{ID: "small", VCPU: 2, MemoryGiB: 4},
			{ID: "medium", VCPU: 4, MemoryGiB: 8},
			{ID: "maximum", VCPU: 8, MemoryGiB: 16},
		},
		Bootstrap: shardresource.BootstrapClasses{
			Guard: "small", NodeTest: "small", GoTest: "small",
		},
		CalibrationResource:         shardresource.Class{ID: "calibration", VCPU: 4, MemoryGiB: 8},
		FastWorkloadMaxDurationMS:   5_000,
		MediumWorkloadMaxDurationMS: 70_000,
		HeadroomPercent:             25,
		MinSamplesToDownsize:        5,
	}
	planned := gate.PlannedWorkload{
		Workload:            gate.Workload{ID: "guard:heavy", Kind: gate.WorkloadKindGuard},
		EstimatedDurationMS: 90_000,
		ResourceCPU:         2, ResourceMemoryGiB: 4,
	}
	selected, err := selectRemoteShardResources(
		policy,
		nil,
		map[string]gate.PlannedWorkload{planned.Workload.ID: planned},
		gate.ContainerShard{IdentityDigest: "sha256:" + strings.Repeat("a", 64), GateIDs: []gate.GateID{gate.GateID(planned.Workload.ID)}},
		shardresource.Context{Runner: "sha256:" + strings.Repeat("b", 64), Toolchain: "sha256:" + strings.Repeat("c", 64)},
	)
	if err != nil {
		t.Fatalf("select remote shard resources: %v", err)
	}
	if selected.VCPU != 2 || selected.MemoryGiB != 4 {
		t.Fatalf("90s bootstrap workload must retain persisted 2C/4GiB identity, got %+v", selected)
	}
}
