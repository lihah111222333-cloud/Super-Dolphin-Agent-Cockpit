package remoteportable

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// PortableWorkloadCatalogDigestDomain separates the exact-tree workload catalog identity.
const PortableWorkloadCatalogDigestDomain = "remote-ci-portable-workload-catalog/v1"

// PortableWorkloadCatalogFacts 是由 exact-tree workload catalog 派生的 typed proof。
type PortableWorkloadCatalogFacts struct {
	CatalogDigest        string `json:"catalog_digest"`
	WorkloadCount        int    `json:"workload_count"`
	LinuxWorkloadCount   int    `json:"linux_workload_count"`
	WindowsWorkloadCount int    `json:"windows_workload_count"`
}

type portableWorkloadCatalogMaterial struct {
	WorkloadCount        int                        `json:"workload_count"`
	LinuxWorkloadCount   int                        `json:"linux_workload_count"`
	WindowsWorkloadCount int                        `json:"windows_workload_count"`
	Workloads            []PortableManifestWorkload `json:"workloads"`
}

type portableCatalogWorkload struct {
	workload PortableManifestWorkload
	encoded  []byte
}

// DerivePortableWorkloadCatalogFacts 对全部 exact-tree workloads 做 canonical 排序、计数和 domain digest。
func DerivePortableWorkloadCatalogFacts(workloads []PortableManifestWorkload) (PortableWorkloadCatalogFacts, error) {
	entries := make([]portableCatalogWorkload, 0, len(workloads))
	linuxCount, windowsCount := 0, 0
	for index, workload := range workloads {
		if err := validatePortableManifestWorkload(workload); err != nil {
			return PortableWorkloadCatalogFacts{}, fmt.Errorf("validate workload catalog item %d: %w", index, err)
		}
		encoded, err := json.Marshal(workload)
		if err != nil {
			return PortableWorkloadCatalogFacts{}, fmt.Errorf("encode workload catalog item %d: %w", index, err)
		}
		switch workload.TargetPlatform {
		case string(RepositoryWorkloadPlatformLinuxAMD64):
			linuxCount++
		case string(RepositoryWorkloadPlatformWindowsAMD64):
			windowsCount++
		default:
			return PortableWorkloadCatalogFacts{}, errors.New("workload catalog platform is unsupported")
		}
		entries = append(entries, portableCatalogWorkload{workload: workload, encoded: encoded})
	}
	slices.SortFunc(entries, func(left, right portableCatalogWorkload) int {
		return bytes.Compare(left.encoded, right.encoded)
	})
	sorted := make([]PortableManifestWorkload, 0, len(entries))
	for index, entry := range entries {
		if index > 0 && sorted[index-1].UnitID == entry.workload.UnitID {
			return PortableWorkloadCatalogFacts{}, fmt.Errorf("workload catalog unit %q is duplicated", entry.workload.UnitID)
		}
		sorted = append(sorted, entry.workload)
	}
	material := portableWorkloadCatalogMaterial{
		WorkloadCount: len(sorted), LinuxWorkloadCount: linuxCount, WindowsWorkloadCount: windowsCount,
		Workloads: sorted,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return PortableWorkloadCatalogFacts{}, fmt.Errorf("encode workload catalog: %w", err)
	}
	return PortableWorkloadCatalogFacts{
		CatalogDigest: portableFramedDigest(PortableWorkloadCatalogDigestDomain, encoded),
		WorkloadCount: len(sorted), LinuxWorkloadCount: linuxCount, WindowsWorkloadCount: windowsCount,
	}, nil
}

// ValidatePortableWorkloadCatalogFacts 校验 result 中 catalog proof 的内部计数不变量。
func ValidatePortableWorkloadCatalogFacts(facts PortableWorkloadCatalogFacts) error {
	if !validPortableDigest(facts.CatalogDigest) || facts.WorkloadCount < 0 || facts.LinuxWorkloadCount < 0 || facts.WindowsWorkloadCount < 0 {
		return errors.New("portable workload catalog facts are invalid")
	}
	if facts.LinuxWorkloadCount+facts.WindowsWorkloadCount != facts.WorkloadCount {
		return errors.New("portable workload catalog platform counts do not match total")
	}
	return nil
}

// ValidatePortableWorkloadCatalogProof 比对 validation result 与 exact-tree catalog 的完整 proof。
func ValidatePortableWorkloadCatalogProof(result PortableValidationResult, workloads []PortableManifestWorkload) error {
	facts, err := DerivePortableWorkloadCatalogFacts(workloads)
	if err != nil {
		return err
	}
	if result.CatalogDigest != facts.CatalogDigest || result.WorkloadCount != facts.WorkloadCount || result.LinuxWorkloadCount != facts.LinuxWorkloadCount || result.WindowsWorkloadCount != facts.WindowsWorkloadCount {
		return errors.New("portable validation result workload catalog proof does not match exact tree")
	}
	return nil
}
