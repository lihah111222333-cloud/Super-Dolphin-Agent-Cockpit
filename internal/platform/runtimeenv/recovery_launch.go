package runtimeenv

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const (
	updateTransactionRootEnv     = "SUPER_DOLPHIN_UPDATE_TRANSACTION_ROOT"
	updateTransactionIDEnv       = "SUPER_DOLPHIN_UPDATE_TRANSACTION_ID"
	updateExecutableIDEnv        = "SUPER_DOLPHIN_UPDATE_EXECUTABLE_IDENTITY"
	updateExecutableSHAEnv       = "SUPER_DOLPHIN_UPDATE_EXECUTABLE_SHA256"
	updateTerminationEndpointEnv = "SUPER_DOLPHIN_UPDATE_TERMINATION_ENDPOINT"
	updateTerminationTokenEnv    = "SUPER_DOLPHIN_UPDATE_TERMINATION_TOKEN"
)

// RecoveryLaunch 是 early selector 可读取的 frozen probation 启动契约。
type RecoveryLaunch struct {
	TransactionRoot     string
	TransactionID       string
	ExecutableIdentity  string
	ExecutableSHA256    string
	TerminationEndpoint string
	TerminationToken    string
	ContractPresent     bool
}

// ResolveRecoveryLaunch 在任何 normal runtime 解析前读取 frozen probation contract。
func ResolveRecoveryLaunch(executable string, environ []string) (RecoveryLaunch, error) {
	env := environmentMap(environ)
	launch, err := parseRecoveryContract(env)
	if err != nil {
		return RecoveryLaunch{}, err
	}
	if launch.TransactionRoot == "" {
		launch.TransactionRoot = packagedTransactionRoot(executable)
	}
	if err := validateTransactionRoot(launch.TransactionRoot); err != nil {
		return RecoveryLaunch{}, err
	}
	return launch, nil
}

// AppendRecoveryLaunchEnvironment 附加完整 frozen contract，拒绝部分或重复字段。
func AppendRecoveryLaunchEnvironment(base []string, launch RecoveryLaunch) ([]string, error) {
	if !launch.ContractPresent {
		return nil, errors.New("probation launch contract must be present")
	}
	if _, err := parseRecoveryContract(map[string]string{
		updateTransactionRootEnv:     launch.TransactionRoot,
		updateTransactionIDEnv:       launch.TransactionID,
		updateExecutableIDEnv:        launch.ExecutableIdentity,
		updateExecutableSHAEnv:       launch.ExecutableSHA256,
		updateTerminationEndpointEnv: launch.TerminationEndpoint,
		updateTerminationTokenEnv:    launch.TerminationToken,
	}); err != nil {
		return nil, err
	}
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			return nil, fmt.Errorf("invalid environment entry %q", entry)
		}
		switch name {
		case updateTransactionRootEnv, updateTransactionIDEnv, updateExecutableIDEnv, updateExecutableSHAEnv,
			updateTerminationEndpointEnv, updateTerminationTokenEnv:
			return nil, fmt.Errorf("duplicate probation launch field %s", name)
		}
	}
	return append(base,
		updateTransactionRootEnv+"="+launch.TransactionRoot,
		updateTransactionIDEnv+"="+launch.TransactionID,
		updateExecutableIDEnv+"="+launch.ExecutableIdentity,
		updateExecutableSHAEnv+"="+launch.ExecutableSHA256,
		updateTerminationEndpointEnv+"="+launch.TerminationEndpoint,
		updateTerminationTokenEnv+"="+launch.TerminationToken,
	), nil
}

func parseRecoveryContract(env map[string]string) (RecoveryLaunch, error) {
	launch := RecoveryLaunch{
		TransactionRoot:     strings.TrimSpace(env[updateTransactionRootEnv]),
		TransactionID:       strings.TrimSpace(env[updateTransactionIDEnv]),
		ExecutableIdentity:  strings.TrimSpace(env[updateExecutableIDEnv]),
		ExecutableSHA256:    strings.TrimSpace(env[updateExecutableSHAEnv]),
		TerminationEndpoint: strings.TrimSpace(env[updateTerminationEndpointEnv]),
		TerminationToken:    strings.TrimSpace(env[updateTerminationTokenEnv]),
	}
	values := []string{
		launch.TransactionRoot, launch.TransactionID, launch.ExecutableIdentity,
		launch.ExecutableSHA256, launch.TerminationEndpoint, launch.TerminationToken,
	}
	present := nonEmptyCount(values)
	launch.ContractPresent = present > 0
	if present > 0 && present != len(values) {
		return RecoveryLaunch{}, errors.New("probation launch contract is partial")
	}
	if launch.ContractPresent {
		if err := validateTerminationContract(launch); err != nil {
			return RecoveryLaunch{}, err
		}
	}
	return launch, nil
}

func nonEmptyCount(values []string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

// validateTerminationContract 校验协作终止 endpoint 与随机 token 的完整格式。
func validateTerminationContract(launch RecoveryLaunch) error {
	if !filepath.IsAbs(launch.TerminationEndpoint) || filepath.Clean(launch.TerminationEndpoint) != launch.TerminationEndpoint {
		return errors.New("probation termination endpoint must be absolute and clean")
	}
	if len(launch.TerminationToken) != 64 {
		return errors.New("probation termination token must be 64 lowercase hex characters")
	}
	for _, value := range launch.TerminationToken {
		if value < '0' || value > '9' && value < 'a' || value > 'f' {
			return errors.New("probation termination token must be 64 lowercase hex characters")
		}
	}
	return nil
}

func validateTransactionRoot(root string) error {
	if root == "" {
		return nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("probation transaction root must be absolute and clean: %q", root)
	}
	return nil
}

// DetachedCommandEnvironment 只保留 Guard/Recovery detached command 的 frozen 环境变量。
func DetachedCommandEnvironment(environ []string) ([]string, error) {
	result := make([]string, 0, 5)
	seen := make(map[string]struct{}, 5)
	for _, entry := range environ {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("invalid environment entry %q", entry)
		}
		if !isDetachedEnvironmentAllowed(name) {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate detached environment variable %s", name)
		}
		seen[name] = struct{}{}
		result = append(result, entry)
	}
	return result, nil
}

// isDetachedEnvironmentAllowed 返回 detached command 可继承的 frozen 环境变量。
func isDetachedEnvironmentAllowed(name string) bool {
	switch name {
	case "HOME", "LANG", "LC_ALL", "PATH", "TMPDIR":
		return true
	default:
		return false
	}
}

func packagedTransactionRoot(executable string) string {
	executable = filepath.Clean(strings.TrimSpace(executable))
	macOSDir := filepath.Dir(executable)
	contentsDir := filepath.Dir(macOSDir)
	appRoot := filepath.Dir(contentsDir)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contentsDir) != "Contents" || filepath.Ext(appRoot) != ".app" {
		return ""
	}
	return filepath.Join(filepath.Dir(appRoot), ".super-dolphin-update-transactions")
}
