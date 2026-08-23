package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedlauncher"
	catalog "github.com/lihah111222333-cloud/super-dolphin-agent/scripts/mcp_lsp_workload_catalog"
)

// ownedGateRunners 构造 release、工作流所有者的 runner。
func ownedGateRunners(plan gatePlan) map[string]gateRunner {
	return map[string]gateRunner{
		"backend:test-integrity": {run: func() error {
			return runTrustedOwnedGateCommand(plan, "go", "test", "./internal/guards", "-count=1")
		}},
		"workflow:actionlint": {run: func() error {
			return runTrustedOwnedGateCommand(plan, "make", "actionlint")
		}},
		"release:semantic-guards": {run: func() error {
			return runTrustedOwnedGateCommand(plan, "go", "test", "./scripts", "-count=1")
		}},
		"mcp-lsp:catalog": {run: func() error {
			return runMcpLSPCatalogGuard(plan)
		}},
		"mcp-lsp:idle-quick": {run: func() error {
			return runMcpLSPQuickReleaseAuthority(plan)
		}},
	}
}

func runTrustedOwnedGateCommand(plan gatePlan, tool string, arguments ...string) error {
	var executable string
	var err error
	switch tool {
	case "go":
		executable, err = gateprivate.CanonicalRuntimeGoExecutable()
	case "git":
		executable, err = gateprivate.TrustedGitBinary()
	case "make":
		executable, err = gateprivate.CanonicalRootExecutable("owned gate make", "/usr/bin/make")
	default:
		return fmt.Errorf("unsupported owned gate tool %q", tool)
	}
	if err != nil {
		return err
	}
	environment := []string{
		"PATH=" + filepath.Dir(executable) + ":/usr/bin:/bin:/usr/sbin:/sbin",
		"GOENV=off", "GOFLAGS=", "GOTOOLCHAIN=local", "GOWORK=off",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_REPLACE_OBJECTS=1", "LC_ALL=C",
	}
	return runCommandWithEnvironment(gateExecutionDir(plan, ""), executable, environment, arguments...)
}

func runMcpLSPCatalogGuard(plan gatePlan) error {
	if !catalogGuardHostSupported(runtime.GOOS) {
		return fmt.Errorf("mcp-lsp catalog release guard is N/V on Windows: trusted POSIX host entry is unavailable")
	}
	installRoot, err := trustedlauncher.CurrentUserInstallRoot()
	if err != nil {
		return fmt.Errorf("resolve trusted candidate Gate install root: %w", err)
	}
	ctx, cancel := gateprivate.WithTimeout(context.Background(), 10*time.Minute)
	result, err := trustedlauncher.Build(ctx, trustedlauncher.BuildOptions{
		RepositoryRoot: gateExecutionDir(plan, ""), Tree: plan.ReviewObject.CandidateTree, InstallRoot: installRoot,
	})
	cancel()
	if err != nil {
		return fmt.Errorf("build trusted candidate Gate for mcp-lsp catalog: %w", err)
	}
	return runCommandWithEnvironment(gateExecutionDir(plan, ""), result.BinaryPath, []string{"LC_ALL=C"},
		"worker", "mcp-lsp-catalog-guard", "--repository", gateExecutionDir(plan, ""), "--tree", plan.ReviewObject.CandidateTree)
}

func catalogGuardHostSupported(goos string) bool {
	return goos != "windows"
}

// runMcpLSPQuickReleaseAuthority 从 exact candidate catalog 校验 release authority。
func runMcpLSPQuickReleaseAuthority(plan gatePlan) error {
	document, err := catalog.LoadAt(gateExecutionDir(plan, ""), plan.ReviewObject.CandidateTree)
	if err != nil {
		return fmt.Errorf("load mcp-lsp quick release authority: %w", err)
	}
	workload, err := document.Find("mcp-lsp-idle-quick")
	if err != nil {
		return err
	}
	return requireMcpLSPQuickReleaseAuthority(workload)
}

func requireMcpLSPQuickReleaseAuthority(workload catalog.Workload) error {
	if err := catalog.RequireIdleQuickReleaseRole(workload); err != nil {
		return err
	}
	return catalog.RequireRemoteCompletionAuthority(workload)
}
