package remoteportable

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"

	"strings"
	"unicode"
)

const (
	// PortableManifestSchemaVersion identifies the sole top-level portable input.
	PortableManifestSchemaVersion = "remote-ci-portable-manifest/v1"
	// PortableManifestContractID identifies the four-node lifecycle contract.
	PortableManifestContractID = "remote-ci-four-node/v1"
	// PortableManifestDigestDomain separates top-level manifest identity.
	PortableManifestDigestDomain = "remote-ci-portable-manifest/v1"
	// PortableManifestABIDigestDomain separates top-level wire ABI identity.
	PortableManifestABIDigestDomain = "remote-ci-portable-manifest-abi/v1"
	// PortableManifestWorkloadExecutionDigestDomain separates one workload's execution identity.
	PortableManifestWorkloadExecutionDigestDomain = "remote-ci-portable-workload-execution/v1"
	PortableManifestMaxResultBytes                = 1 << 20
	PortableManifestMaxEventBytes                 = 64 << 10
)

// PortableManifest 是 remote-ci-portable-manifest/v1 的唯一顶层输入。
type PortableManifest struct {
	SchemaVersion            string                          `json:"schema_version"`
	ContractID               string                          `json:"contract_id"`
	Engine                   PortableManifestEngine          `json:"engine"`
	Target                   PortableManifestTarget          `json:"target"`
	Workloads                []PortableManifestWorkload      `json:"workloads"`
	CatalogPolicies          []PortableManifestCatalogPolicy `json:"catalog_policies"`
	Profile                  Profile                         `json:"profile"`
	CapacityPolicyDigest     string                          `json:"capacity_policy_digest"`
	RequiredNodeCapabilities []string                        `json:"required_node_capabilities"`
	LogPolicy                PortableManifestLogPolicy       `json:"log_policy"`
	Authority                PortableManifestAuthority       `json:"authority"`
	Modules                  []RepositoryWorkloadModule      `json:"modules"`
	ExcludedNestedModules    []string                        `json:"excluded_nested_modules"`
}

// PortableManifestEngine 只声明独立 engine 的静态协议、工具链和能力要求；actual facts 在 composition 绑定时注入。
type PortableManifestEngine struct {
	RequiredProtocolABIDigest string   `json:"required_protocol_abi_digest"`
	RequiredToolchainDigest   string   `json:"required_toolchain_digest"`
	RequiredCapabilities      []string `json:"required_capabilities"`
}

// PortableManifestTarget 绑定静态 repository object format 与 repo-relative mount；exact object/path 在外部 binding。
type PortableManifestTarget struct {
	RepositoryID  string `json:"repository_id"`
	ObjectFormat  string `json:"object_format"`
	WorktreeMount string `json:"worktree_mount"`
}

// PortableManifestWorkload 是顶层 manifest 的稳定 workload wire DTO。
type PortableManifestWorkload struct {
	UnitID               string `json:"unit_id"`
	CommandDigest        string `json:"command_digest"`
	TargetPlatform       string `json:"target_platform"`
	RuntimeCapability    string `json:"runtime_capability"`
	CPUMilli             int64  `json:"cpu_milli"`
	MemoryBytes          int64  `json:"memory_bytes"`
	HistoricalEstimateMS int64  `json:"historical_estimate_ms"`
	ExecutionDigest      string `json:"execution_digest"`
	Kind                 string `json:"kind"`
	ModuleRoot           string `json:"module_root"`
	Package              string `json:"package"`
	Name                 string `json:"name"`
	ExecutionPolicy      string `json:"execution_policy"`
}

// PortableManifestCatalogPolicy 是 exact-tree Go catalog 自动扩展使用的显式平台资源策略。
type PortableManifestCatalogPolicy struct {
	Platform             string `json:"platform"`
	Runtime              string `json:"runtime"`
	CPUMilli             int64  `json:"cpu_milli"`
	MemoryBytes          int64  `json:"memory_bytes"`
	HistoricalEstimateMS int64  `json:"historical_estimate_ms"`
	ExecutionPolicy      string `json:"execution_policy"`
}

// PortableManifestLogPolicy 绑定 result/event schema 与输出边界。
type PortableManifestLogPolicy struct {
	ResultSchema   string `json:"result_schema"`
	EventSchema    string `json:"event_schema"`
	MaxResultBytes int64  `json:"max_result_bytes"`
	MaxEventBytes  int64  `json:"max_event_bytes"`
}

// PortableManifestAuthority 绑定合同身份和 PostgreSQL authority identity。
type PortableManifestAuthority struct {
	ContractID         string `json:"contract_id"`
	PostgreSQLIdentity string `json:"postgresql_identity"`
}

// PortableManifestCanonicalEncode 生成 top-level manifest 的 canonical bytes。
func PortableManifestCanonicalEncode(manifest PortableManifest) ([]byte, error) {
	if err := manifest.validateTopLevel(); err != nil {
		return nil, err
	}
	if manifest.CatalogPolicies == nil {
		manifest.CatalogPolicies = []PortableManifestCatalogPolicy{}
	}
	if manifest.Workloads == nil {
		manifest.Workloads = []PortableManifestWorkload{}
	}
	manifest.Modules = slices.Clone(manifest.Modules)
	manifest.ExcludedNestedModules = slices.Clone(manifest.ExcludedNestedModules)
	manifest.RequiredNodeCapabilities = slices.Clone(manifest.RequiredNodeCapabilities)
	manifest.CatalogPolicies = slices.Clone(manifest.CatalogPolicies)
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode portable manifest: %w", err)
	}
	if len(encoded) == 0 || len(encoded) > PortableWorkloadManifestMaxBytes {
		return nil, errors.New("portable manifest exceeds 1 MiB")
	}
	return encoded, nil
}

// PortableManifestContentDigest 返回省略 raw blob digest 的 canonical manifest domain digest。
func PortableManifestContentDigest(manifest PortableManifest) (string, error) {
	encoded, err := PortableManifestCanonicalEncode(manifest)
	if err != nil {
		return "", err
	}
	return portableFramedDigest(PortableManifestDigestDomain, encoded), nil
}

// DerivePortableWorkloadExecutionDigest derives the canonical execution identity
// from one workload with execution_digest itself blanked from the material.
func DerivePortableWorkloadExecutionDigest(workload PortableManifestWorkload) (string, error) {
	workload.ExecutionDigest = ""
	if err := validatePortableWorkloadMaterial(workload); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(workload)
	if err != nil {
		return "", fmt.Errorf("encode portable workload execution material: %w", err)
	}
	return portableFramedDigest(PortableManifestWorkloadExecutionDigestDomain, encoded), nil
}

// DecodePortableManifest 严格解码 remote-ci-portable-manifest/v1。
func DecodePortableManifest(data []byte) (PortableManifest, error) {
	if len(data) == 0 || len(data) > PortableWorkloadManifestMaxBytes {
		return PortableManifest{}, errors.New("portable manifest size is invalid")
	}
	if err := walkPortableManifestJSON(data); err != nil {
		return PortableManifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest PortableManifest
	if err := decoder.Decode(&manifest); err != nil {
		return PortableManifest{}, fmt.Errorf("decode portable manifest: %w", err)
	}
	if err := ensurePortableManifestEOF(decoder); err != nil {
		return PortableManifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return PortableManifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, data) {
		return PortableManifest{}, errors.New("portable manifest is not canonical")
	}
	return manifest, nil
}

// Validate 校验 top-level manifest 的全部跨域身份与 workload 字段。
func (manifest PortableManifest) Validate() error {
	return manifest.validateTopLevel()
}

func (manifest PortableManifest) validateTopLevel() error {
	if manifest.SchemaVersion != PortableManifestSchemaVersion || manifest.ContractID != PortableManifestContractID {
		return errors.New("portable manifest schema or contract identity is invalid")
	}
	if err := manifest.validateStaticIdentity(); err != nil {
		return err
	}
	if err := manifest.validateStaticPolicy(); err != nil {
		return err
	}
	if err := manifest.validateModuleDeclarations(); err != nil {
		return err
	}
	if err := validatePortableManifestCatalogPolicies(manifest.CatalogPolicies); err != nil {
		return err
	}
	if len(manifest.Workloads) == 0 && len(manifest.CatalogPolicies) == 0 {
		return errors.New("portable manifest must declare workloads or catalog_policies")
	}
	return validatePortableManifestWorkloads(manifest.Workloads)
}

func (manifest PortableManifest) validateStaticIdentity() error {
	if err := manifest.Engine.validate(); err != nil {
		return err
	}
	return manifest.Target.validate()
}

func (manifest PortableManifest) validateStaticPolicy() error {
	if err := manifest.Profile.Validate(); err != nil {
		return err
	}
	if !validPortableDigest(manifest.CapacityPolicyDigest) {
		return errors.New("portable capacity_policy_digest is invalid")
	}
	if err := validatePortableCapabilities(manifest.RequiredNodeCapabilities); err != nil {
		return err
	}
	if err := manifest.LogPolicy.validate(); err != nil {
		return err
	}
	return manifest.Authority.validate()
}

func (manifest PortableManifest) validateModuleDeclarations() error {
	if manifest.Modules == nil || manifest.ExcludedNestedModules == nil || len(manifest.Modules) == 0 {
		return errors.New("portable manifest module declarations are required")
	}
	if err := validatePortableModules(manifest.Modules); err != nil {
		return err
	}
	return validatePortableExcludedModules(manifest.ExcludedNestedModules)
}

func (engine PortableManifestEngine) validate() error {
	if !validPortableDigest(engine.RequiredProtocolABIDigest) || !validPortableDigest(engine.RequiredToolchainDigest) || engine.RequiredCapabilities == nil || len(engine.RequiredCapabilities) == 0 {
		return errors.New("portable engine policy is invalid")
	}
	for index, capability := range engine.RequiredCapabilities {
		if !validPortableToken(capability) || index > 0 && engine.RequiredCapabilities[index-1] >= capability {
			return errors.New("portable engine required capabilities must be sorted and unique")
		}
	}
	return nil
}

func (target PortableManifestTarget) validate() error {
	if err := validatePortableIdentifier(target.RepositoryID, "target.repository_id"); err != nil {
		return err
	}
	if target.ObjectFormat != "sha1" && target.ObjectFormat != "sha256" {
		return errors.New("portable target object_format is invalid")
	}
	if !validPortableRepoRelativePath(target.WorktreeMount) {
		return errors.New("portable target mount is invalid")
	}
	return nil
}

func (policy PortableManifestLogPolicy) validate() error {
	if policy.ResultSchema != "remote-ci-result/v1" || policy.EventSchema != "remote-ci-event/v1" {
		return errors.New("portable log policy is invalid")
	}
	if policy.MaxResultBytes <= 0 || policy.MaxResultBytes > PortableManifestMaxResultBytes || policy.MaxEventBytes <= 0 || policy.MaxEventBytes > PortableManifestMaxEventBytes {
		return errors.New("portable log policy is invalid")
	}
	return nil
}

func (authority PortableManifestAuthority) validate() error {
	if authority.ContractID != PortableManifestContractID || authority.PostgreSQLIdentity != "remote-ci-postgresql-primary/v1" {
		return errors.New("portable authority identity is invalid")
	}
	return nil
}

func validatePortableCapabilities(capabilities []string) error {
	if len(capabilities) == 0 {
		return errors.New("portable required_node_capabilities are required")
	}
	for index, capability := range capabilities {
		if !validPortableToken(capability) || index > 0 && capabilities[index-1] >= capability {
			return errors.New("portable required_node_capabilities must be sorted and unique")
		}
	}
	return nil
}

func validatePortableManifestWorkloads(workloads []PortableManifestWorkload) error {
	seen := make(map[string]struct{}, len(workloads))
	for index, workload := range workloads {
		if err := validatePortableManifestWorkload(workload); err != nil {
			return fmt.Errorf("workload %d: %w", index, err)
		}
		if _, duplicate := seen[workload.UnitID]; duplicate || index > 0 && workloads[index-1].UnitID >= workload.UnitID {
			return errors.New("portable manifest workloads must be sorted and unique")
		}
		seen[workload.UnitID] = struct{}{}
	}
	return nil
}

func validatePortableManifestWorkload(workload PortableManifestWorkload) error {
	if err := validatePortableWorkloadMaterial(workload); err != nil {
		return err
	}
	expected, err := DerivePortableWorkloadExecutionDigest(workload)
	if err != nil {
		return err
	}
	if workload.ExecutionDigest != expected {
		return errors.New("portable workload execution_digest does not match canonical material")
	}
	return nil
}

func validatePortableWorkloadMaterial(workload PortableManifestWorkload) error {
	if err := validatePortableIdentifier(workload.UnitID, "workload.unit_id"); err != nil {
		return err
	}
	if !validPortableDigest(workload.CommandDigest) {
		return errors.New("portable workload command_digest is invalid")
	}
	if err := validatePortableWorkloadRouting(workload); err != nil {
		return err
	}
	return validatePortableWorkloadTarget(workload)
}

// validatePortableManifestCatalogPolicies 校验 Linux/Windows 双平台策略的排序与完整性。
func validatePortableManifestCatalogPolicies(policies []PortableManifestCatalogPolicy) error {
	if len(policies) == 0 {
		return nil
	}
	if len(policies) != 2 {
		return errors.New("portable manifest catalog_policies must contain exactly two platforms")
	}
	wantPlatforms := [...]string{string(RepositoryWorkloadPlatformLinuxAMD64), string(RepositoryWorkloadPlatformWindowsAMD64)}
	for index, policy := range policies {
		if policy.Platform != wantPlatforms[index] {
			return errors.New("portable manifest catalog_policies must be sorted linux then windows")
		}
		if index > 0 && policies[index-1].Platform >= policy.Platform {
			return errors.New("portable manifest catalog_policies must be sorted and unique")
		}
		if err := validatePortableManifestCatalogPolicy(policy); err != nil {
			return err
		}
	}
	return nil
}

// validatePortableManifestCatalogPolicy 校验单个平台的 runtime、资源和执行策略。
func validatePortableManifestCatalogPolicy(policy PortableManifestCatalogPolicy) error {
	if !validPortableCatalogPolicyPlatformRuntime(policy.Platform, policy.Runtime) || policy.CPUMilli <= 0 || policy.MemoryBytes <= 0 || policy.HistoricalEstimateMS <= 0 {
		return errors.New("portable manifest catalog policy resource fields are invalid")
	}
	if policy.ExecutionPolicy != string(RepositoryWorkloadExecutionPolicyNormal) && policy.ExecutionPolicy != string(RepositoryWorkloadExecutionPolicyRace) {
		return errors.New("portable manifest catalog policy execution policy is invalid")
	}
	return nil
}

func validPortableCatalogPolicyPlatformRuntime(platform, runtime string) bool {
	return platform == string(RepositoryWorkloadPlatformLinuxAMD64) && runtime == string(RepositoryWorkloadRuntimeUbuntuContainer) || platform == string(RepositoryWorkloadPlatformWindowsAMD64) && runtime == string(RepositoryWorkloadRuntimeWindowsNative)
}

func validatePortableWorkloadRouting(workload PortableManifestWorkload) error {
	if !validPortableToken(workload.TargetPlatform) || !validPortableToken(workload.RuntimeCapability) || workload.CPUMilli <= 0 || workload.MemoryBytes <= 0 || workload.HistoricalEstimateMS <= 0 {
		return errors.New("portable workload routing fields are invalid")
	}
	if !validPortableWorkloadPlatformRuntime(workload.TargetPlatform, workload.RuntimeCapability) {
		return errors.New("portable workload platform/runtime is invalid")
	}
	return nil
}

func validPortableWorkloadPlatformRuntime(platform, runtime string) bool {
	return platform == string(RepositoryWorkloadPlatformLinuxAMD64) && runtime == string(RepositoryWorkloadRuntimeUbuntuContainer) || platform == string(RepositoryWorkloadPlatformWindowsAMD64) && runtime == string(RepositoryWorkloadRuntimeWindowsNative)
}

func validatePortableWorkloadTarget(workload PortableManifestWorkload) error {
	if workload.Kind != RepositoryWorkloadKindGoTest && workload.Kind != RepositoryWorkloadKindGoPackage {
		return errors.New("portable workload kind is invalid")
	}
	if !validPortableModuleRoot(workload.ModuleRoot) || !validPortablePackagePath(workload.Package) || !validPortableToken(workload.ExecutionPolicy) {
		return errors.New("portable workload target fields are invalid")
	}
	if !validPortableWorkloadName(workload.Kind, workload.Name) {
		return errors.New("portable workload name is invalid")
	}
	if workload.ExecutionPolicy != string(RepositoryWorkloadExecutionPolicyNormal) && workload.ExecutionPolicy != string(RepositoryWorkloadExecutionPolicyRace) {
		return errors.New("portable workload execution policy is invalid")
	}
	return nil
}

func validPortableModuleRoot(value string) bool {
	return value == "." || validPortableRepoRelativePath(value)
}

func validPortablePackagePath(value string) bool {
	if value == "." {
		return true
	}
	if !strings.HasPrefix(value, "./") || len(value) <= 2 || !validPortableRepoRelativePath(value[2:]) {
		return false
	}
	for component := range strings.SplitSeq(value[2:], "/") {
		if strings.HasPrefix(component, "-") {
			return false
		}
	}
	return true
}

func validPortableWorkloadName(kind, name string) bool {
	if kind == RepositoryWorkloadKindGoPackage {
		return name == ""
	}
	if kind != RepositoryWorkloadKindGoTest || !strings.HasPrefix(name, "Test") || len(name) <= len("Test") {
		return false
	}
	return validPortableGoIdentifier(name)
}

func validPortableGoIdentifier(value string) bool {
	for index, runeValue := range value {
		if index == 0 && !unicode.IsLetter(runeValue) && runeValue != '_' || index > 0 && !unicode.IsLetter(runeValue) && !unicode.IsDigit(runeValue) && runeValue != '_' {
			return false
		}
	}
	return value != ""
}

// PortableManifestABIDigest 返回 top-level manifest wire ABI 摘要。
func PortableManifestABIDigest() (string, error) {
	material, err := portableTopLevelABIMaterial()
	if err != nil {
		return "", err
	}
	return portableFramedDigest(PortableManifestABIDigestDomain, material), nil
}

func portableTopLevelABIMaterial() ([]byte, error) {
	var material []byte
	for _, typ := range portableManifestWireTypes() {
		fields, err := portableTopLevelABIDescriptors(typ)
		if err != nil {
			return nil, err
		}
		material = appendPortableFrame(material, []byte(typ.Name()))
		for _, field := range fields {
			material = appendPortableFrame(material, []byte(field))
		}
	}
	return material, nil
}

func portableManifestWireTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[PortableManifest](), reflect.TypeFor[PortableManifestEngine](), reflect.TypeFor[PortableManifestTarget](),
		reflect.TypeFor[PortableManifestWorkload](), reflect.TypeFor[PortableManifestCatalogPolicy](), reflect.TypeFor[PortableManifestLogPolicy](), reflect.TypeFor[PortableManifestAuthority](),
		reflect.TypeFor[RepositoryWorkloadModule](),
	}
}

func portableTopLevelABIDescriptors(typ reflect.Type) ([]string, error) {
	fields := make([]string, 0, typ.NumField())
	for field := range typ.Fields() {
		name, options, hasOptions := strings.Cut(field.Tag.Get("json"), ",")
		if field.PkgPath != "" || !hasOptions && name == "" || name == "-" || options != "" {
			return nil, fmt.Errorf("portable ABI field %s.%s is invalid", typ.Name(), field.Name)
		}
		fields = append(fields, field.Name+"|"+name+"|"+field.Type.String())
	}
	return fields, nil
}

func portableTopLevelABIFields(typ reflect.Type) ([]string, error) {
	descriptors, err := portableTopLevelABIDescriptors(typ)
	if err != nil {
		return nil, err
	}
	fields := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		_, remainder, _ := strings.Cut(descriptor, "|")
		jsonName, _, _ := strings.Cut(remainder, "|")
		fields = append(fields, jsonName)
	}
	return fields, nil
}

// PortableManifestConsumerFields 返回由 ABI reflection 动态枚举的消费字段集合。
func PortableManifestConsumerFields() ([]string, error) {
	fields := make([]string, 0)
	for _, typ := range portableManifestWireTypes() {
		values, err := portableTopLevelABIFields(typ)
		if err != nil {
			return nil, err
		}
		for _, value := range values {
			fields = append(fields, typ.Name()+"."+value)
		}
	}
	return fields, nil
}

// ValidatePortableManifestFieldRegistry 校验 top-level ABI 字段可被动态枚举。
func ValidatePortableManifestFieldRegistry() error {
	fields, err := PortableManifestConsumerFields()
	if err != nil {
		return err
	}
	if len(fields) == 0 {
		return errors.New("portable manifest field registry is empty")
	}
	return nil
}
