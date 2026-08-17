package gate

import (
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestResourceMappersPreserveCanonicalIdentityBehavior(t *testing.T) {
	t.Run("planned workload tier", func(t *testing.T) {
		tier, err := plannedWorkloadResourceTier(PlannedWorkload{ResourceCPU: 2, ResourceMemoryGiB: 4})
		if err != nil || tier != cicontract.WorkloadResourceTierFast {
			t.Fatalf("plannedWorkloadResourceTier() = (%d, %v), want (fast, nil)", tier, err)
		}
	})
	t.Run("tuple tier", func(t *testing.T) {
		tier, err := normalResourceTierForTuple(4, 8)
		if err != nil || tier != cicontract.WorkloadResourceTierMedium {
			t.Fatalf("normalResourceTierForTuple() = (%d, %v), want (medium, nil)", tier, err)
		}
	})
	t.Run("reserve identity", func(t *testing.T) {
		tier, err := resourceTierForReserve(RemoteCIShardResources{ClassID: "maximum", CPU: 8, MemoryGiB: 16})
		if err != nil || tier != cicontract.WorkloadResourceTierSlow {
			t.Fatalf("resourceTierForReserve() = (%d, %v), want (slow, nil)", tier, err)
		}
	})
	t.Run("compile class validation", func(t *testing.T) {
		for _, classID := range []string{"small", "medium", "maximum"} {
			if err := validateCompileGroupResourceClass(classID); err != nil {
				t.Fatalf("validateCompileGroupResourceClass(%q): %v", classID, err)
			}
		}
		if err := validateCompileGroupResourceClass("2"); err == nil {
			t.Fatal("validateCompileGroupResourceClass accepted non-canonical class")
		}
	})
}
