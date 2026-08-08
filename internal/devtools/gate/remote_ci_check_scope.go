package gate

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// RequiredCheckForWorkloadID 将规范 workload（含展开目标）映射到唯一检查分类。
func RequiredCheckForWorkloadID(workloadID string) (cicontract.RequiredCheck, error) {
	gateID, _, _, _, err := ParseWorkloadID(workloadID)
	if err != nil {
		return "", err
	}
	switch gateID {
	case GateIDFrontendE2E:
		return cicontract.RequiredCheckE2E, nil
	case GateIDBackendTestGuardWithRace:
		return cicontract.RequiredCheckRace, nil
	case GateIDBackendTestWithGuard:
		return cicontract.RequiredCheckNormal, nil
	case GateIDFrontendPreflight:
		return cicontract.RequiredCheckDependency, nil
	case GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendFullTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify:
		return cicontract.RequiredCheckFrontend, nil
	case GateIDAIMaintenanceSelfTest, GateIDLSPChangedDiagnostics, GateIDBackendNilness, GateIDSQLCVerify,
		GateIDCodemapCheck, GateIDProjectMapCheck, GateIDCapabilityContractCheck, GateIDWhitespaceCheck, GateIDReleaseLayeredCheck:
		return cicontract.RequiredCheckGate, nil
	default:
		return "", fmt.Errorf("remote CI gate %q has no required-check classification", gateID)
	}
}

// RequiredChecksForWorkloadCatalog 返回目录实际计划覆盖的 canonical 检查子集。
func RequiredChecksForWorkloadCatalog(catalog WorkloadCatalog) ([]cicontract.RequiredCheck, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return nil, fmt.Errorf("validate required-check workload catalog: %w", err)
	}
	seen := make(map[cicontract.RequiredCheck]struct{}, len(cicontract.RequiredChecks()))
	for _, workload := range catalog.Workloads {
		check, err := RequiredCheckForWorkloadID(workload.ID)
		if err != nil {
			return nil, fmt.Errorf("classify workload %q: %w", workload.ID, err)
		}
		seen[check] = struct{}{}
	}
	required := make([]cicontract.RequiredCheck, 0, len(seen))
	for _, check := range cicontract.RequiredChecks() {
		if _, exists := seen[check]; exists {
			required = append(required, check)
		}
	}
	if len(required) == 0 {
		return nil, fmt.Errorf("workload catalog has no required-check coverage")
	}
	return required, nil
}

// validateSQLiteWorkloadCatalogPassingCheckReceipts 回读 exact catalog 后校验回执范围。
func validateSQLiteWorkloadCatalogPassingCheckReceipts(queryer sqliteRowQueryer, catalogDigest string, receipts []CheckReceiptRecord) error {
	catalog, err := loadSQLiteWorkloadCatalog(queryer, catalogDigest)
	if err != nil {
		return err
	}
	return validateWorkloadCatalogPassingCheckReceipts(catalog.Catalog, receipts)
}
