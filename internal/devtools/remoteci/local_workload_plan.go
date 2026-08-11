package remoteci

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// LocalWorkloadPlanInput supplies only the immutable proofs required to build
// canonical local workload identities. Callers cannot supply resource,
// environment, execution, or PASS identity overrides.
type LocalWorkloadPlanInput struct {
	RepositoryRoot  string
	ExactTreeSHA    string
	SelectedGateIDs []gate.GateID
	Receipt         gate.LocalExecutorSessionReceipt
}

// LocalWorkloadInputProof is the canonical input material for one selected
// workload. It is an audit projection, not a caller-provided identity input.
type LocalWorkloadInputProof struct {
	WorkloadID        gate.GateID
	InputDigest       string
	ExecutionDigest   string
	EnvironmentDigest string
}

// LocalWorkloadPlan is the narrow, callback-free handoff from the canonical
// producer to the local scheduler. It contains no cloud, token, or executor
// callbacks.
type LocalWorkloadPlan struct {
	GatePlan        gate.GatePlan
	Catalog         gate.WorkloadCatalog
	CatalogDigest   string
	SelectedGateIDs []gate.GateID
	Items           []gate.LocalWorkloadScheduleItem
	InputProofs     []LocalWorkloadInputProof
	ReceiptDigest   string
}

// BuildLocalWorkloadPlan 构造 scheduler 唯一可接受的 local PASS identity 输入。
// 精确 Git tree 只用于派生 workload input digest，仓库路径、tree 与 commit 均不得进入 key。
func BuildLocalWorkloadPlan(ctx context.Context, input LocalWorkloadPlanInput) (LocalWorkloadPlan, error) {
	if ctx == nil {
		return LocalWorkloadPlan{}, errors.New("local workload plan context is required")
	}
	if strings.TrimSpace(input.RepositoryRoot) == "" {
		return LocalWorkloadPlan{}, errors.New("local workload plan repository root is required")
	}
	plan, catalog, err := canonicalLocalWorkloadCatalog(input.ExactTreeSHA)
	if err != nil {
		return LocalWorkloadPlan{}, err
	}
	if err := validateLocalWorkloadPlanSelection(catalog, input.SelectedGateIDs); err != nil {
		return LocalWorkloadPlan{}, err
	}
	if err := validateLocalWorkloadPlanReceipt(ctx, input.Receipt, input.RepositoryRoot, input.ExactTreeSHA); err != nil {
		return LocalWorkloadPlan{}, err
	}
	catalog, err = bindCanonicalLocalWorkloadInputDigests(ctx, input.RepositoryRoot, input.ExactTreeSHA, catalog, input.Receipt)
	if err != nil {
		return LocalWorkloadPlan{}, err
	}
	return finalizeLocalWorkloadPlan(plan, catalog, input)
}

// finalizeLocalWorkloadPlan 将受回执约束的 item 与 digest 固化为 scheduler handoff。
func finalizeLocalWorkloadPlan(plan gate.GatePlan, catalog gate.WorkloadCatalog, input LocalWorkloadPlanInput) (LocalWorkloadPlan, error) {
	projections, err := gate.ProjectLocalBootstrapWorkloadPlanning(catalog, input.SelectedGateIDs)
	if err != nil {
		return LocalWorkloadPlan{}, fmt.Errorf("project canonical local bootstrap planning: %w", err)
	}
	catalogDigest, err := gate.WorkloadCatalogDigest(catalog)
	if err != nil {
		return LocalWorkloadPlan{}, fmt.Errorf("digest canonical local workload catalog: %w", err)
	}
	items, proofs, err := buildLocalWorkloadPlanItems(catalog, projections, input.Receipt)
	if err != nil {
		return LocalWorkloadPlan{}, err
	}
	receiptDigest := ""
	if input.Receipt != nil {
		var err error
		receiptDigest, err = input.Receipt.Digest()
		if err != nil {
			return LocalWorkloadPlan{}, fmt.Errorf("local workload plan executor receipt digest: %w", err)
		}
	}
	return LocalWorkloadPlan{
		GatePlan: plan, Catalog: catalog, CatalogDigest: catalogDigest,
		SelectedGateIDs: append([]gate.GateID(nil), input.SelectedGateIDs...),
		Items:           items, InputProofs: proofs, ReceiptDigest: receiptDigest,
	}, nil
}

func canonicalLocalWorkloadCatalog(tree string) (gate.GatePlan, gate.WorkloadCatalog, error) {
	source, err := localWorkloadPlanSource(tree)
	if err != nil {
		return gate.GatePlan{}, gate.WorkloadCatalog{}, err
	}
	plan, err := gate.BuildGatePlan(gate.ProfileLocalFast, source)
	if err != nil {
		return gate.GatePlan{}, gate.WorkloadCatalog{}, fmt.Errorf("build canonical local gate plan: %w", err)
	}
	catalog, err := gate.BuildWorkloadCatalog(plan, gate.DefaultWorkloadBootstrapPolicy())
	if err != nil {
		return gate.GatePlan{}, gate.WorkloadCatalog{}, fmt.Errorf("build canonical local workload catalog: %w", err)
	}
	return plan, catalog, nil
}

func bindCanonicalLocalWorkloadInputDigests(ctx context.Context, repositoryRoot, tree string, catalog gate.WorkloadCatalog, receipt gate.LocalExecutorSessionReceipt) (gate.WorkloadCatalog, error) {
	authority := gate.CandidateObjectAuthority{}
	if receipt != nil {
		var err error
		authority, err = gate.LocalExecutorSessionReceiptCandidateObjectAuthority(receipt)
		if err != nil {
			return gate.WorkloadCatalog{}, fmt.Errorf("verify local workload plan receipt candidate object authority: %w", err)
		}
	}
	inputDigests, _, _, err := remoteWorkloadFingerprintsWithSnapshotWithCandidateObjectAuthority(ctx, repositoryRoot, tree, authority, remoteShardableWorkloads(catalog))
	if err != nil {
		return gate.WorkloadCatalog{}, fmt.Errorf("fingerprint canonical local workloads: %w", err)
	}
	bound, err := bindRemoteWorkloadInputDigests(catalog, inputDigests)
	if err != nil {
		return gate.WorkloadCatalog{}, fmt.Errorf("bind canonical local workload input digests: %w", err)
	}
	return bound, nil
}

func validateLocalWorkloadPlanReceipt(ctx context.Context, receipt gate.LocalExecutorSessionReceipt, repositoryRoot, exactTreeSHA string) error {
	if receipt == nil {
		return nil
	}
	if err := gate.ValidateLocalExecutorSessionReceipt(receipt); err != nil {
		return fmt.Errorf("local workload plan executor receipt is invalid: %w", err)
	}
	if _, err := receipt.Digest(); err != nil {
		return fmt.Errorf("local workload plan executor receipt is invalid: %w", err)
	}
	if err := gate.ReverifyLocalExecutorSessionReceiptForLookup(ctx, receipt, repositoryRoot, exactTreeSHA); err != nil {
		return fmt.Errorf("reverify local workload plan executor receipt for lookup: %w", err)
	}
	return nil
}

func localWorkloadPlanSource(tree string) (gate.SourceSpec, error) {
	objectFormat := gate.GitObjectFormat("")
	switch len(tree) {
	case 40:
		objectFormat = gate.GitObjectFormatSHA1
	case 64:
		objectFormat = gate.GitObjectFormatSHA256
	default:
		return gate.SourceSpec{}, errors.New("local workload plan exact tree SHA is invalid")
	}
	source := gate.SourceSpec{
		Kind: gate.SourceKindTree, ObjectFormat: objectFormat,
		Tree: &gate.TreeSource{SHA: tree}, SourceTreeSHA: tree,
	}
	if err := source.Validate(); err != nil {
		return gate.SourceSpec{}, fmt.Errorf("local workload plan exact tree: %w", err)
	}
	return source, nil
}

// validateLocalWorkloadPlanSelection 校验选择集合非空、无重复且均为 catalog 内可分片 workload。
func validateLocalWorkloadPlanSelection(catalog gate.WorkloadCatalog, selected []gate.GateID) error {
	if len(selected) == 0 {
		return errors.New("local workload plan selection is empty")
	}
	known := make(map[gate.GateID]gate.Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		known[gate.GateID(workload.ID)] = workload
	}
	seen := make(map[gate.GateID]struct{}, len(selected))
	for index, id := range selected {
		if strings.TrimSpace(string(id)) == "" {
			return fmt.Errorf("local workload plan selection[%d] is empty", index)
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("local workload plan selection contains duplicate %q", id)
		}
		workload, ok := known[id]
		if !ok {
			return fmt.Errorf("local workload plan selection contains unknown %q", id)
		}
		if !workload.Shardable {
			return fmt.Errorf("local workload plan selection contains non-shardable %q", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func buildLocalWorkloadPlanItems(catalog gate.WorkloadCatalog, projections []gate.LocalWorkloadPlanningProjection, receipt gate.LocalExecutorSessionReceipt) ([]gate.LocalWorkloadScheduleItem, []LocalWorkloadInputProof, error) {
	workloads := make(map[gate.GateID]gate.Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		workloads[gate.GateID(workload.ID)] = workload
	}
	items := make([]gate.LocalWorkloadScheduleItem, 0, len(projections))
	proofs := make([]LocalWorkloadInputProof, 0, len(projections))
	for _, projection := range projections {
		item, proof, err := buildLocalWorkloadPlanItem(workloads, projection, receipt)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, item)
		if proof != (LocalWorkloadInputProof{}) {
			proofs = append(proofs, proof)
		}
	}
	return items, proofs, nil
}

// buildLocalWorkloadPlanItem 仅为 receipt 显式覆盖的 workload 构造本地 identity。
func buildLocalWorkloadPlanItem(workloads map[gate.GateID]gate.Workload, projection gate.LocalWorkloadPlanningProjection, receipt gate.LocalExecutorSessionReceipt) (gate.LocalWorkloadScheduleItem, LocalWorkloadInputProof, error) {
	workload, ok := workloads[projection.WorkloadID]
	if !ok {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, fmt.Errorf("canonical local workload %q is missing from catalog", projection.WorkloadID)
	}
	eligibility, err := gate.EvaluateLocalWorkloadExecutionEligibility(projection.WorkloadID)
	if err != nil {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, fmt.Errorf("local workload %q eligibility: %w", projection.WorkloadID, err)
	}
	receiptCovered, err := localWorkloadPlanReceiptCoversItem(receipt, projection.WorkloadID, eligibility)
	if err != nil {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, err
	}
	if !receiptCovered {
		return directRemoteLocalWorkloadPlanItem(projection), LocalWorkloadInputProof{}, nil
	}
	executionDigest, err := localWorkloadPlanExecutionDigest(workload)
	if err != nil {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, err
	}
	environment, err := receipt.Environment(projection.WorkloadID)
	if err != nil {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, fmt.Errorf("local workload %q receipt environment: %w", projection.WorkloadID, err)
	}
	environmentDigest, err := gate.LocalWorkloadPassEnvironmentDigest(environment)
	if err != nil {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, fmt.Errorf("local workload %q receipt environment digest: %w", projection.WorkloadID, err)
	}
	identity, err := localWorkloadPlanIdentity(projection.WorkloadID, executionDigest, workload.InputDigest, environmentDigest)
	if err != nil {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, err
	}
	key := gate.NewWorkloadPassKey(gate.WorkloadPassNamespaceLocal, identity.IdentityDigest)
	if err := key.Validate(); err != nil {
		return gate.LocalWorkloadScheduleItem{}, LocalWorkloadInputProof{}, fmt.Errorf("local workload %q PASS key: %w", projection.WorkloadID, err)
	}
	return gate.LocalWorkloadScheduleItem{
			WorkloadID: projection.WorkloadID,
			LocalKey:   key, LocalIdentity: identity,
			Resource:      gate.LocalWorkloadResource{DurationMS: projection.PredictedDurationMS, CPU: projection.ResourceCPU, MemoryGiB: projection.ResourceMemoryGiB},
			LocalEligible: eligibility.Eligible,
		}, LocalWorkloadInputProof{
			WorkloadID: projection.WorkloadID, InputDigest: workload.InputDigest,
			ExecutionDigest: executionDigest, EnvironmentDigest: environmentDigest,
		}, nil
}

func localWorkloadPlanReceiptCoversItem(receipt gate.LocalExecutorSessionReceipt, workloadID gate.GateID, eligibility gate.LocalWorkloadExecutionEligibility) (bool, error) {
	if eligibility.Strategy == "" || gate.LocalExecutorSessionReceiptIncludesWorkload(receipt, workloadID) {
		return eligibility.Strategy != "", nil
	}
	if eligibility.Eligible {
		return false, fmt.Errorf("local workload %q requires a producer-sealed executor receipt covering the workload", workloadID)
	}
	return false, nil
}

func directRemoteLocalWorkloadPlanItem(projection gate.LocalWorkloadPlanningProjection) gate.LocalWorkloadScheduleItem {
	return gate.LocalWorkloadScheduleItem{
		WorkloadID: projection.WorkloadID,
		Resource: gate.LocalWorkloadResource{
			DurationMS: projection.PredictedDurationMS,
			CPU:        projection.ResourceCPU,
			MemoryGiB:  projection.ResourceMemoryGiB,
		},
	}
}

func localWorkloadPlanIdentity(workloadID gate.GateID, executionDigest, inputDigest, environmentDigest string) (gate.WorkloadPassIdentity, error) {
	identity := gate.WorkloadPassIdentity{WorkloadID: workloadID, ExecutionDigest: executionDigest, InputDigest: inputDigest, EnvironmentDigest: environmentDigest}
	digest, err := gate.WorkloadPassIdentitySHA256(identity)
	if err != nil {
		return gate.WorkloadPassIdentity{}, fmt.Errorf("local workload %q identity digest: %w", workloadID, err)
	}
	identity.IdentityDigest = digest
	if err := identity.Validate(); err != nil {
		return gate.WorkloadPassIdentity{}, fmt.Errorf("local workload %q identity: %w", workloadID, err)
	}
	return identity, nil
}

func localWorkloadPlanExecutionDigest(workload gate.Workload) (string, error) {
	digest, err := gate.WorkloadExecutionDigest(workload.ID)
	if err != nil {
		return "", fmt.Errorf("canonical local workload %q execution digest: %w", workload.ID, err)
	}
	if workload.CommandDigest != digest {
		return "", fmt.Errorf("canonical local workload %q execution digest drifted", workload.ID)
	}
	return gate.WorkloadPassExecutionDigest(workload), nil
}
