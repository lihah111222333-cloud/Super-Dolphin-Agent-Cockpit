package prompt

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
)

const (
	systemContextCommandTimeout = 2 * time.Second
	systemContextMaxStatusLines = 20
)

// buildSystemContext 组装会随 turn 变化的系统上下文，目前包含 git status 和可选 cache breaker。
func (s *service) buildSystemContext(ctx context.Context, buildCtx BuildCtx) SystemContext {
	systemContext := SystemContext{}
	if gitStatus := loadSystemContextGitStatus(ctx, buildCtx); gitStatus != "" {
		systemContext["gitStatus"] = gitStatus
	}
	if cacheBreaker := systemContextCacheBreaker(s.cfg); cacheBreaker != "" {
		systemContext["cacheBreaker"] = cacheBreaker
	}
	if len(systemContext) == 0 {
		return nil
	}
	return systemContext
}

// loadSystemContextGitStatus 根据 BuildCtx 选择仓库目录并读取简短 git status。
func loadSystemContextGitStatus(ctx context.Context, buildCtx BuildCtx) string {
	dir := systemContextRepoDir(buildCtx)
	if dir == "" {
		return ""
	}
	return runSystemContextGitStatus(ctx, dir)
}

// systemContextRepoDir 优先使用当前 cwd，缺失时退回 git root。
func systemContextRepoDir(buildCtx BuildCtx) string {
	for _, value := range []string{strings.TrimSpace(buildCtx.CWD), strings.TrimSpace(buildCtx.GitRoot)} {
		if value != "" {
			return value
		}
	}
	return ""
}

// runSystemContextGitStatus 在固定超时内执行 git status，失败时返回可展示的降级文本。
func runSystemContextGitStatus(ctx context.Context, dir string) string {
	ctx, cancel := ctxutil.WithTimeout(ctx, systemContextCommandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--short", "--branch").CombinedOutput()
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "not a git repository") {
			return "Not a git repository."
		}
		return "Git status unavailable."
	}
	return trimSystemContextGitStatus(string(output))
}

// trimSystemContextGitStatus 清理 git status 输出并限制行数，避免 prompt 被大型脏树撑爆。
func trimSystemContextGitStatus(output string) string {
	lines := make([]string, 0, systemContextMaxStatusLines)
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r\t ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "Working tree clean."
	}
	if len(lines) <= systemContextMaxStatusLines {
		return strings.Join(lines, "\n")
	}
	trimmed := append([]string(nil), lines[:systemContextMaxStatusLines]...)
	trimmed = append(trimmed, fmt.Sprintf("... (+%d more lines)", len(lines)-systemContextMaxStatusLines))
	return strings.Join(trimmed, "\n")
}

// systemContextCacheBreaker 在配置开启时返回当前时间，用于强制系统上下文不复用旧缓存。
func systemContextCacheBreaker(cfg *Config) string {
	if cfg == nil || !cfg.EnableSystemContextCacheBreaker {
		return ""
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}
