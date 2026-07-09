package app

import (
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type dependencyContract struct {
	profile contract.DependencyProfile
}

func newDependencyContract(profile contract.DependencyProfile) dependencyContract {
	return dependencyContract{profile: profile}
}

// Require 按 dependency profile 校验 app 依赖是否允许缺席。
// production 缺关键依赖必须 fail-fast，desktop/test 只允许清单内的显式外部依赖。
func (c dependencyContract) Require(name string, value any) error {
	return contract.RequireDependency(name, c.profile, value)
}

func appDependencyProfile(dependency contract.DependencyConfig, cfg *contract.Config) (contract.DependencyProfile, error) {
	profile := dependency.Profile
	if strings.TrimSpace(string(profile)) == "" && cfg != nil {
		profile = cfg.Dependency.Profile
	}
	switch profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileProduction, contract.DependencyProfileTest:
		return profile, nil
	case "":
		return "", fmt.Errorf("app dependency profile is required")
	default:
		return "", fmt.Errorf("app dependency profile %q is not supported", profile)
	}
}

func dependencyUnsupported(name string, profile contract.DependencyProfile) error {
	return contract.MissingDependencyModeError(name, profile)
}

func dependencyDeferred(name string, profile contract.DependencyProfile) error {
	return contract.NewDependencyModeError(contract.ErrDependencyDeferred, name, profile)
}
