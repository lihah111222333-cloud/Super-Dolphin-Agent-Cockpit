package main

import (
	"fmt"
	"os/exec"
	"strings"

	gateclosure "github.com/lihah111222333-cloud/super-dolphin-agent/build/gate/closure"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func runClosureCheck(args []string) error {
	if len(args) != 3 || args[1] != "--tree" || !isClosureAction(args[0]) || strings.TrimSpace(args[2]) == "" {
		return protocolError("closure check, refresh, refresh-dependencies, or provenance requires one --tree <staged-tree-sha> argument")
	}
	switch args[0] {
	case "provenance":
		return writeClosureProvenance(args[2])
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

func isClosureAction(action string) bool {
	switch action {
	case "check", "refresh", "refresh-dependencies", "provenance":
		return true
	default:
		return false
	}
}

// writeClosureProvenance 同时输出已编译生成器身份和精确候选树重建身份。
func writeClosureProvenance(tree string) error {
	repositoryRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return infrastructureError("resolve closure provenance repository root: %v", err)
	}
	compiled, err := gateclosure.GeneratorProvenance()
	if err != nil {
		return infrastructureError("compute compiled closure generator provenance: %v", err)
	}
	candidate, err := gateclosure.GeneratorProvenanceForTree(strings.TrimSpace(string(repositoryRoot)), tree)
	if err != nil {
		return infrastructureError("compute candidate closure generator provenance: %v", err)
	}
	_, err = fmt.Printf("%s %s\n", compiled, candidate)
	return err
}

func runClosureRefresh(tree, operation string, refresh func(string) error) error {
	if err := refresh(tree); err != nil {
		return infrastructureError("%s: %v", operation, err)
	}
	return nil
}
