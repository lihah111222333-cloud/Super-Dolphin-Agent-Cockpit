package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const gateSchemaVersion uint32 = 1

// Profile identifies a versioned gate planning surface.
type Profile string

const (
	ProfileLocalFast      Profile = "local-fast"
	ProfilePush           Profile = "push"
	ProfileRemoteRequired Profile = "remote-required"
	ProfilePromotion      Profile = "promotion"
	ProfileRelease        Profile = "release"
)

// Validate 校验 profile 是否属于公开 CLI 契约。
func (p Profile) Validate() error {
	switch p {
	case ProfileLocalFast, ProfilePush, ProfileRemoteRequired, ProfilePromotion, ProfileRelease:
		return nil
	default:
		return fmt.Errorf("unsupported gate profile %q", p)
	}
}

// GateID is the stable identity shared by planners and the existing command owner.
type GateID string

const (
	GateIDAIMaintenanceSelfTest    GateID = "ai-maintenance:self-test"
	GateIDFrontendLint             GateID = "frontend:lint"
	GateIDFrontendTest             GateID = "frontend:test"
	GateIDFrontendFullTest         GateID = "frontend:test-full"
	GateIDFrontendBuild            GateID = "frontend:build"
	GateIDFrontendEmbedVerify      GateID = "frontend:embed-verify"
	GateIDBackendTestWithGuard     GateID = "backend:test_with_guard"
	GateIDLSPChangedDiagnostics    GateID = "lsp:changed-diagnostics"
	GateIDBackendTestGuardWithRace GateID = "backend:test_with_guard_and_race"
	GateIDBackendNilness           GateID = "backend:nilness"
	GateIDSQLCVerify               GateID = "sqlc:verify"
	GateIDCodemapCheck             GateID = "codemap:check"
	GateIDProjectMapCheck          GateID = "project-map:check"
	GateIDCapabilityContractCheck  GateID = "capcontract:check"
	GateIDWhitespaceCheck          GateID = "diff:whitespace"
	GateIDReleaseLayeredCheck      GateID = "release:ci-l3"
)

const (
	containerExecutionOwner = "container-executor"
	containerExecutorBinary = "/usr/local/bin/super-dolphin-gate-executor"
	commandIdentityPrefix   = "container-executor/v1/"
)

// GateSpec is the canonical catalog entry consumed only by the container executor.
type GateSpec struct {
	ID               GateID    `json:"id"`
	ExecutionOwner   string    `json:"execution_owner"`
	CommandIdentity  string    `json:"command_identity"`
	Argv             []string  `json:"argv"`
	Profiles         []Profile `json:"profiles"`
	RequiredProfiles []Profile `json:"required_profiles"`
}

// Validate 校验 GateSpec 的容器命令闭包与 profile 集合。
func (s GateSpec) Validate() error {
	if strings.TrimSpace(string(s.ID)) == "" {
		return errors.New("gate id is required")
	}
	if s.ExecutionOwner != containerExecutionOwner {
		return fmt.Errorf("gate %q execution_owner must be %q", s.ID, containerExecutionOwner)
	}
	if s.CommandIdentity != commandIdentityPrefix+string(s.ID) {
		return fmt.Errorf("gate %q has invalid command_identity %q", s.ID, s.CommandIdentity)
	}
	if !slices.Equal(s.Argv, containerGateArgv(s.ID)) {
		return fmt.Errorf("gate %q argv is not the canonical container executor command", s.ID)
	}
	if err := validateProfileSet("profiles", s.Profiles); err != nil {
		return fmt.Errorf("gate %q: %w", s.ID, err)
	}
	if err := validateProfileSet("required_profiles", s.RequiredProfiles); err != nil {
		return fmt.Errorf("gate %q: %w", s.ID, err)
	}
	for _, profile := range s.RequiredProfiles {
		if !slices.Contains(s.Profiles, profile) {
			return fmt.Errorf("gate %q required profile %q is not enabled", s.ID, profile)
		}
	}
	return nil
}

// GateRegistry 返回单一有序 gate catalog 的隔离副本。
func GateRegistry() []GateSpec {
	all := allProfiles()
	nonRelease := []Profile{ProfileLocalFast, ProfilePush, ProfileRemoteRequired, ProfilePromotion}
	releaseRequired := []Profile{ProfileRelease}
	registry := []GateSpec{
		newGateSpec(GateIDAIMaintenanceSelfTest, all, all),
		newGateSpec(GateIDFrontendLint, all, all),
		newGateSpec(GateIDFrontendTest, nonRelease, nonRelease),
		newGateSpec(GateIDFrontendFullTest, []Profile{ProfileRelease}, []Profile{ProfileRelease}),
		newGateSpec(GateIDFrontendBuild, all, all),
		newGateSpec(GateIDFrontendEmbedVerify, all, all),
		newGateSpec(GateIDBackendTestWithGuard, all, all),
		newGateSpec(GateIDLSPChangedDiagnostics, all, all),
		newGateSpec(GateIDBackendTestGuardWithRace, all, releaseRequired),
		newGateSpec(GateIDBackendNilness, all, releaseRequired),
		newGateSpec(GateIDSQLCVerify, all, all),
		newGateSpec(GateIDCodemapCheck, all, all),
		newGateSpec(GateIDProjectMapCheck, all, all),
		newGateSpec(GateIDCapabilityContractCheck, all, all),
		newGateSpec(GateIDWhitespaceCheck, all, all),
		newGateSpec(GateIDReleaseLayeredCheck, []Profile{ProfileRelease}, []Profile{ProfileRelease}),
	}
	return cloneGateSpecs(registry)
}

func newGateSpec(id GateID, profiles, required []Profile) GateSpec {
	return GateSpec{
		ID:               id,
		ExecutionOwner:   containerExecutionOwner,
		CommandIdentity:  commandIdentityPrefix + string(id),
		Argv:             containerGateArgv(id),
		Profiles:         append([]Profile(nil), profiles...),
		RequiredProfiles: append([]Profile(nil), required...),
	}
}

func containerGateArgv(id GateID) []string {
	return []string{containerExecutorBinary, "run", "--gate", string(id)}
}

func allProfiles() []Profile {
	return []Profile{ProfileLocalFast, ProfilePush, ProfileRemoteRequired, ProfilePromotion, ProfileRelease}
}

func validateProfileSet(name string, profiles []Profile) error {
	if len(profiles) == 0 {
		return fmt.Errorf("%s is empty", name)
	}
	ordered := allProfiles()
	last := -1
	for _, profile := range profiles {
		if err := profile.Validate(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		index := slices.Index(ordered, profile)
		if index <= last {
			return fmt.Errorf("%s must be unique and canonically ordered", name)
		}
		last = index
	}
	return nil
}

func cloneGateSpecs(specs []GateSpec) []GateSpec {
	cloned := make([]GateSpec, len(specs))
	for index, spec := range specs {
		cloned[index] = spec
		cloned[index].Argv = append([]string(nil), spec.Argv...)
		cloned[index].Profiles = append([]Profile(nil), spec.Profiles...)
		cloned[index].RequiredProfiles = append([]Profile(nil), spec.RequiredProfiles...)
	}
	return cloned
}

// GateRegistryDigest 将 plan 绑定到完整有序 catalog。
func GateRegistryDigest() (string, error) {
	registry := GateRegistry()
	if err := validateGateRegistry(registry); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(registry)
	if err != nil {
		return "", fmt.Errorf("marshal gate registry: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// validateGateRegistry 校验 catalog 标识、命令与 profile 约束均唯一且完整。
func validateGateRegistry(registry []GateSpec) error {
	if len(registry) == 0 {
		return errors.New("gate registry is empty")
	}
	seen := make(map[GateID]struct{}, len(registry))
	commands := make(map[string]struct{}, len(registry))
	for index, spec := range registry {
		if err := spec.Validate(); err != nil {
			return fmt.Errorf("gate registry entry %d: %w", index, err)
		}
		if _, duplicate := seen[spec.ID]; duplicate {
			return fmt.Errorf("gate registry repeats id %q", spec.ID)
		}
		if _, duplicate := commands[spec.CommandIdentity]; duplicate {
			return fmt.Errorf("gate registry repeats command_identity %q", spec.CommandIdentity)
		}
		seen[spec.ID] = struct{}{}
		commands[spec.CommandIdentity] = struct{}{}
	}
	return nil
}

// GatePlan is the canonical JSON plan emitted by the first CLI slice.
type GatePlan struct {
	SchemaVersion uint32     `json:"schema_version"`
	Profile       Profile    `json:"profile"`
	Source        SourceSpec `json:"source"`
	PolicyDigest  string     `json:"policy_digest"`
	Gates         []GateSpec `json:"gates"`
	PlanDigest    string     `json:"plan_digest"`
}

// BuildGatePlan 在不读取 Git 或 worktree 的前提下生成 profile 必需 gate plan。
func BuildGatePlan(profile Profile, source SourceSpec) (GatePlan, error) {
	if err := profile.Validate(); err != nil {
		return GatePlan{}, err
	}
	if err := source.Validate(); err != nil {
		return GatePlan{}, fmt.Errorf("source spec: %w", err)
	}
	policyDigest, err := GateRegistryDigest()
	if err != nil {
		return GatePlan{}, err
	}
	plan := GatePlan{
		SchemaVersion: gateSchemaVersion,
		Profile:       profile,
		Source:        source,
		PolicyDigest:  policyDigest,
		Gates:         requiredGatesForProfile(profile),
	}
	plan.PlanDigest, err = plan.digest()
	if err != nil {
		return GatePlan{}, err
	}
	return plan, plan.Validate()
}

// Validate 拒绝 registry 漂移、顺序变化与 digest 篡改。
func (p GatePlan) Validate() error {
	if p.SchemaVersion != gateSchemaVersion {
		return fmt.Errorf("unsupported gate plan schema_version %d", p.SchemaVersion)
	}
	if err := p.Profile.Validate(); err != nil {
		return err
	}
	if err := p.Source.Validate(); err != nil {
		return fmt.Errorf("source spec: %w", err)
	}
	policyDigest, err := GateRegistryDigest()
	if err != nil {
		return err
	}
	if p.PolicyDigest != policyDigest || !equalGateSpecs(p.Gates, requiredGatesForProfile(p.Profile)) {
		return errors.New("gate plan does not match canonical registry")
	}
	wantDigest, err := p.digest()
	if err != nil {
		return err
	}
	if p.PlanDigest != wantDigest {
		return errors.New("gate plan digest mismatch")
	}
	return nil
}

// equalGateSpecs 比较包含嵌套切片的完整 GateSpec 命令闭包。
func equalGateSpecs(left, right []GateSpec) bool {
	return slices.EqualFunc(left, right, func(a, b GateSpec) bool {
		return a.ID == b.ID &&
			a.ExecutionOwner == b.ExecutionOwner &&
			a.CommandIdentity == b.CommandIdentity &&
			slices.Equal(a.Argv, b.Argv) &&
			slices.Equal(a.Profiles, b.Profiles) &&
			slices.Equal(a.RequiredProfiles, b.RequiredProfiles)
	})
}

func requiredGatesForProfile(profile Profile) []GateSpec {
	registry := GateRegistry()
	required := make([]GateSpec, 0, len(registry))
	for _, spec := range registry {
		if slices.Contains(spec.RequiredProfiles, profile) {
			required = append(required, spec)
		}
	}
	return cloneGateSpecs(required)
}

func (p GatePlan) digest() (string, error) {
	material := struct {
		SchemaVersion uint32     `json:"schema_version"`
		Profile       Profile    `json:"profile"`
		Source        SourceSpec `json:"source"`
		PolicyDigest  string     `json:"policy_digest"`
		Gates         []GateSpec `json:"gates"`
	}{p.SchemaVersion, p.Profile, p.Source, p.PolicyDigest, p.Gates}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal gate plan digest material: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// ExitCode is the stable process contract shared by gate adapters.
type ExitCode int

const (
	ExitOK                 ExitCode = 0
	ExitProtocol           ExitCode = 2
	ExitGateViolation      ExitCode = 10
	ExitEvidenceIncomplete ExitCode = 11
	ExitSourceMismatch     ExitCode = 12
	ExitInfrastructure     ExitCode = 13
	ExitRegistryInvariant  ExitCode = 14
	ExitCancelled          ExitCode = 15
	ExitTimeout            ExitCode = 16
)

// Validate 校验进程退出码是否属于版本化协议。
func (c ExitCode) Validate() error {
	switch c {
	case ExitOK, ExitProtocol, ExitGateViolation, ExitEvidenceIncomplete, ExitSourceMismatch, ExitInfrastructure, ExitRegistryInvariant, ExitCancelled, ExitTimeout:
		return nil
	default:
		return fmt.Errorf("unsupported gate exit code %d", c)
	}
}

type exitError struct {
	code ExitCode
	err  error
}

// Error 返回稳定退出错误的原始消息。
func (e *exitError) Error() string { return e.err.Error() }

// Unwrap 保留底层错误链供调用方分类。
func (e *exitError) Unwrap() error { return e.err }

// WithExitCode 为失败附加稳定进程退出码。
func WithExitCode(code ExitCode, err error) error {
	if err == nil {
		err = errors.New("gate command failed")
	}
	if validateErr := code.Validate(); validateErr != nil || code == ExitOK {
		return &exitError{code: ExitRegistryInvariant, err: fmt.Errorf("invalid failure exit code %d: %w", code, err)}
	}
	return &exitError{code: code, err: err}
}

// ExitCodeOf 将未分类错误保守映射为基础设施失败。
func ExitCodeOf(err error) ExitCode {
	if err == nil {
		return ExitOK
	}
	var coded *exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ExitInfrastructure
}
