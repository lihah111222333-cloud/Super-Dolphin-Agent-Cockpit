//go:build !windows

package main

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

func validateRuntimeServerGoplsRootCohortPlatform() error {
	return nil
}

// runtimeServerAcquireGoplsRootLease 为共享 gopls daemon 获取 durable root lease。
func runtimeServerAcquireGoplsRootLease(command multilsp.ServerCommand, serverBinary, dir string, env []string, controller multilsp.GoplsRootCohortController) (*multilsp.GoplsRootCohortLease, error) {
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return nil, nil
	}
	config, err := runtimeServerGoplsRootCohortConfig(command, serverBinary, dir, env)
	if err != nil {
		return nil, err
	}
	if controller == nil {
		return nil, fmt.Errorf("%w for cohort %s", multilsp.ErrGoplsRootCohortDurabilityUnsupported, config.CohortID)
	}
	lease, err := controller.AcquireLease(config)
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

// runtimeServerNewPlatformGoplsRootCohortController 为共享 daemon 创建唯一 durable 生命周期 owner。
func runtimeServerNewPlatformGoplsRootCohortController(command multilsp.ServerCommand) (multilsp.GoplsRootCohortController, error) {
	if !runtimeServerUsesSharedGoplsDaemon(command) {
		return nil, nil
	}
	return runtimeServerNewDurableGoplsRootCohortController()
}
