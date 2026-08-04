package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type frontendE2ECommand struct {
	script string
}

type frontendE2EMatch struct {
	pathFragment string
	script       string
}

var allFrontendE2ECommands = []frontendE2ECommand{
	{script: "test:e2e:business"},
	{script: "test:e2e:desktop-wide"},
	{script: "smoke:desktop:failure"},
	{script: "smoke:desktop:ux"},
}

var frontendE2EMatches = []frontendE2EMatch{
	{pathFragment: "business-flows", script: "test:e2e:business"},
	{pathFragment: "desktop-wide", script: "test:e2e:desktop-wide"},
	{pathFragment: "desktop-failure", script: "smoke:desktop:failure"},
	{pathFragment: "desktop-ux", script: "smoke:desktop:ux"},
	{pathFragment: "playwright.desktop.config.js", script: "smoke:desktop:ux"},
}

// ownedGateRunners 构造 release、工作流、夜间协议及 E2E 所有者的不可缓存 runner。
func ownedGateRunners(plan gatePlan) map[string]gateRunner {
	return map[string]gateRunner{
		"backend:test-integrity": {run: func() error {
			return runCommand("", "go", "test", "./internal/guards", "-count=1")
		}},
		"frontend:e2e": {run: func() error {
			return runFrontendE2E(plan)
		}},
		"workflow:actionlint": {run: func() error {
			return runCommand("", "make", "actionlint")
		}},
		"release:semantic-guards": {run: func() error {
			return runCommand("", "go", "test", "./scripts", "-count=1")
		}},
		"nightly-protocol:check": {run: func() error {
			return runCommand("", "go", "run", "./scripts/nightly_protocol_validator")
		}},
		"mcp-lsp:catalog": {run: func() error {
			return runCommand("", "./scripts/check_mcp_lsp_workload_catalog.sh")
		}},
		"mcp-lsp:idle-quick": {run: func() error {
			return runMcpLSPQuickRoundTrip()
		}},
	}
}

// runMcpLSPQuickRoundTrip 执行本地 quick workload 并立即用 catalog guard 验证回执。
func runMcpLSPQuickRoundTrip() error {
	receiptDir, err := os.MkdirTemp("", "super-dolphin-quick-roundtrip-")
	if err != nil {
		return fmt.Errorf("create mcp-lsp quick roundtrip directory: %w", err)
	}
	receipt := filepath.Join(receiptDir, "receipt.json")
	runErr := runCommand("", "./scripts/run_mcp_lsp_workload.sh", "--id", "mcp-lsp-idle-quick", "--receipt", receipt)
	if runErr != nil {
		if cleanupErr := os.RemoveAll(receiptDir); cleanupErr != nil {
			return fmt.Errorf("mcp-lsp quick workload failed: %w; cleanup failed: %v", runErr, cleanupErr)
		}
		return runErr
	}
	guardErr := runCommand("", "./scripts/check_mcp_lsp_workload_catalog.sh", "--receipt", receipt, "--id", "mcp-lsp-idle-quick")
	cleanupErr := os.RemoveAll(receiptDir)
	if guardErr != nil {
		if cleanupErr != nil {
			return fmt.Errorf("mcp-lsp quick receipt guard failed: %w; cleanup failed: %v", guardErr, cleanupErr)
		}
		return guardErr
	}
	if cleanupErr != nil {
		return fmt.Errorf("cleanup mcp-lsp quick roundtrip directory: %w", cleanupErr)
	}
	return nil
}

// runFrontendE2E 按变更路径顺序执行所有匹配的前端 E2E 脚本，并在首个失败处阻断。
func runFrontendE2E(plan gatePlan) error {
	commands := frontendE2ECommands(plan.ChangedFiles)
	if len(commands) == 0 {
		return errors.New("frontend e2e gate has no matching command")
	}
	for _, command := range commands {
		if err := runCommand("frontend-app", "npm", "run", command.script); err != nil {
			return err
		}
	}
	return nil
}

// frontendE2ECommands 将已知规格精确映射到 npm 脚本，未知配置或 package.json 变更触发完整矩阵。
func frontendE2ECommands(files []string) []frontendE2ECommand {
	selected := map[string]bool{}
	for _, file := range files {
		if !frontendE2ERelevant(file) {
			continue
		}
		script, matched := frontendE2EScript(file)
		if !matched {
			return append([]frontendE2ECommand(nil), allFrontendE2ECommands...)
		}
		selected[script] = true
	}
	commands := make([]frontendE2ECommand, 0, len(selected))
	for _, command := range allFrontendE2ECommands {
		if selected[command.script] {
			commands = append(commands, command)
		}
	}
	return commands
}

// frontendE2EScript 返回路径对应的唯一专项脚本；未登记路径由调用方提升为完整矩阵。
func frontendE2EScript(file string) (string, bool) {
	for _, match := range frontendE2EMatches {
		if strings.Contains(file, match.pathFragment) {
			return match.script, true
		}
	}
	return "", false
}
