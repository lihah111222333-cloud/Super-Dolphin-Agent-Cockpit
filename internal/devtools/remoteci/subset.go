package remoteci

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// RemoteSubsetRequest identifies the remote workload subset to prepare. Selected
// contains only shardable workloads to execute remotely. Excluded records
// separately selected local or otherwise omitted catalog items, so they cannot
// leak into the remote result projection.
type RemoteSubsetRequest struct {
	Selected []gate.GateID
	Excluded []gate.GateID
}

// PrepareSubset 在任何 job、OSS、临时目录或 ECI 副作用前冻结远程子集，
// 仍先派生完整 catalog 的精确身份，再只查询该有效 scope 的 PASS 并规划其 MISS。
func (coordinator *Coordinator) PrepareSubset(
	ctx context.Context,
	input RunInput,
	request RemoteSubsetRequest,
) (*PreparedRun, error) {
	coordinator.progress.phase(ProgressPhasePrepare, "started")
	if err := validateCoordinatorPrepareInput(ctx, input); err != nil {
		return nil, err
	}
	coordinator.progress.phase(ProgressPhasePrepare, "input_validated")
	plan, catalog, entrypoint, err := buildRemotePlan(input)
	if err != nil {
		return nil, err
	}
	coordinator.progress.phase(ProgressPhasePrepare, "plan_built")
	coordinator.progress.phase(ProgressPhasePrepare, "identity_started")
	input, catalog, catalogDigest, fingerprintSnapshot, err := prepareRemoteWorkloadIdentity(ctx, input, catalog)
	if err != nil {
		return nil, err
	}
	coordinator.progress.phase(ProgressPhasePrepare, "identity_completed")
	scope, executionCatalog, excluded, err := newRemoteSubsetScope(catalog, request)
	if err != nil {
		return nil, err
	}
	coordinator.progress.phase(ProgressPhasePrepare, "scope_built")
	coordinator.progress.phase(ProgressPhasePrepare, "reuse_started")
	reuse, err := prepareRemoteWorkloadReuse(
		ctx,
		input,
		executionCatalog,
		coordinator.config.WorkerTimeout,
		coordinator.config.ResourcePolicy,
		fingerprintSnapshot,
		func(state string) { coordinator.progress.phase(ProgressPhasePrepare, state) },
	)
	if err != nil {
		return nil, err
	}
	coordinator.progress.setCacheCounts(len(reuse.reused), len(reuse.cacheMisses), len(reuse.reused))
	coordinator.progress.phase(ProgressPhasePrepare, "reuse_completed")
	if reuse.allReused() {
		if err := validateAllHitExecutionIdentity(input); err != nil {
			return nil, err
		}
		coordinator.progress.phase(ProgressPhasePrepare, "compile_inputs_skipped")
	} else {
		coordinator.progress.phase(ProgressPhasePrepare, "compile_inputs_started")
		compileInputs, compileErr := remoteCompileGroupInputsForMisses(
			ctx,
			fingerprintSnapshot,
			catalog,
			reuse.cacheMisses,
		)
		if compileErr != nil {
			return nil, compileErr
		}
		input.WorkloadCompileGroupInputs = cloneRemoteCompileGroupInputs(compileInputs)
		coordinator.progress.phase(ProgressPhasePrepare, "compile_inputs_completed")
	}
	return coordinator.freezePreparedRun(input, plan, catalog, executionCatalog, catalogDigest, entrypoint, scope, excluded, reuse)
}

func newRemoteSubsetScope(
	catalog gate.WorkloadCatalog,
	request RemoteSubsetRequest,
) (*gate.RemoteCIExecutionScope, gate.WorkloadCatalog, []gate.GateID, error) {
	if len(request.Selected) == 0 {
		return nil, gate.WorkloadCatalog{}, nil, errors.New("remote CI subset selected workloads are required")
	}
	if err := validateRemoteSubsetExcluded(catalog, request.Selected, request.Excluded); err != nil {
		return nil, gate.WorkloadCatalog{}, nil, err
	}
	canonicalRequest := canonicalizeRemoteSubsetRequest(catalog, request)
	scope, err := gate.NewRemoteCISubsetExecutionScope(catalog, canonicalRequest.Selected)
	if err != nil {
		return nil, gate.WorkloadCatalog{}, nil, fmt.Errorf("construct remote CI subset execution scope: %w", err)
	}
	executionCatalog, err := remoteCatalogForExecutionScope(catalog, scope)
	if err != nil {
		return nil, gate.WorkloadCatalog{}, nil, err
	}
	return &scope, executionCatalog, slices.Clone(canonicalRequest.Excluded), nil
}

// canonicalizeRemoteSubsetRequest projects validated user or scheduler IDs
// through the complete catalog, preserving its canonical order for every
// persisted scope and remote execution projection.
func canonicalizeRemoteSubsetRequest(catalog gate.WorkloadCatalog, request RemoteSubsetRequest) RemoteSubsetRequest {
	selectedSet := remoteSubsetGateIDSet(request.Selected)
	excludedSet := remoteSubsetGateIDSet(request.Excluded)
	canonical := RemoteSubsetRequest{
		Selected: make([]gate.GateID, 0, len(request.Selected)),
		Excluded: make([]gate.GateID, 0, len(request.Excluded)),
	}
	for _, workload := range catalog.Workloads {
		gateID := gate.GateID(workload.ID)
		if _, selected := selectedSet[gateID]; selected {
			canonical.Selected = append(canonical.Selected, gateID)
		}
		if _, excluded := excludedSet[gateID]; excluded {
			canonical.Excluded = append(canonical.Excluded, gateID)
		}
	}
	return canonical
}

func remoteSubsetGateIDSet(ids []gate.GateID) map[gate.GateID]struct{} {
	set := make(map[gate.GateID]struct{}, len(ids))
	for _, gateID := range ids {
		set[gateID] = struct{}{}
	}
	return set
}

func validateRemoteSubsetExcluded(catalog gate.WorkloadCatalog, selected, excluded []gate.GateID) error {
	catalogIDs := make(map[gate.GateID]struct{}, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		catalogIDs[gate.GateID(workload.ID)] = struct{}{}
	}
	selectedIDs, err := validateRemoteSubsetSelected(catalogIDs, selected)
	if err != nil {
		return err
	}
	return validateRemoteSubsetExcludedIDs(catalogIDs, selectedIDs, excluded)
}

func validateRemoteSubsetSelected(catalogIDs map[gate.GateID]struct{}, selected []gate.GateID) (map[gate.GateID]struct{}, error) {
	selectedIDs := make(map[gate.GateID]struct{}, len(selected))
	for _, gateID := range selected {
		if gateID == "" {
			return nil, errors.New("remote CI subset selected workload is empty")
		}
		if _, duplicate := selectedIDs[gateID]; duplicate {
			return nil, fmt.Errorf("remote CI subset selected workload %q is duplicated", gateID)
		}
		if _, known := catalogIDs[gateID]; !known {
			return nil, fmt.Errorf("remote CI subset selected workload %q is outside catalog", gateID)
		}
		selectedIDs[gateID] = struct{}{}
	}
	return selectedIDs, nil
}

// validateRemoteSubsetExcludedIDs 校验排除集合仍属于完整 catalog，且不得重复或覆盖已选 ID。
func validateRemoteSubsetExcludedIDs(catalogIDs, selectedIDs map[gate.GateID]struct{}, excluded []gate.GateID) error {
	excludedIDs := make(map[gate.GateID]struct{}, len(excluded))
	for _, gateID := range excluded {
		if gateID == "" {
			return errors.New("remote CI subset excluded workload is empty")
		}
		if _, known := catalogIDs[gateID]; !known {
			return fmt.Errorf("remote CI subset excluded workload %q is outside catalog", gateID)
		}
		if _, duplicate := excludedIDs[gateID]; duplicate {
			return fmt.Errorf("remote CI subset excluded workload %q is duplicated", gateID)
		}
		if _, overlap := selectedIDs[gateID]; overlap {
			return fmt.Errorf("remote CI subset workload %q is both selected and excluded", gateID)
		}
		excludedIDs[gateID] = struct{}{}
	}
	return nil
}

// remoteCatalogForExecutionScope 只按已验证 scope 从完整 catalog 投影执行项，缺项立即拒绝。
func remoteCatalogForExecutionScope(catalog gate.WorkloadCatalog, scope gate.RemoteCIExecutionScope) (gate.WorkloadCatalog, error) {
	if err := scope.ValidateAgainstCatalog(catalog); err != nil {
		return gate.WorkloadCatalog{}, fmt.Errorf("validate remote CI execution scope: %w", err)
	}
	selected := make(map[gate.GateID]struct{}, len(scope.SelectedGateIDs()))
	for _, gateID := range scope.SelectedGateIDs() {
		selected[gateID] = struct{}{}
	}
	projected := gate.WorkloadCatalog{Version: catalog.Version, Authoritative: catalog.Authoritative}
	for _, workload := range catalog.Workloads {
		if _, ok := selected[gate.GateID(workload.ID)]; ok {
			projected.Workloads = append(projected.Workloads, workload)
		}
	}
	if len(projected.Workloads) != len(selected) {
		return gate.WorkloadCatalog{}, errors.New("remote CI execution scope projection is incomplete")
	}
	return projected, nil
}

// validatePreparedRemoteExecutionScope 复核冻结 scope、excluded 与执行 catalog 未在准备后漂移。
func validatePreparedRemoteExecutionScope(prepared *PreparedRun) error {
	if prepared.scope == nil {
		return errors.New("prepared remote CI execution scope is required")
	}
	if err := prepared.scope.ValidateAgainstCatalog(prepared.catalog); err != nil {
		return fmt.Errorf("validate prepared remote CI execution scope: %w", err)
	}
	if prepared.scope.IsFull() && len(prepared.excluded) != 0 {
		return errors.New("prepared full remote CI execution scope cannot exclude catalog workloads")
	}
	if err := validateRemoteSubsetExcluded(prepared.catalog, prepared.scope.SelectedGateIDs(), prepared.excluded); err != nil {
		return fmt.Errorf("validate prepared remote CI excluded workloads: %w", err)
	}
	expected, err := remoteCatalogForExecutionScope(prepared.catalog, *prepared.scope)
	if err != nil {
		return err
	}
	if !remoteExecutionCatalogsEqual(expected, prepared.executionCatalog) {
		return errors.New("prepared remote CI execution catalog drifted from scope")
	}
	return nil
}

// remoteExecutionCatalogsEqual 按 catalog 元数据和顺序化 workload 内容严格比较执行投影。
func remoteExecutionCatalogsEqual(left, right gate.WorkloadCatalog) bool {
	if left.Version != right.Version || left.Authoritative != right.Authoritative || len(left.Workloads) != len(right.Workloads) {
		return false
	}
	for index := range left.Workloads {
		if left.Workloads[index] != right.Workloads[index] {
			return false
		}
	}
	return true
}
