package gate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// TestRequiredChecksForWorkloadCatalogScopesByActualCatalog 锁定快速目录只生成实际范围，
// release 目录仍精确生成完整六项检查。
func TestRequiredChecksForWorkloadCatalogScopesByActualCatalog(t *testing.T) {
	fast := checkScopeTestCatalog(GateIDWhitespaceCheck, GateIDBackendTestWithGuard, GateIDFrontendTest)
	assertRequiredCheckScope(t, fast, []cicontract.RequiredCheck{
		cicontract.RequiredCheckGate, cicontract.RequiredCheckNormal, cicontract.RequiredCheckFrontend,
	})
	release := checkScopeTestCatalog(
		GateIDWhitespaceCheck, GateIDBackendTestWithGuard, GateIDFrontendE2E,
		GateIDBackendTestGuardWithRace, GateIDFrontendTest, GateIDFrontendPreflight,
	)
	assertRequiredCheckScope(t, release, cicontract.RequiredChecks())
}

func checkScopeTestCatalog(ids ...GateID) WorkloadCatalog {
	workloads := make([]Workload, 0, len(ids))
	for _, id := range ids {
		workloads = append(workloads, Workload{ID: string(id), Kind: WorkloadKindGuard, CommandDigest: strings.Repeat("a", 64), BootstrapEstimateMS: 1, Shardable: true})
	}
	return WorkloadCatalog{Version: durationLedgerVersion, Authoritative: true, Workloads: workloads}
}

func assertRequiredCheckScope(t *testing.T, catalog WorkloadCatalog, want []cicontract.RequiredCheck) {
	t.Helper()
	got, err := RequiredChecksForWorkloadCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required checks = %v, want %v", got, want)
	}
}
