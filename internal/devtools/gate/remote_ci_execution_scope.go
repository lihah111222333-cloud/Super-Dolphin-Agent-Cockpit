package gate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// RemoteCIExecutionScopeKind identifies whether a run covers the complete
// catalog or an explicitly selected subset of shardable workloads.
type RemoteCIExecutionScopeKind string

const (
	RemoteCIExecutionScopeFull   RemoteCIExecutionScopeKind = "full"
	RemoteCIExecutionScopeSubset RemoteCIExecutionScopeKind = "subset"
)

const remoteCIExecutionScopeDomain = "remote-ci-execution-scope/v1"

// RemoteCIExecutionScope is a validated, catalog-ordered execution scope.
//
// The fields are intentionally private: callers must construct a scope from
// the exact persisted catalog and cannot provide an independently forged JSON
// or digest. A nil *RemoteCIExecutionScope on a run means legacy/full scope.
type RemoteCIExecutionScope struct {
	kind            RemoteCIExecutionScopeKind
	selectedGateIDs []GateID
}

type remoteCIExecutionScopeJSON struct {
	SelectedGateIDs []GateID `json:"selected_gate_ids"`
}

type remoteCIExecutionScopeDigestPayload struct {
	Domain          string   `json:"domain"`
	SelectedGateIDs []GateID `json:"selected_gate_ids"`
}

// NewRemoteCIFullExecutionScope constructs the canonical full scope for a
// catalog. Full scopes are represented in memory for API binding but are not
// persisted in the additive side table.
func NewRemoteCIFullExecutionScope(catalog WorkloadCatalog) (RemoteCIExecutionScope, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return RemoteCIExecutionScope{}, fmt.Errorf("validate full remote CI execution catalog: %w", err)
	}
	selected := make([]GateID, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		selected = append(selected, GateID(workload.ID))
	}
	scope := RemoteCIExecutionScope{kind: RemoteCIExecutionScopeFull, selectedGateIDs: selected}
	if err := scope.validateAgainstCatalog(catalog); err != nil {
		return RemoteCIExecutionScope{}, err
	}
	return scope, nil
}

// NewRemoteCISubsetExecutionScope constructs a canonical subset from a
// catalog. IDs must be a non-empty, duplicate-free subsequence of the
// catalog's shardable workload IDs.
func NewRemoteCISubsetExecutionScope(catalog WorkloadCatalog, selected []GateID) (RemoteCIExecutionScope, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return RemoteCIExecutionScope{}, fmt.Errorf("validate subset remote CI execution catalog: %w", err)
	}
	scope := RemoteCIExecutionScope{kind: RemoteCIExecutionScopeSubset, selectedGateIDs: slices.Clone(selected)}
	if err := scope.validateAgainstCatalog(catalog); err != nil {
		return RemoteCIExecutionScope{}, err
	}
	return scope, nil
}

// SelectedGateIDs returns a defensive copy in canonical catalog order.
func (scope RemoteCIExecutionScope) SelectedGateIDs() []GateID {
	return slices.Clone(scope.selectedGateIDs)
}

// Kind reports whether the validated scope is full or subset.
func (scope RemoteCIExecutionScope) Kind() RemoteCIExecutionScopeKind {
	return scope.kind
}

// IsFull reports whether the scope covers the complete catalog.
func (scope RemoteCIExecutionScope) IsFull() bool {
	return scope.kind == RemoteCIExecutionScopeFull
}

// IsSubset reports whether the scope is an explicit selected subset.
func (scope RemoteCIExecutionScope) IsSubset() bool {
	return scope.kind == RemoteCIExecutionScopeSubset
}

// Validate checks the scope shape independently of a catalog.
func (scope RemoteCIExecutionScope) Validate() error {
	if scope.kind != RemoteCIExecutionScopeFull && scope.kind != RemoteCIExecutionScopeSubset {
		return errors.New("remote CI execution scope kind is invalid")
	}
	if len(scope.selectedGateIDs) == 0 {
		return errors.New("remote CI execution scope selected GateIDs are empty")
	}
	seen := make(map[GateID]struct{}, len(scope.selectedGateIDs))
	for _, gateID := range scope.selectedGateIDs {
		if strings.TrimSpace(string(gateID)) == "" {
			return errors.New("remote CI execution scope selected GateID is empty")
		}
		if _, duplicate := seen[gateID]; duplicate {
			return fmt.Errorf("remote CI execution scope selected GateID %q is duplicated", gateID)
		}
		seen[gateID] = struct{}{}
		if scope.IsSubset() && gateID == GateIDReleaseLayeredCheck {
			return errors.New("remote CI subset execution scope must not include release owner")
		}
	}
	return nil
}

// ValidateAgainstCatalog checks unknown, extra and out-of-order IDs against
// the exact catalog. A subset may only select shardable workload entries;
// full scope must contain every catalog entry in canonical order.
func (scope RemoteCIExecutionScope) ValidateAgainstCatalog(catalog WorkloadCatalog) error {
	return scope.validateAgainstCatalog(catalog)
}

// ProjectRemoteCIExecutionCatalog retains the persisted catalog as the only
// source of truth, then selects the exact full or subset execution coverage.
// Nil is legacy/full and never fabricates a second catalog.
func ProjectRemoteCIExecutionCatalog(catalog WorkloadCatalog, scope *RemoteCIExecutionScope) (WorkloadCatalog, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return WorkloadCatalog{}, fmt.Errorf("validate remote CI execution catalog: %w", err)
	}
	if scope == nil {
		return catalog, nil
	}
	if err := scope.ValidateAgainstCatalog(catalog); err != nil {
		return WorkloadCatalog{}, fmt.Errorf("validate remote CI execution scope against catalog: %w", err)
	}
	if scope.IsFull() {
		return catalog, nil
	}
	selected := make(map[GateID]struct{}, len(scope.selectedGateIDs))
	for _, gateID := range scope.selectedGateIDs {
		selected[gateID] = struct{}{}
	}
	projected := catalog
	projected.Workloads = make([]Workload, 0, len(selected))
	for _, workload := range catalog.Workloads {
		if _, ok := selected[GateID(workload.ID)]; ok {
			projected.Workloads = append(projected.Workloads, workload)
		}
	}
	if len(projected.Workloads) != len(selected) {
		return WorkloadCatalog{}, errors.New("remote CI execution catalog projection is incomplete")
	}
	return projected, nil
}

func (scope RemoteCIExecutionScope) validateAgainstCatalog(catalog WorkloadCatalog) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return fmt.Errorf("validate remote CI execution scope catalog: %w", err)
	}
	if scope.IsFull() {
		return validateRemoteCIFullExecutionScope(scope, catalog)
	}
	return validateRemoteCISubsetExecutionScope(scope, catalog)
}

func validateRemoteCIFullExecutionScope(scope RemoteCIExecutionScope, catalog WorkloadCatalog) error {
	if !slices.Equal(scope.selectedGateIDs, remoteCIExecutionScopeCatalogIDs(catalog)) {
		return errors.New("remote CI full execution scope does not match canonical catalog")
	}
	return nil
}

func validateRemoteCISubsetExecutionScope(scope RemoteCIExecutionScope, catalog WorkloadCatalog) error {
	catalogIDs, workloads := remoteCIExecutionScopeCatalogEntries(catalog)
	if len(scope.selectedGateIDs) == len(catalogIDs) {
		return errors.New("remote CI subset execution scope cannot equal full catalog")
	}
	catalogIndex := make(map[GateID]int, len(catalogIDs))
	for index, gateID := range catalogIDs {
		catalogIndex[gateID] = index
	}
	previousIndex := -1
	for _, gateID := range scope.selectedGateIDs {
		workload, exists := workloads[gateID]
		if !exists {
			return fmt.Errorf("remote CI execution scope selected GateID %q is outside catalog", gateID)
		}
		if !workload.Shardable {
			return fmt.Errorf("remote CI subset execution scope selected non-shardable GateID %q", gateID)
		}
		index := catalogIndex[gateID]
		if index <= previousIndex {
			return fmt.Errorf("remote CI execution scope selected GateID %q is not in canonical catalog order", gateID)
		}
		previousIndex = index
	}
	return nil
}

func remoteCIExecutionScopeCatalogIDs(catalog WorkloadCatalog) []GateID {
	ids := make([]GateID, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		ids = append(ids, GateID(workload.ID))
	}
	return ids
}

func remoteCIExecutionScopeCatalogEntries(catalog WorkloadCatalog) ([]GateID, map[GateID]Workload) {
	ids := remoteCIExecutionScopeCatalogIDs(catalog)
	workloads := make(map[GateID]Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		workloads[GateID(workload.ID)] = workload
	}
	return ids, workloads
}

// CanonicalJSON returns the strict side-table payload for a subset scope.
func (scope RemoteCIExecutionScope) CanonicalJSON() (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	if !scope.IsSubset() {
		return "", errors.New("only subset remote CI execution scopes have a persisted JSON payload")
	}
	payload, err := json.Marshal(remoteCIExecutionScopeJSON{SelectedGateIDs: scope.selectedGateIDs})
	if err != nil {
		return "", fmt.Errorf("encode remote CI execution scope: %w", err)
	}
	return string(payload), nil
}

// Digest returns the domain-separated digest of the canonical ordered IDs.
func (scope RemoteCIExecutionScope) Digest() (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(remoteCIExecutionScopeDigestPayload{Domain: remoteCIExecutionScopeDomain, SelectedGateIDs: scope.selectedGateIDs})
	if err != nil {
		return "", fmt.Errorf("encode remote CI execution scope digest payload: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// decodeRemoteCIExecutionScope decodes one strict side-table subset payload.
func decodeRemoteCIExecutionScope(scopeJSON, scopeDigest string) (RemoteCIExecutionScope, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(scopeJSON)))
	decoder.DisallowUnknownFields()
	var payload remoteCIExecutionScopeJSON
	if err := decoder.Decode(&payload); err != nil {
		return RemoteCIExecutionScope{}, fmt.Errorf("decode remote CI execution scope: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return RemoteCIExecutionScope{}, errors.New("remote CI execution scope JSON has trailing values")
	} else if !errors.Is(err, io.EOF) {
		return RemoteCIExecutionScope{}, fmt.Errorf("decode trailing remote CI execution scope JSON: %w", err)
	}
	scope := RemoteCIExecutionScope{kind: RemoteCIExecutionScopeSubset, selectedGateIDs: payload.SelectedGateIDs}
	if err := scope.Validate(); err != nil {
		return RemoteCIExecutionScope{}, err
	}
	canonicalJSON, err := scope.CanonicalJSON()
	if err != nil {
		return RemoteCIExecutionScope{}, err
	}
	if scopeJSON != canonicalJSON {
		return RemoteCIExecutionScope{}, errors.New("remote CI execution scope JSON is not canonical")
	}
	digest, err := scope.Digest()
	if err != nil {
		return RemoteCIExecutionScope{}, err
	}
	if scopeDigest != digest {
		return RemoteCIExecutionScope{}, errors.New("remote CI execution scope digest does not match content")
	}
	return scope, nil
}

func remoteCIExecutionScopesEqual(left, right *RemoteCIExecutionScope) bool {
	if left == nil || left.IsFull() {
		return right == nil || right.IsFull()
	}
	if right == nil || right.IsFull() {
		return false
	}
	return left.kind == right.kind && slices.Equal(left.selectedGateIDs, right.selectedGateIDs)
}
