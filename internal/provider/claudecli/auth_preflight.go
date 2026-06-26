package claudecli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
// 只有命令明确返回 loggedIn=false 时才阻断；状态不可判定时继续启动，让 CLI 返回真实错误。
func (d *driver) preflightClaudeAuth(ctx context.Context, binary, cwd string, cfg cliLaunchConfig) error {
	checkCtx, cancel := ctxutil.WithTimeout(ctx, claudeAuthPreflightTimeout)
	defer cancel()
	status, raw, statusErr := d.authStatus(checkCtx, binary, cwd, cfg)
	inconclusive := statusErr != nil
	if inconclusive {
		pkglogger.Warn("claudecli: auth status preflight inconclusive; continuing launch", "error", statusErr)
	}
	if inconclusive || status.LoggedIn {
		return nil
	}
	if !claudeAuthStatusReportsLoggedOut(raw) {
		pkglogger.Warn("claudecli: auth status preflight missing loggedIn=false; continuing launch", "raw", strings.TrimSpace(raw))
		return nil
	}
	detail := strings.TrimSpace(raw)
	if detail == "" {
		detail = fmt.Sprintf("authMethod=%s apiProvider=%s", status.AuthMethod, status.APIProvider)
	}
	return fmt.Errorf("claudecli: authentication required: %s", detail)
}

// claudeAuthStatusReportsLoggedOut 判断原始 JSON 是否明确声明 loggedIn=false。
func claudeAuthStatusReportsLoggedOut(raw string) bool {
	var payload struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &payload); err != nil || payload.LoggedIn == nil {
		return false
	}
	return !*payload.LoggedIn
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
