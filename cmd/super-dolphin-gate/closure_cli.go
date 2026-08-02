package main

import (
	"os/exec"
	"strings"

	gateclosure "github.com/lihah111222333-cloud/super-dolphin-agent/build/gate/closure"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func runClosureCheck(args []string) error {
	if len(args) != 3 || args[1] != "--tree" || (args[0] != "check" && args[0] != "refresh" && args[0] != "refresh-dependencies") || strings.TrimSpace(args[2]) == "" {
		return protocolError("closure check, refresh, or refresh-dependencies requires one --tree <staged-tree-sha> argument")
	}
	switch args[0] {
	case "refresh-dependencies":
		return runClosureRefresh(args[2], "refresh gate-image dependency closure", gateclosure.RefreshDependencyClosure)
	case "refresh":
		return runClosureRefresh(args[2], "refresh gate-image closure", func(tree string) error { return gateclosure.Generate(tree, false) })
	default:
		repositoryRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			return infrastructureError("resolve closure repository root: %v", err)
		}
		if err := gateclosure.CheckTree(strings.TrimSpace(string(repositoryRoot)), args[2]); err != nil {
			return gatecontract.WithExitCode(gatecontract.ExitGateViolation, err)
		}
		return nil
	}
}

func runClosureRefresh(tree, operation string, refresh func(string) error) error {
	if err := refresh(tree); err != nil {
		return infrastructureError("%s: %v", operation, err)
	}
	return nil
}
