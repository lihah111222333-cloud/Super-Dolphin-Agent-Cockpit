package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// LocalWorkloadRunnerSemanticPolicy is the canonical local runner policy owner.
const LocalWorkloadRunnerSemanticPolicy = cicontract.LocalWorkloadRunnerSemanticPolicy

// WorkloadPassNamespace identifies the execution authority that produced a
// PASS.  It is deliberately outside WorkloadPassIdentity's canonical hash.
type WorkloadPassNamespace string

const (
	// WorkloadPassNamespaceRemote selects historical and new ECI evidence.
	WorkloadPassNamespaceRemote WorkloadPassNamespace = cicontract.RemoteWorkloadPassNamespace
	// WorkloadPassNamespaceLocal selects canonical host evidence.
	WorkloadPassNamespaceLocal WorkloadPassNamespace = cicontract.LocalWorkloadPassNamespace
)

// Validate rejects namespace aliases so lookup cannot silently cross the
// local/ECI authority boundary.
func (namespace WorkloadPassNamespace) Validate() error {
	switch namespace {
	case WorkloadPassNamespaceRemote, WorkloadPassNamespaceLocal:
		return nil
	default:
		return fmt.Errorf("workload PASS namespace %q is unsupported", namespace)
	}
}

// WorkloadPassKey is the storage/query key for one canonical identity.
type WorkloadPassKey struct {
	// Namespace is the explicit authority domain.
	Namespace WorkloadPassNamespace
	// IdentityDigest is the canonical workload identity digest.
	IdentityDigest string
}

// NewWorkloadPassKey constructs a typed key. Call Validate before persistence;
// retaining a value type here keeps callers from accidentally using a raw
// namespace string in SQL.
func NewWorkloadPassKey(namespace WorkloadPassNamespace, identityDigest string) WorkloadPassKey {
	return WorkloadPassKey{Namespace: namespace, IdentityDigest: identityDigest}
}

// Validate verifies both the namespace and the canonical identity digest.
func (key WorkloadPassKey) Validate() error {
	if err := key.Namespace.Validate(); err != nil {
		return err
	}
	if !isPrefixedSHA256Digest(key.IdentityDigest) {
		return errors.New("workload PASS key identity digest is invalid")
	}
	return nil
}

// String returns the explicit storage key shape required by the authority.
func (key WorkloadPassKey) String() string {
	return string(key.Namespace) + ":" + key.IdentityDigest
}

// ParseWorkloadPassKey rejects missing/unknown namespaces and malformed
// identity digests instead of treating an unqualified key as local.
func ParseWorkloadPassKey(value string) (WorkloadPassKey, error) {
	namespace, identityDigest, ok := strings.Cut(value, ":")
	if !ok {
		return WorkloadPassKey{}, errors.New("workload PASS key namespace prefix is required")
	}
	key := NewWorkloadPassKey(WorkloadPassNamespace(namespace), identityDigest)
	if err := key.Validate(); err != nil {
		return WorkloadPassKey{}, err
	}
	return key, nil
}

// LocalWorkloadPassEnvironment is the correctness material for a canonical
// host execution. Source path, commit and tree are intentionally absent.
type LocalWorkloadPassEnvironment struct {
	// Platform is the canonical GOOS/GOARCH pair (for example darwin/arm64).
	Platform string `json:"platform"`
	// GOOS is the local host operating-system identity.
	GOOS string `json:"goos"`
	// GOARCH is the local host architecture identity.
	GOARCH string `json:"goarch"`
	// GOAMD64/GOARM64 bind architecture-level compiler selection when set.
	GOAMD64 string `json:"goamd64"`
	GOARM64 string `json:"goarm64"`
	// CGOEnabled is the canonical one-hot CGO setting.
	CGOEnabled string `json:"cgo_enabled"`
	// GOEXPERIMENT, CC and CXX bind the effective Go/C toolchain selection.
	GOEXPERIMENT string `json:"goexperiment"`
	CC           string `json:"cc"`
	CXX          string `json:"cxx"`
	// SDK/OSBuild/GoVersion bind the host SDK and actual compiler version.
	SDK       string `json:"sdk"`
	OSBuild   string `json:"os_build"`
	GoVersion string `json:"go_version"`
	// GoFlags is the canonical workload compiler/test profile.
	GoFlags string `json:"go_flags"`
	// ToolchainClosureDigest binds the complete local toolchain closure.
	ToolchainClosureDigest string `json:"toolchain_closure_digest"`
	// RunnerSemanticPolicy is the versioned local executor policy identifier.
	RunnerSemanticPolicy string `json:"runner_semantic_policy"`
	// BaseRunnerSemanticDigest binds the common receipt host runner closure.
	BaseRunnerSemanticDigest string `json:"base_runner_semantic_digest"`
	// RunnerSemanticDigest binds the exact local executor/runner closure.
	RunnerSemanticDigest string `json:"runner_semantic_digest"`
}

type localWorkloadPassEnvironmentPayload struct {
	SchemaVersion              string `json:"schema_version"`
	Domain                     string `json:"domain"`
	Platform                   string `json:"platform"`
	GOOS                       string `json:"goos"`
	GOARCH                     string `json:"goarch"`
	GOAMD64                    string `json:"goamd64"`
	GOARM64                    string `json:"goarm64"`
	CGOEnabled                 string `json:"cgo_enabled"`
	GOEXPERIMENT               string `json:"goexperiment"`
	CC                         string `json:"cc"`
	CXX                        string `json:"cxx"`
	SDK                        string `json:"sdk"`
	OSBuild                    string `json:"os_build"`
	GoVersion                  string `json:"go_version"`
	GoFlags                    string `json:"go_flags"`
	ToolchainClosureDigest     string `json:"toolchain_closure_digest"`
	RunnerSemanticPolicyDigest string `json:"runner_semantic_policy_digest"`
	BaseRunnerSemanticDigest   string `json:"base_runner_semantic_digest"`
	RunnerSemanticDigest       string `json:"runner_semantic_digest"`
}

type localWorkloadPassHostContextPayload struct {
	Domain                     string `json:"domain"`
	Platform                   string `json:"platform"`
	GOOS                       string `json:"goos"`
	GOARCH                     string `json:"goarch"`
	GOAMD64                    string `json:"goamd64"`
	GOARM64                    string `json:"goarm64"`
	CGOEnabled                 string `json:"cgo_enabled"`
	GOEXPERIMENT               string `json:"goexperiment"`
	CC                         string `json:"cc"`
	CXX                        string `json:"cxx"`
	SDK                        string `json:"sdk"`
	OSBuild                    string `json:"os_build"`
	GoVersion                  string `json:"go_version"`
	ToolchainClosureDigest     string `json:"toolchain_closure_digest"`
	RunnerSemanticPolicyDigest string `json:"runner_semantic_policy_digest"`
	BaseRunnerSemanticDigest   string `json:"base_runner_semantic_digest"`
}

// ValidateLocalWorkloadPassEnvironment is exported for scheduler and CLI
// callers that must fail before touching the executor or SQLite.
func ValidateLocalWorkloadPassEnvironment(environment LocalWorkloadPassEnvironment) error {
	if err := validateLocalWorkloadPassPlatform(environment); err != nil {
		return err
	}
	if err := validateLocalWorkloadPassCompiler(environment); err != nil {
		return err
	}
	return validateLocalWorkloadPassRunner(environment)
}

func validateLocalWorkloadPassPlatform(environment LocalWorkloadPassEnvironment) error {
	if strings.TrimSpace(environment.Platform) == "" || strings.TrimSpace(environment.GOOS) == "" || strings.TrimSpace(environment.GOARCH) == "" {
		return errors.New("local workload PASS platform and GOOS/GOARCH are required")
	}
	if environment.Platform != environment.GOOS+"/"+environment.GOARCH {
		return errors.New("local workload PASS platform does not match GOOS/GOARCH")
	}
	if environment.CGOEnabled != "0" && environment.CGOEnabled != "1" {
		return errors.New("local workload PASS CGO_ENABLED must be 0 or 1")
	}
	return nil
}

func validateLocalWorkloadPassCompiler(environment LocalWorkloadPassEnvironment) error {
	if err := ValidateCanonicalGoFlags(environment.GoFlags); err != nil {
		return fmt.Errorf("local workload PASS GoFlags: %w", err)
	}
	if !isPrefixedSHA256Digest(environment.ToolchainClosureDigest) {
		return errors.New("local workload PASS toolchain closure digest is required")
	}
	return nil
}

func validateLocalWorkloadPassRunner(environment LocalWorkloadPassEnvironment) error {
	if !isPrefixedSHA256Digest(environment.BaseRunnerSemanticDigest) {
		return errors.New("local workload PASS base runner semantic digest is required")
	}
	if !isPrefixedSHA256Digest(environment.RunnerSemanticDigest) {
		return errors.New("local workload PASS runner semantic digest is required")
	}
	if environment.RunnerSemanticPolicy != cicontract.LocalWorkloadRunnerSemanticPolicy {
		return errors.New("local workload PASS runner semantic policy is not canonical")
	}
	return nil
}

// LocalWorkloadPassEnvironmentDigest returns the local-domain digest. The
// runner policy enters as a derived digest, preventing arbitrary policy text
// from becoming trusted identity material.
func LocalWorkloadPassEnvironmentDigest(environment LocalWorkloadPassEnvironment) (string, error) {
	if err := ValidateLocalWorkloadPassEnvironment(environment); err != nil {
		return "", err
	}
	policyDigest := sha256.Sum256([]byte(environment.RunnerSemanticPolicy))
	payload, err := json.Marshal(localWorkloadPassEnvironmentPayload{
		SchemaVersion:              cicontract.LocalWorkloadPassEnvironmentSchemaVersion,
		Domain:                     cicontract.LocalWorkloadPassEnvironmentDomain,
		Platform:                   environment.Platform,
		GOOS:                       environment.GOOS,
		GOARCH:                     environment.GOARCH,
		GOAMD64:                    environment.GOAMD64,
		GOARM64:                    environment.GOARM64,
		CGOEnabled:                 environment.CGOEnabled,
		GOEXPERIMENT:               environment.GOEXPERIMENT,
		CC:                         environment.CC,
		CXX:                        environment.CXX,
		SDK:                        environment.SDK,
		OSBuild:                    environment.OSBuild,
		GoVersion:                  environment.GoVersion,
		GoFlags:                    environment.GoFlags,
		ToolchainClosureDigest:     environment.ToolchainClosureDigest,
		RunnerSemanticPolicyDigest: fmt.Sprintf("sha256:%x", policyDigest),
		BaseRunnerSemanticDigest:   environment.BaseRunnerSemanticDigest,
		RunnerSemanticDigest:       environment.RunnerSemanticDigest,
	})
	if err != nil {
		return "", fmt.Errorf("encode local workload PASS environment: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// LocalWorkloadPassHostContextDigest binds the common host closure while
// deliberately excluding GoFlags, so normal and race entries share one host.
func LocalWorkloadPassHostContextDigest(environment LocalWorkloadPassEnvironment) (string, error) {
	if err := ValidateLocalWorkloadPassEnvironment(environment); err != nil {
		return "", err
	}
	policyDigest := sha256.Sum256([]byte(environment.RunnerSemanticPolicy))
	payload, err := json.Marshal(localWorkloadPassHostContextPayload{
		Domain: cicontract.LocalWorkloadPassHostContextDomain, Platform: environment.Platform,
		GOOS: environment.GOOS, GOARCH: environment.GOARCH, GOAMD64: environment.GOAMD64, GOARM64: environment.GOARM64,
		CGOEnabled: environment.CGOEnabled, GOEXPERIMENT: environment.GOEXPERIMENT, CC: environment.CC, CXX: environment.CXX,
		SDK: environment.SDK, OSBuild: environment.OSBuild, GoVersion: environment.GoVersion,
		ToolchainClosureDigest:     environment.ToolchainClosureDigest,
		RunnerSemanticPolicyDigest: fmt.Sprintf("sha256:%x", policyDigest),
		BaseRunnerSemanticDigest:   environment.BaseRunnerSemanticDigest,
	})
	if err != nil {
		return "", fmt.Errorf("encode local workload host context: %w", err)
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest), nil
}
