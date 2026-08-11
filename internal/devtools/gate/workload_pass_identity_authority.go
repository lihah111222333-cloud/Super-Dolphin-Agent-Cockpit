package gate

import (
	"crypto/sha256"
	"fmt"
)

// WorkloadPassExecutionDigest 返回 catalog workload 的唯一执行语义摘要。
func WorkloadPassExecutionDigest(workload Workload) string {
	sum := sha256.Sum256([]byte("remote-workload-execution/v1\x00" + workload.ID + "\x00" + workload.CommandDigest))
	return fmt.Sprintf("sha256:%x", sum)
}

// validateSQLiteRemoteCIRunWorkloadPassIdentities 验证 finalization 的冻结身份集。
func validateSQLiteRemoteCIRunWorkloadPassIdentities(
	catalog WorkloadCatalog,
	persisted []RemoteCIWorkloadResult,
	supplied []WorkloadPassIdentity,
) (map[GateID]WorkloadPassIdentity, error) {
	if len(persisted) != len(supplied) {
		return nil, fmt.Errorf("remote CI authority workload identity count mismatch: persisted=%d supplied=%d", len(persisted), len(supplied))
	}
	verified, err := validateSuppliedWorkloadPassIdentities(catalog, supplied)
	if err != nil {
		return nil, err
	}
	if err := validatePersistedWorkloadPassIdentities(persisted, verified); err != nil {
		return nil, err
	}
	return verified, nil
}

// validateSuppliedWorkloadPassIdentities 校验调用方冻结身份并建立 workload 索引。
func validateSuppliedWorkloadPassIdentities(catalog WorkloadCatalog, supplied []WorkloadPassIdentity) (map[GateID]WorkloadPassIdentity, error) {
	catalogByID := indexCanonicalWorkloadPassCatalog(catalog)
	verified := make(map[GateID]WorkloadPassIdentity, len(supplied))
	for _, identity := range supplied {
		if _, duplicate := verified[identity.WorkloadID]; duplicate {
			return nil, fmt.Errorf("remote CI authority workload identity %q is duplicated", identity.WorkloadID)
		}
		if err := validateCanonicalWorkloadPassIdentity(identity, catalogByID); err != nil {
			return nil, err
		}
		verified[identity.WorkloadID] = identity
	}
	return verified, nil
}

// indexCanonicalWorkloadPassCatalog 建立 canonical catalog 的 workload 索引。
func indexCanonicalWorkloadPassCatalog(catalog WorkloadCatalog) map[GateID]Workload {
	indexed := make(map[GateID]Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		indexed[GateID(workload.ID)] = workload
	}
	return indexed
}

// validateCanonicalWorkloadPassIdentity 校验单个冻结身份的 catalog 命令和输入绑定。
func validateCanonicalWorkloadPassIdentity(identity WorkloadPassIdentity, catalog map[GateID]Workload) error {
	workload, ok := catalog[identity.WorkloadID]
	if !ok || !workload.Shardable {
		return fmt.Errorf("remote CI authority workload identity %q is absent from its shardable catalog", identity.WorkloadID)
	}
	if err := identity.Validate(); err != nil {
		return fmt.Errorf("remote CI authority workload identity %q is invalid: %w", identity.WorkloadID, err)
	}
	if expected := WorkloadPassExecutionDigest(workload); identity.ExecutionDigest != expected {
		return fmt.Errorf("remote CI authority workload %q execution digest does not match canonical catalog command", identity.WorkloadID)
	}
	if identity.InputDigest != workload.InputDigest {
		return fmt.Errorf("remote CI authority workload %q input digest does not match canonical catalog input", identity.WorkloadID)
	}
	return nil
}

// validatePersistedWorkloadPassIdentities 校验 SQLite 投影与冻结身份逐项一致。
func validatePersistedWorkloadPassIdentities(persisted []RemoteCIWorkloadResult, verified map[GateID]WorkloadPassIdentity) error {
	for _, result := range persisted {
		identity, ok := verified[result.Identity.WorkloadID]
		if !ok || identity != result.Identity {
			return fmt.Errorf("persisted remote CI workload result %q does not match frozen authority identity", result.Identity.WorkloadID)
		}
	}
	return nil
}
