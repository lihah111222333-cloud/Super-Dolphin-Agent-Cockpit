package prompt

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

const (
	systemContextCommandTimeout = 2 * time.Second
	systemContextMaxStatusLines = 20
)

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

func loadSystemContextGitStatus(ctx context.Context, buildCtx BuildCtx) string {
	dir := systemContextRepoDir(buildCtx)
	if dir == "" {
		return ""
	}
	return runSystemContextGitStatus(ctx, dir)
}

func systemContextRepoDir(buildCtx BuildCtx) string {
	for _, value := range []string{strings.TrimSpace(buildCtx.CWD), strings.TrimSpace(buildCtx.GitRoot)} {
		if value != "" {
			return value
		}
	}
	return ""
}

func runSystemContextGitStatus(ctx context.Context, dir string) string {
	ctx, cancel := kernel.WithTimeout(ctx, systemContextCommandTimeout)
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

func systemContextCacheBreaker(cfg *Config) string {
	if cfg == nil || !cfg.EnableSystemContextCacheBreaker {
		return ""
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}
