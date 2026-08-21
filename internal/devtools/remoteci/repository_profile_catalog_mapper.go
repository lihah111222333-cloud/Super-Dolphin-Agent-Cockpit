package remoteci

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// RepositoryProfileCatalogMapping 是 strict manifest profile 的无副作用 canonical 投影。
type RepositoryProfileCatalogMapping struct {
	Catalog       gate.WorkloadCatalog
	CatalogDigest string
	Execution     []RepositoryWorkloadExecutionMapping
	Planning      []RepositoryWorkloadPlanningProjection
	Environment   []RepositoryWorkloadEnvironmentBinding
}

// RepositoryWorkloadExecutionMapping 绑定 manifest workload 与唯一可执行 Gate workload identity。
type RepositoryWorkloadExecutionMapping struct {
	SourceWorkloadID       string
	WorkloadID             string
	DeclaredCommandDigest  string
	CanonicalCommandDigest string
}

// RepositoryWorkloadPlanningProjection 只承载调度估值与资源需求。
type RepositoryWorkloadPlanningProjection struct {
	SourceWorkloadID    string
	WorkloadID          string
	BootstrapEstimateMS int64
	CPUMilli            int64
	MemoryBytes         int64
}

// RepositoryWorkloadEnvironmentBinding 只承载 target workload 的平台与 runtime 绑定。
type RepositoryWorkloadEnvironmentBinding struct {
	SourceWorkloadID string
	WorkloadID       string
	Platform         RepositoryWorkloadPlatform
	Runtime          RepositoryWorkloadRuntime
}

// MapRepositoryWorkloadProfile 把一个已 strict 校验的 manifest profile 映射为 authoritative catalog。
// 本函数只做纯内存映射，不读取 catalog inventory，也不接入 Coordinator、SQLite 或 executor side effect。
func MapRepositoryWorkloadProfile(manifest RepositoryWorkloadManifest, profile gate.Profile) (RepositoryProfileCatalogMapping, error) {
	if err := manifest.Validate(); err != nil {
		return RepositoryProfileCatalogMapping{}, fmt.Errorf("validate repository workload manifest: %w", err)
	}
	if err := profile.Validate(); err != nil {
		return RepositoryProfileCatalogMapping{}, err
	}
	selected, err := repositoryManifestProfile(manifest.Profiles, profile)
	if err != nil {
		return RepositoryProfileCatalogMapping{}, err
	}
	return buildRepositoryProfileCatalogMapping(manifest.Workloads, selected, profile)
}

func buildRepositoryProfileCatalogMapping(workloads []RepositoryWorkload, selected RepositoryWorkloadProfile, profile gate.Profile) (RepositoryProfileCatalogMapping, error) {
	workloadsByID := make(map[string]RepositoryWorkload, len(workloads))
	for _, workload := range workloads {
		workloadsByID[workload.ID] = workload
	}
	mapping := RepositoryProfileCatalogMapping{
		Execution:   make([]RepositoryWorkloadExecutionMapping, 0, len(selected.RequiredWorkloadIDs)),
		Planning:    make([]RepositoryWorkloadPlanningProjection, 0, len(selected.RequiredWorkloadIDs)),
		Environment: make([]RepositoryWorkloadEnvironmentBinding, 0, len(selected.RequiredWorkloadIDs)),
	}
	catalogWorkloads := make([]gate.Workload, 0, len(selected.RequiredWorkloadIDs))
	for _, sourceID := range selected.RequiredWorkloadIDs {
		source, ok := workloadsByID[sourceID]
		if !ok {
			return RepositoryProfileCatalogMapping{}, fmt.Errorf("profile %q references unknown workload %q", profile, sourceID)
		}
		canonical, err := mapRepositoryWorkloadExecution(source)
		if err != nil {
			return RepositoryProfileCatalogMapping{}, fmt.Errorf("map workload %q: %w", source.ID, err)
		}
		catalogWorkloads = append(catalogWorkloads, canonical)
		appendRepositoryWorkloadProjections(&mapping, source, canonical)
	}
	catalog, err := gate.NewAuthoritativeWorkloadCatalog(catalogWorkloads)
	if err != nil {
		return RepositoryProfileCatalogMapping{}, fmt.Errorf("build authoritative workload catalog: %w", err)
	}
	mapping.Catalog = catalog
	catalogDigest, err := gate.WorkloadCatalogDigest(mapping.Catalog)
	if err != nil {
		return RepositoryProfileCatalogMapping{}, fmt.Errorf("digest authoritative workload catalog: %w", err)
	}
	mapping.CatalogDigest = catalogDigest
	if err := validateRepositoryProfileCatalogMapping(mapping, selected.RequiredWorkloadIDs); err != nil {
		return RepositoryProfileCatalogMapping{}, err
	}
	return mapping, nil
}

func repositoryManifestProfile(profiles []RepositoryWorkloadProfile, profile gate.Profile) (RepositoryWorkloadProfile, error) {
	for _, candidate := range profiles {
		if candidate.Profile == profile {
			return candidate, nil
		}
	}
	return RepositoryWorkloadProfile{}, fmt.Errorf("profile %q is not declared by repository workload manifest", profile)
}

func mapRepositoryWorkloadExecution(source RepositoryWorkload) (gate.Workload, error) {
	policy, err := mapRepositoryExecutionPolicy(source.ExecutionPolicy)
	if err != nil {
		return gate.Workload{}, err
	}
	target := gate.PortableGoTarget{
		ModuleRoot:      source.ModuleRoot,
		Package:         source.Package,
		Name:            source.Name,
		ExecutionPolicy: policy,
	}
	switch source.Kind {
	case RepositoryWorkloadKindGoPackage:
		return gate.NewPortableGoPackageWorkload(target, source.BootstrapEstimateMS)
	case RepositoryWorkloadKindGoTest:
		return gate.NewPortableGoTestWorkload(target, source.BootstrapEstimateMS)
	default:
		return gate.Workload{}, fmt.Errorf("repository workload kind %q has no canonical execution mapping", source.Kind)
	}
}

func mapRepositoryExecutionPolicy(policy RepositoryWorkloadExecutionPolicy) (gate.PortableGoExecutionPolicy, error) {
	switch policy {
	case RepositoryWorkloadExecutionPolicyNormal:
		return gate.PortableGoExecutionPolicyNormal, nil
	case RepositoryWorkloadExecutionPolicyRace:
		return gate.PortableGoExecutionPolicyRace, nil
	default:
		return "", fmt.Errorf("repository execution policy %q has no canonical execution mapping", policy)
	}
}

func appendRepositoryWorkloadProjections(mapping *RepositoryProfileCatalogMapping, source RepositoryWorkload, canonical gate.Workload) {
	mapping.Execution = append(mapping.Execution, RepositoryWorkloadExecutionMapping{
		SourceWorkloadID: source.ID, WorkloadID: canonical.ID,
		DeclaredCommandDigest: source.CommandDigest, CanonicalCommandDigest: canonical.CommandDigest,
	})
	mapping.Planning = append(mapping.Planning, RepositoryWorkloadPlanningProjection{
		SourceWorkloadID: source.ID, WorkloadID: canonical.ID, BootstrapEstimateMS: source.BootstrapEstimateMS,
		CPUMilli: source.CPUMilli, MemoryBytes: source.MemoryBytes,
	})
	mapping.Environment = append(mapping.Environment, RepositoryWorkloadEnvironmentBinding{
		SourceWorkloadID: source.ID, WorkloadID: canonical.ID, Platform: source.Platform, Runtime: source.Runtime,
	})
}

func validateRepositoryProfileCatalogMapping(mapping RepositoryProfileCatalogMapping, requiredIDs []string) error {
	if err := validateRepositoryCatalogHeader(mapping); err != nil {
		return err
	}
	if err := validateRepositoryProjectionCounts(mapping, len(requiredIDs)); err != nil {
		return err
	}
	seenWorkloadIDs := make(map[string]struct{}, len(requiredIDs))
	for index, sourceID := range requiredIDs {
		if err := validateRepositoryMappingRow(mapping, index, sourceID, seenWorkloadIDs); err != nil {
			return err
		}
	}
	return nil
}

func validateRepositoryCatalogHeader(mapping RepositoryProfileCatalogMapping) error {
	if err := gate.ValidateWorkloadCatalog(mapping.Catalog); err != nil {
		return fmt.Errorf("validate mapped workload catalog: %w", err)
	}
	if !mapping.Catalog.Authoritative {
		return errors.New("mapped workload catalog must be authoritative")
	}
	digest, err := gate.WorkloadCatalogDigest(mapping.Catalog)
	if err != nil {
		return fmt.Errorf("digest mapped workload catalog: %w", err)
	}
	if digest != mapping.CatalogDigest {
		return errors.New("mapped workload catalog digest is stale")
	}
	return nil
}

func validateRepositoryProjectionCounts(mapping RepositoryProfileCatalogMapping, requiredCount int) error {
	if len(mapping.Catalog.Workloads) != requiredCount || len(mapping.Execution) != requiredCount ||
		len(mapping.Planning) != requiredCount || len(mapping.Environment) != requiredCount {
		return errors.New("mapped workload catalog has missing or orphan projections")
	}
	return nil
}

func validateRepositoryMappingRow(mapping RepositoryProfileCatalogMapping, index int, sourceID string, seen map[string]struct{}) error {
	workload := mapping.Catalog.Workloads[index]
	execution := mapping.Execution[index]
	planning := mapping.Planning[index]
	environment := mapping.Environment[index]
	if err := validateRepositoryMappingIdentity(index, sourceID, workload.ID, execution, planning, environment, seen); err != nil {
		return err
	}
	if err := validateRepositoryMappingExecution(index, workload, execution); err != nil {
		return err
	}
	if err := validateRepositoryMappingPlanning(index, workload, planning); err != nil {
		return err
	}
	return validateRepositoryMappingEnvironment(index, environment)
}

func validateRepositoryMappingIdentity(index int, sourceID, workloadID string, execution RepositoryWorkloadExecutionMapping, planning RepositoryWorkloadPlanningProjection, environment RepositoryWorkloadEnvironmentBinding, seen map[string]struct{}) error {
	if execution.SourceWorkloadID != sourceID || planning.SourceWorkloadID != sourceID || environment.SourceWorkloadID != sourceID {
		return fmt.Errorf("mapped workload %d source identity is stale or orphaned", index)
	}
	if execution.WorkloadID != workloadID || planning.WorkloadID != workloadID || environment.WorkloadID != workloadID {
		return fmt.Errorf("mapped workload %d canonical identity is stale or orphaned", index)
	}
	if _, ok := seen[workloadID]; ok {
		return fmt.Errorf("mapped workload %d duplicates canonical execution ID %q", index, workloadID)
	}
	seen[workloadID] = struct{}{}
	return nil
}

func validateRepositoryMappingExecution(index int, workload gate.Workload, execution RepositoryWorkloadExecutionMapping) error {
	if err := gate.ValidateExecutableWorkloadID(gate.GateID(workload.ID)); err != nil {
		return fmt.Errorf("mapped workload %d is not executable: %w", index, err)
	}
	digest, err := gate.WorkloadExecutionDigest(workload.ID)
	if err != nil {
		return fmt.Errorf("mapped workload %d execution digest: %w", index, err)
	}
	if digest != workload.CommandDigest || execution.CanonicalCommandDigest != workload.CommandDigest {
		return fmt.Errorf("mapped workload %d canonical command digest is stale", index)
	}
	if !isCanonicalPrefixedSHA256(execution.DeclaredCommandDigest) {
		return fmt.Errorf("mapped workload %d declared command digest is invalid", index)
	}
	return nil
}

func validateRepositoryMappingPlanning(index int, workload gate.Workload, planning RepositoryWorkloadPlanningProjection) error {
	if planning.BootstrapEstimateMS != workload.BootstrapEstimateMS || planning.CPUMilli <= 0 || planning.MemoryBytes <= 0 {
		return fmt.Errorf("mapped workload %d planning projection is invalid", index)
	}
	return nil
}

func validateRepositoryMappingEnvironment(index int, environment RepositoryWorkloadEnvironmentBinding) error {
	if err := validateRepositoryWorkloadPlatformRuntime(environment.Platform, environment.Runtime); err != nil {
		return fmt.Errorf("mapped workload %d environment binding: %w", index, err)
	}
	return nil
}

func isCanonicalPrefixedSHA256(value string) bool {
	digest, ok := strings.CutPrefix(value, "sha256:")
	return ok && len(digest) == 64 && strings.Trim(digest, "0123456789abcdef") == ""
}
