package main

import (
	"errors"
	"io"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/codemapindex"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/projectmaptrusted"
)

// runCodemapCLI 对显式 Git tree 运行编译进 gate 的代码地图生成器。
func runCodemapCLI(args []string, _ io.Writer) error {
	action, tree, err := parseCodemapCLI(args)
	if err != nil {
		return err
	}
	repository, err := projectMapGitOutput("rev-parse", "--show-toplevel")
	if err != nil {
		return infrastructureError("resolve codemap repository root: %v", err)
	}
	root := strings.TrimSpace(repository)
	switch action {
	case "check":
		err = codemapindex.CheckTree(root, tree)
	case "refresh":
		err = codemapindex.RefreshTree(root, tree)
	}
	if err == nil {
		return nil
	}
	var treeErr *projectmaptrusted.TreeError
	if errors.As(err, &treeErr) {
		return sourceError("codemap %s tree: %v", action, err)
	}
	return gatecontract.WithExitCode(gatecontract.ExitGateViolation, err)
}

func parseCodemapCLI(args []string) (string, string, error) {
	if len(args) != 3 || args[1] != "--tree" || args[0] != "check" && args[0] != "refresh" {
		return "", "", protocolError("codemap check or refresh requires one --tree <exact-tree-sha> argument")
	}
	tree := strings.TrimSpace(args[2])
	if tree == "" || tree != args[2] {
		return "", "", protocolError("codemap exact tree sha is required")
	}
	return args[0], tree, nil
}
