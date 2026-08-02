package remoteci

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

const runtimeDepsLockPath = "build/gate/runtime-deps.lock"

type toolchainLock struct {
	SchemaVersion      string             `json:"schema_version"`
	BuildKitVersion    string             `json:"buildkit_version"`
	BuildKitImage      string             `json:"buildkit_image"`
	DockerfileFrontend string             `json:"dockerfile_frontend"`
	SourceDateEpoch    string             `json:"source_date_epoch"`
	TargetPlatforms    []string           `json:"target_platforms"`
	BaseImages         []lockedBaseImage  `json:"base_images"`
	DependencySources  []string           `json:"dependency_sources"`
	RuntimeDepsLock    string             `json:"runtime_deps_lock"`
	RuntimeTools       lockedRuntimeTools `json:"runtime_tools"`
	NetworkPolicy      string             `json:"network_policy"`
}

type lockedRuntimeTools struct {
	NodeVersion, NPMVersion, PythonVersion, Ripgrep, Sqruff, Gopls, SQLC string
	SqruffArtifacts                                                      []lockedSqruffArtifact `json:"sqruff_artifacts"`
	NPMPackages                                                          []string               `json:"npm_lsp_packages"`
}
type lockedSqruffArtifact struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
}
type lockedBaseImage struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

var runtimeDepsPlatforms = []string{"linux/amd64", "linux/arm64"}

func validateToolchainVersions(lock toolchainLock) error {
	if lock.SchemaVersion != "1" {
		return fmt.Errorf("toolchain schema version %q is unsupported", lock.SchemaVersion)
	}
	if err := validateBuildKitVersion(lock.BuildKitVersion); err != nil {
		return fmt.Errorf("validate locked BuildKit version: %w", err)
	}
	if err := validateBuildKitImageReference(lock.BuildKitImage); err != nil {
		return fmt.Errorf("validate locked BuildKit image: %w", err)
	}
	if lock.DockerfileFrontend != "builtin:dockerfile.v1" {
		return errors.New("Dockerfile frontend must be the locked builtin:dockerfile.v1 frontend")
	}
	return validateSourceDateEpoch(lock.SourceDateEpoch)
}

func validateBuildKitVersion(version string) error {
	if !strings.HasPrefix(version, "v") {
		return errors.New("BuildKit version must be v-prefixed")
	}
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return errors.New("BuildKit version must contain major, minor, and patch components")
	}
	for _, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return errors.New("BuildKit version must contain only decimal components")
		}
	}
	return nil
}

func validateBuildKitImageReference(reference string) error {
	const prefix = "mirror.gcr.io/moby/buildkit@"
	if !strings.HasPrefix(reference, prefix) {
		return errors.New("BuildKit image must use the canonical mirror repository")
	}
	return validateDigest("BuildKit image digest", strings.TrimPrefix(reference, prefix))
}

func loadRuntimeDepsLock(closure map[string]sourceexport.TreeEntry, platform string) (runtimeDepsLock, error) {
	entry, exists := closure[runtimeDepsLockPath]
	if !exists {
		return runtimeDepsLock{}, fmt.Errorf("candidate input closure is missing %s", runtimeDepsLockPath)
	}
	var lock runtimeDepsLock
	if err := decodeStrictJSON(entry.Data, &lock); err != nil {
		return runtimeDepsLock{}, fmt.Errorf("decode runtime dependencies lock: %w", err)
	}
	if err := validateRuntimeDepsLock(lock, platform, closure); err != nil {
		return runtimeDepsLock{}, err
	}
	return lock, nil
}
func validateRuntimeDepsLock(lock runtimeDepsLock, platform string, closure map[string]sourceexport.TreeEntry) error {
	if lock.SchemaVersion != "13" || lock.BuildMode != "node-local" || lock.CacheScope != "node" {
		return errors.New("runtime dependencies lock header is invalid")
	}
	if !slices.Contains(runtimeDepsPlatforms, platform) {
		return fmt.Errorf("runtime dependencies target platform %q is unsupported", platform)
	}
	return validateRuntimeDepsClosure(lock, closure)
}
func validateLockedDependencies(lock toolchainLock, closure map[string]sourceexport.TreeEntry) error {
	if lock.RuntimeDepsLock != runtimeDepsLockPath {
		return fmt.Errorf("runtime dependencies lock must be %q", runtimeDepsLockPath)
	}
	if _, ok := closure[runtimeDepsLockPath]; !ok {
		return errors.New("runtime dependencies lock is outside the input closure")
	}
	tools := lock.RuntimeTools
	if tools.NodeVersion == "" || tools.NPMVersion == "" || tools.PythonVersion == "" || tools.Ripgrep == "" || tools.Sqruff == "" || tools.Gopls == "" || tools.SQLC == "" {
		return errors.New("runtime tool versions must all be locked")
	}
	if err := validateLockedSqruffArtifacts(tools); err != nil {
		return err
	}
	if err := validateDependencySources(lock.DependencySources, closure); err != nil {
		return err
	}
	if lock.NetworkPolicy != "none" && lock.NetworkPolicy != "locked-dependencies" {
		return fmt.Errorf("network policy %q is not permitted", lock.NetworkPolicy)
	}
	return nil
}
func validateLockedSqruffArtifacts(tools lockedRuntimeTools) error {
	if len(tools.SqruffArtifacts) != len(runtimeDepsPlatforms) {
		return errors.New("sqruff artifacts must contain both target platforms")
	}
	for index, platform := range runtimeDepsPlatforms {
		artifact := tools.SqruffArtifacts[index]
		if artifact.Platform != platform || len(artifact.SHA256) != sha256.Size*2 {
			return fmt.Errorf("sqruff artifact for %s is not canonically locked", platform)
		}
		decoded, err := hex.DecodeString(artifact.SHA256)
		if err != nil || hex.EncodeToString(decoded) != artifact.SHA256 {
			return fmt.Errorf("sqruff artifact SHA-256 for %s is not canonical", platform)
		}
	}
	return nil
}
func validateDependencySources(dependencies []string, closure map[string]sourceexport.TreeEntry) error {
	if err := validateSortedUnique("dependency sources", dependencies); err != nil {
		return err
	}
	for _, dependency := range dependencies {
		if _, exists := closure[dependency]; !exists {
			return fmt.Errorf("locked dependency source %q is outside the input closure", dependency)
		}
	}
	return nil
}
