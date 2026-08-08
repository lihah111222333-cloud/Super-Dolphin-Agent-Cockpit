package main

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedlauncher"
)

type launcherCommand struct {
	action     string
	repository string
	tree       string
	receipt    string
}

// runLauncherCLI 校验当前宿主 launcher 是否绑定精确源码树和构建身份。
func runLauncherCLI(args []string) error {
	command, err := parseLauncherCommand(args)
	if err != nil {
		return err
	}
	linked, err := linkedLauncherIdentity()
	if err != nil {
		return infrastructureError("trusted launcher build identity is not linked: %v", err)
	}
	return verifyLauncherRepository(command, linked)
}

// parseLauncherCommand 严格解析 launcher 的固定子命令。
func parseLauncherCommand(args []string) (launcherCommand, error) {
	if len(args) == 0 {
		return launcherCommand{}, protocolError("launcher requires the verify subcommand")
	}
	action := args[0]
	if action != "verify" {
		return launcherCommand{}, protocolError("launcher requires the verify subcommand")
	}
	command, err := parseLauncherFlags(action, args[1:])
	if err != nil {
		return launcherCommand{}, err
	}
	return command, validateLauncherCommand(command)
}

// parseLauncherFlags 解析 launcher 参数并拒绝位置参数。
func parseLauncherFlags(action string, args []string) (launcherCommand, error) {
	flags := flag.NewFlagSet("launcher "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repository := flags.String("repository", "", "exact-tree repository root")
	tree := flags.String("tree", "", "exact staged tree")
	receipt := flags.String("receipt", "", "launcher receipt path")
	if err := flags.Parse(args); err != nil {
		return launcherCommand{}, protocolError("launcher verification arguments are invalid")
	}
	if flags.NArg() != 0 {
		return launcherCommand{}, protocolError("launcher verification arguments are invalid")
	}
	return launcherCommand{action: action, repository: *repository, tree: *tree, receipt: *receipt}, nil
}

// validateLauncherCommand 校验每个子命令唯一允许的参数集合。
func validateLauncherCommand(command launcherCommand) error {
	if command.receipt == "" {
		return protocolError("launcher verification requires --receipt")
	}
	if command.action == "verify" && (command.repository == "" || command.tree == "") {
		return protocolError("launcher verify requires --repository, --tree, and --receipt")
	}
	return nil
}

// linkedGateCLIIdentity 返回普通 Gate 或 launcher payload 内的源码与工具链身份。
func linkedGateCLIIdentity() (string, string, error) {
	if trustedlauncher.IsLinkedIdentityPayload(gateSourceDigest) {
		identity, err := linkedLauncherIdentity()
		return identity.SourceSHA256, identity.ToolchainSHA256, err
	}
	if gateSourceDigest == "" || gateToolchainDigest == "" {
		return "", "", errors.New("gate CLI build identity is not linked")
	}
	return gateSourceDigest, gateToolchainDigest, nil
}

// linkedLauncherIdentity 解码 linker payload 并绑定独立的参数摘要。
func linkedLauncherIdentity() (trustedlauncher.LinkedIdentity, error) {
	return trustedlauncher.DecodeLinkedIdentity(gateSourceDigest, gateToolchainDigest)
}

// verifyLauncherRepository 校验仓库精确树和 launcher 回执。
func verifyLauncherRepository(command launcherCommand, linked trustedlauncher.LinkedIdentity) error {
	if err := trustedlauncher.Verify(context.Background(), trustedlauncher.VerifyOptions{
		RepositoryRoot: command.repository,
		Tree:           command.tree,
		ReceiptPath:    command.receipt,
		Linked:         linked,
	}); err != nil {
		return infrastructureError("verify trusted launcher: %v", err)
	}
	return nil
}
