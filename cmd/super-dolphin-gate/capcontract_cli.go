package main

import (
	"errors"
	"io"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
)

// runCapabilityContractCLI 对显式 Git tree 运行受信 capability manifest 生成器。
func runCapabilityContractCLI(args []string, _ io.Writer) error {
	action, tree, err := parseCapabilityContractCLI(args)
	if err != nil {
		return err
	}
	repository, err := projectMapGitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return infrastructureError("resolve capability-contract repository root: %v", err)
	}
	root := strings.TrimSpace(repository)
	switch action {
	case "check":
		err = capcontract.CheckTree(root, tree)
	case "refresh":
		err = capcontract.RefreshTree(root, tree)
	default:
		return protocolError("unknown capability-contract action %q", action)
	}
	if err == nil {
		return nil
	}
	var treeErr *projectmaptrusted.TreeError
	if errors.As(err, &treeErr) {
		return sourceError("capability-contract %s tree: %v", action, err)
	}
	return gatecontract.WithExitCode(gatecontract.ExitGateViolation, err)
}

// parseCapabilityContractCLI 解析 capability-contract 的操作和精确 tree 参数。
func parseCapabilityContractCLI(args []string) (string, string, error) {
	if len(args) != 3 || args[1] != "--tree" || args[0] != "check" && args[0] != "refresh" {
		return "", "", protocolError("capability-contract check or refresh requires one --tree <exact-tree-sha> argument")
	}
	tree := strings.TrimSpace(args[2])
	if tree == "" || tree != args[2] {
		return "", "", protocolError("capability-contract exact tree sha is required")
	}
	return args[0], tree, nil
}
