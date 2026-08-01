package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/frontendcodesizetrusted"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// runFrontendCodeSizeCLI 在精确候选树上检查或刷新前端代码规模基线。
func runFrontendCodeSizeCLI(args []string, stdout io.Writer) error {
	if len(args) == 1 && args[0] == "node-path" {
		node, err := frontendcodesizetrusted.TrustedNodePath()
		if err != nil {
			return frontendCodeSizeExitError(err)
		}
		_, err = fmt.Fprintln(stdout, node)
		return err
	}
	operation, tree, acceptedTree, err := parseFrontendCodeSizeArgs(args)
	if err != nil {
		return err
	}
	root, err := os.Getwd()
	if err != nil {
		return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, fmt.Errorf("resolve current directory: %w", err))
	}
	receipt, err := frontendcodesizetrusted.RunWithAcceptedBaselineReceipt(context.Background(), root, tree, acceptedTree, operation)
	if err != nil {
		return frontendCodeSizeExitError(err)
	}
	_, err = fmt.Fprintf(stdout, "frontend-code-size %s passed for tree %s execution-closure=%s\n", operation, tree, receipt.IdentitySHA256)
	return err
}

// runLocalGuardCLI 分派不依赖 coordinator 的本地守卫命令。
func runLocalGuardCLI(args []string, stdout io.Writer) error {
	if args[0] == "closure" {
		return runClosureCheck(args[1:])
	}
	return runFrontendCodeSizeCLI(args[1:], stdout)
}

// parseFrontendCodeSizeArgs 解析并限制候选树命令的唯一参数面。
func parseFrontendCodeSizeArgs(args []string) (frontendcodesizetrusted.Operation, string, string, error) {
	if len(args) == 0 {
		return "", "", "", protocolError("frontend-code-size requires check or refresh")
	}
	operation := frontendcodesizetrusted.Operation(args[0])
	flags := flag.NewFlagSet("frontend-code-size "+string(operation), flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	tree := flags.String("tree", "", "exact Git tree SHA")
	acceptedTree := flags.String("accepted-tree", "", "accepted baseline Git tree SHA")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *tree == "" || *acceptedTree == "" {
		return "", "", "", protocolError("frontend-code-size %s requires --tree <exact-tree-sha> --accepted-tree <exact-tree-sha>", operation)
	}
	return operation, *tree, *acceptedTree, nil
}

// frontendCodeSizeExitError 将 trusted 包错误映射为 gate 退出码。
func frontendCodeSizeExitError(err error) error {
	var trustedErr *frontendcodesizetrusted.Error
	if errors.As(err, &trustedErr) && trustedErr.Kind == frontendcodesizetrusted.ErrorInput {
		return protocolError("%v", err)
	}
	if errors.As(err, &trustedErr) && trustedErr.Kind == frontendcodesizetrusted.ErrorViolation {
		return gatecontract.WithExitCode(gatecontract.ExitGateViolation, err)
	}
	return gatecontract.WithExitCode(gatecontract.ExitInfrastructure, err)
}
