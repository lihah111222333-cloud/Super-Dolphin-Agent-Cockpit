package remoteportable

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"unicode"
)

// RepositoryWorkloadPlatform is a typed workload platform.
type RepositoryWorkloadPlatform string

// RepositoryWorkloadRuntime is a typed runtime capability.
type RepositoryWorkloadRuntime string

const (
	// RepositoryWorkloadKindGoTest identifies an exact Go test workload.
	RepositoryWorkloadKindGoTest = "go_test"
	// RepositoryWorkloadKindGoPackage identifies a whole Go package workload.
	RepositoryWorkloadKindGoPackage = "go_package"
	// RepositoryWorkloadPlatformLinuxAMD64 identifies the Ubuntu platform.
	RepositoryWorkloadPlatformLinuxAMD64 RepositoryWorkloadPlatform = "linux/amd64"
	// RepositoryWorkloadPlatformWindowsAMD64 identifies the Windows platform.
	RepositoryWorkloadPlatformWindowsAMD64 RepositoryWorkloadPlatform = "windows/amd64"
	// RepositoryWorkloadRuntimeUbuntuContainer identifies the Ubuntu runtime capability.
	RepositoryWorkloadRuntimeUbuntuContainer RepositoryWorkloadRuntime = "ubuntu_container"
	// RepositoryWorkloadRuntimeUbuntuWineContainer 标识 Linux 宿主内的 Ubuntu Wine 容器运行时。
	RepositoryWorkloadRuntimeUbuntuWineContainer RepositoryWorkloadRuntime = "ubuntu_wine_container"
)

// RepositoryWorkloadExecutionPolicy identifies a workload execution policy.
type RepositoryWorkloadExecutionPolicy string

const (
	// RepositoryWorkloadExecutionPolicyNormal identifies a normal execution.
	RepositoryWorkloadExecutionPolicyNormal RepositoryWorkloadExecutionPolicy = "normal"
	// RepositoryWorkloadExecutionPolicyRace identifies race execution.
	RepositoryWorkloadExecutionPolicyRace RepositoryWorkloadExecutionPolicy = "race"
)

// RepositoryWorkloadModule is a provider-neutral module declaration used by inventory.
type RepositoryWorkloadModule struct {
	Root       string `json:"root"`
	ModulePath string `json:"module_path"`
}

func validatePortableIdentifier(value, label string) error {
	if value == "" || strings.TrimSpace(value) != value || hasPortableControl(value) || strings.Contains(value, "\\") {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func validatePortableOperationID(value string) error {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return errors.New("operation_id must be 64 lowercase hex characters")
	}
	for _, runeValue := range value {
		if runeValue < '0' || runeValue > '9' && runeValue < 'a' || runeValue > 'f' {
			return errors.New("operation_id must be 64 lowercase hex characters")
		}
	}
	return nil
}

func validPortableToken(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || hasPortableControl(value) {
		return false
	}
	return !isPortableReservedToken(value)
}

func isPortableReservedToken(value string) bool {
	return value == "default" || value == "cwd" || value == "HEAD"
}

func hasPortableControl(value string) bool {
	for _, runeValue := range value {
		if unicode.IsControl(runeValue) {
			return true
		}
	}
	return false
}

func validPortableOID(value string) bool {
	if len(value) != 40 && len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || value != strings.ToLower(value) {
		return false
	}
	for _, byteValue := range decoded {
		if byteValue != 0 {
			return true
		}
	}
	return false
}

func validPortableDigest(value string) bool {
	encoded, found := strings.CutPrefix(value, "sha256:")
	return found && len(encoded) == sha256.Size*2 && validPortableOID(encoded)
}

func validPortableRepoRelativePath(value string) bool {
	if !validPortableToken(value) || strings.HasPrefix(value, "/") || strings.ContainsRune(value, '\\') {
		return false
	}
	return path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func validPortableOIDForFormat(value, objectFormat string) bool {
	want := 40
	if objectFormat == "sha256" {
		want = 64
	}
	return len(value) == want && validPortableOID(value)
}

func portableBytesDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func portableInterfaceNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func validatePortableModules(modules []RepositoryWorkloadModule) error {
	if len(modules) == 0 {
		return errors.New("portable manifest modules are required")
	}
	seenRoots := make(map[string]struct{}, len(modules))
	seenPaths := make(map[string]struct{}, len(modules))
	for index, module := range modules {
		if err := validatePortableModule(module, index, seenRoots, seenPaths); err != nil {
			return err
		}
		seenRoots[module.Root], seenPaths[module.ModulePath] = struct{}{}, struct{}{}
		if index > 0 && modules[index-1].Root >= module.Root {
			return errors.New("portable manifest modules are not sorted")
		}
	}
	return nil
}

func validatePortableModule(module RepositoryWorkloadModule, index int, seenRoots, seenPaths map[string]struct{}) error {
	if module.Root != "." && !validPortableRepoRelativePath(module.Root) {
		return fmt.Errorf("portable module %d root is invalid", index)
	}
	if !validPortableToken(module.ModulePath) || path.IsAbs(module.ModulePath) || strings.Contains(module.ModulePath, "\\") {
		return fmt.Errorf("portable module %d module_path is invalid", index)
	}
	if _, duplicate := seenRoots[module.Root]; duplicate {
		return errors.New("portable manifest module roots are duplicated")
	}
	if _, duplicate := seenPaths[module.ModulePath]; duplicate {
		return errors.New("portable manifest module paths are duplicated")
	}
	return nil
}

func validatePortableExcludedModules(modules []string) error {
	if modules == nil {
		return errors.New("portable manifest excluded_nested_modules is missing")
	}
	for index, module := range modules {
		if !validPortableRepoRelativePath(module) || index > 0 && modules[index-1] >= module {
			return errors.New("portable manifest excluded_nested_modules are invalid")
		}
	}
	return nil
}
