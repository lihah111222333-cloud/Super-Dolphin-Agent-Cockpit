package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/ctxutil"
)

const claudeAuthPreflightTimeout = 10 * time.Second

// claudeAuthStatus 是 `claude auth status --json` 的最小解析结构。
type claudeAuthStatus struct {
	LoggedIn     bool   `json:"loggedIn"`
	AuthMethod   string `json:"authMethod"`
	APIProvider  string `json:"apiProvider"`
	APIKeySource string `json:"apiKeySource"`
}

// preflightClaudeAuth 在启动 Claude CLI 前做认证预检。
// 认证状态必须明确可判定；命令失败或缺少 loggedIn 都会阻断，避免把登录问题推迟到子进程启动后才暴露。
func (d *driver) preflightClaudeAuth(ctx context.Context, binary, cwd string, cfg cliLaunchConfig) error {
	checkCtx, cancel := ctxutil.WithTimeout(ctx, claudeAuthPreflightTimeout)
	defer cancel()
	status, raw, statusErr := d.authStatus(checkCtx, binary, cwd, cfg)
	if statusErr != nil {
		detail := strings.TrimSpace(raw)
		if detail == "" {
			return fmt.Errorf("claudecli: auth status preflight failed: %w", statusErr)
		}
		return fmt.Errorf("claudecli: auth status preflight failed: %w: %s", statusErr, detail)
	}
	loggedIn, err := parseClaudeAuthLoggedIn(raw)
	if err != nil {
		return fmt.Errorf("claudecli: auth status inconclusive: %w", err)
	}
	if loggedIn {
		return nil
	}
	detail := strings.TrimSpace(raw)
	if detail == "" {
		detail = fmt.Sprintf("authMethod=%s apiProvider=%s", status.AuthMethod, status.APIProvider)
	}
	return fmt.Errorf("claudecli: authentication required: %s", detail)
}

// parseClaudeAuthLoggedIn 要求 auth status JSON 明确携带 loggedIn 布尔值。
// 缺字段或坏 JSON 都是不可信状态，调用方必须 fail-fast，而不能继续启动 CLI。
func parseClaudeAuthLoggedIn(raw string) (bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false, errors.New("empty auth status")
	}
	var payload struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return false, fmt.Errorf("decode auth status: %w", err)
	}
	if payload.LoggedIn == nil {
		return false, errors.New("missing loggedIn")
	}
	return *payload.LoggedIn, nil
}

// runClaudeAuthStatus 执行 Claude CLI 认证状态查询，并复用启动环境里的 provider env。
func runClaudeAuthStatus(ctx context.Context, binary, cwd string, cfg cliLaunchConfig) (claudeAuthStatus, string, error) {
	if strings.TrimSpace(binary) == "" {
		binary = defaultClaudeCLIBin
	}
	cmd := exec.CommandContext(ctx, resolveClaudeBinary(binary), "auth", "status", "--json")
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		cmd.Dir = cwd
	}
	env := contract.ScrubDatabaseEnv(os.Environ())
	if launchEnv := claudeLaunchEnv(cfg); len(launchEnv) > 0 {
		env = append(env, contract.ScrubDatabaseEnv(launchEnv)...)
	}
	cmd.Env = ensureLoopbackNoProxy(env)
	raw, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(raw))
	var status claudeAuthStatus
	if jsonErr := json.Unmarshal(raw, &status); jsonErr != nil {
		if err != nil {
			return claudeAuthStatus{}, output, fmt.Errorf("%w: %s", err, output)
		}
		return claudeAuthStatus{}, output, fmt.Errorf("decode auth status: %w", jsonErr)
	}
	if !status.LoggedIn {
		return status, output, nil
	}
	if err != nil {
		return claudeAuthStatus{}, output, err
	}
	return status, output, nil
}
