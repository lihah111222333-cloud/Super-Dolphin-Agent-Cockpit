package main

import (
	"fmt"
	"slices"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteRunContractExecutionCatalog projects the catalog which the receipt must
// cover. A nil scope retains the legacy/full SQLite representation; an explicit
// subset must be valid against the complete persisted catalog.
func remoteRunContractExecutionCatalog(catalog gatecontract.WorkloadCatalog, scope *gatecontract.RemoteCIExecutionScope) (gatecontract.WorkloadCatalog, error) {
	return gatecontract.ProjectRemoteCIExecutionCatalog(catalog, scope)
}

// validateRemoteRunRecordedExecutionScope binds a persisted subset proof to the
// result. Full scope intentionally has no side-table row.
func validateRemoteRunRecordedExecutionScope(catalog gatecontract.WorkloadCatalog, recorded, result *gatecontract.RemoteCIExecutionScope) error {
	if result == nil {
		if recorded != nil {
			return fmt.Errorf("remote CI full receipt has an unexpected persisted execution scope")
		}
		return nil
	}
	if err := result.ValidateAgainstCatalog(catalog); err != nil {
		return fmt.Errorf("validate remote CI result execution scope: %w", err)
	}
	if result.IsFull() {
		if recorded != nil {
			return fmt.Errorf("remote CI full receipt has a persisted execution scope")
		}
		return nil
	}
	if recorded == nil {
		return fmt.Errorf("remote CI subset receipt execution scope is missing")
	}
	if err := recorded.ValidateAgainstCatalog(catalog); err != nil {
		return fmt.Errorf("validate recorded remote CI execution scope: %w", err)
	}
	if !recorded.IsSubset() || !slices.Equal(recorded.SelectedGateIDs(), result.SelectedGateIDs()) {
		return fmt.Errorf("recorded remote CI execution scope does not match result")
	}
	return nil
}

// validateRemoteRunOwnerExecutionSet keeps the legacy release-owner shape for
// nil/full scopes. A subset instead carries only its shardable workloads'
// canonical parent aggregates, in full-catalog parent order.
func validateRemoteRunOwnerExecutionSet(
	catalog gatecontract.WorkloadCatalog,
	scope *gatecontract.RemoteCIExecutionScope,
	executions []gatecontract.PlanGateExecution,
) error {
	if scope == nil || scope.IsFull() {
		return nil
	}
	if !scope.IsSubset() {
		return fmt.Errorf("remote CI execution scope kind is unsupported")
	}
	if err := scope.ValidateAgainstCatalog(catalog); err != nil {
		return fmt.Errorf("validate remote CI subset execution scope: %w", err)
	}
	expected, err := remoteRunSubsetParentGateIDs(catalog, scope)
	if err != nil {
		return err
	}
	if len(executions) != len(expected) {
		return fmt.Errorf("remote CI subset parent aggregate count = %d, want %d", len(executions), len(expected))
	}
	for index, execution := range executions {
		if execution.GateID != expected[index] {
			return fmt.Errorf("remote CI subset parent aggregate %d = %q, want %q", index, execution.GateID, expected[index])
		}
	}
	return nil
}

// remoteRunSubsetParentGateIDs derives the unique parent gates in the complete
// catalog's canonical order, rather than in request or result order.
func remoteRunSubsetParentGateIDs(
	catalog gatecontract.WorkloadCatalog,
	scope *gatecontract.RemoteCIExecutionScope,
) ([]gatecontract.GateID, error) {
	selected := make(map[gatecontract.GateID]struct{}, len(scope.SelectedGateIDs()))
	for _, workloadID := range scope.SelectedGateIDs() {
		selected[workloadID] = struct{}{}
	}
	parents := make([]gatecontract.GateID, 0, len(selected))
	seen := make(map[gatecontract.GateID]struct{}, len(selected))
	for _, workload := range catalog.Workloads {
		if !workload.Shardable {
			continue
		}
		if _, selected := selected[gatecontract.GateID(workload.ID)]; !selected {
			continue
		}
		parent, err := gatecontract.WorkloadParentGateID(workload.ID)
		if err != nil {
			return nil, fmt.Errorf("parse selected remote CI workload %q parent: %w", workload.ID, err)
		}
		if _, duplicate := seen[parent]; duplicate {
			continue
		}
		seen[parent] = struct{}{}
		parents = append(parents, parent)
	}
	if len(parents) != len(seen) {
		return nil, fmt.Errorf("remote CI subset parent aggregate projection is inconsistent")
	}
	return parents, nil
}
