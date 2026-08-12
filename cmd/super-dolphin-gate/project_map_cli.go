package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
)

// runProjectMapCLI 对显式 Git tree 运行编译进 gate 的可信项目地图生成器。
func runProjectMapCLI(args []string, stdout io.Writer) error {
	action, tree, err := parseProjectMapCLI(args)
	if err != nil {
		return err
	}
	repository, err := projectMapGitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return infrastructureError("resolve project-map repository root: %v", err)
	}
	root := strings.TrimSpace(repository)
	tree, err = resolveProjectMapCLITree(root, tree, args)
	if err != nil {
		return err
	}

	switch action {
	case "check":
		err = projectmaptrusted.CheckTree(root, tree, stdout)
	case "refresh":
		err = projectmaptrusted.RefreshTree(root, tree, stdout)
	default:
		return protocolError("unknown project-map action %q", action)
	}
	if err == nil {
		return nil
	}
	return classifyProjectMapError(action, err)
}

// resolveProjectMapCLITree 将外层 index 或已验证 exact clone 的 detached HEAD
// 投影为只读 tree；Git 解析失败立即阻断，不回退到工作区内容。
func resolveProjectMapCLITree(root, tree string, args []string) (string, error) {
	if len(args) != 2 {
		return tree, nil
	}
	gitArgs := []string{"-C", root, "write-tree"}
	if args[1] == "--tree-from-head" {
		gitArgs = []string{"-C", root, "rev-parse", "--verify", "HEAD^{tree}"}
	}
	resolved, err := projectMapGitOutput(gitArgs...)
	if err != nil {
		return "", infrastructureError("resolve project-map exact tree: %v", err)
	}
	return strings.TrimSpace(resolved), nil
}

func parseProjectMapCLI(args []string) (string, string, error) {
	if projectMapDynamicTreeArgs(args) {
		return args[0], "", nil
	}
	if len(args) != 3 || args[1] != "--tree" || args[0] != "check" && args[0] != "refresh" {
		return "", "", protocolError(
			"project-map check or refresh requires one --tree <exact-tree-sha> argument",
		)
	}
	tree := strings.TrimSpace(args[2])
	if tree == "" || tree != args[2] {
		return "", "", protocolError("project-map exact tree sha is required")
	}
	return args[0], tree, nil
}

func projectMapDynamicTreeArgs(args []string) bool {
	if len(args) != 2 {
		return false
	}
	return args[0] == "check" && (args[1] == "--tree-from-index" || args[1] == "--tree-from-head")
}

func classifyProjectMapError(action string, err error) error {
	var treeErr *projectmaptrusted.TreeError
	if errors.As(err, &treeErr) {
		return sourceError("project-map %s tree: %v", action, err)
	}
	var candidateErr *projectmaptrusted.CandidateError
	var generatorErr *projectmaptrusted.GeneratorError
	if errors.As(err, &candidateErr) ||
		errors.As(err, &generatorErr) ||
		errors.Is(err, projectmaptrusted.ErrManagedOutputsModified) {
		return gatecontract.WithExitCode(
			gatecontract.ExitGateViolation,
			fmt.Errorf("project-map %s: %w", action, err),
		)
	}
	return infrastructureError("project-map %s: %v", action, err)
}

func projectMapGitOutput(args ...string) (string, error) {
	command := exec.Command("git", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, detail)
	}
	return string(output), nil
}
