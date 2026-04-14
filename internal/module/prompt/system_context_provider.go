package prompt

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	systemContextCommandTimeout = 2 * time.Second
	systemContextMaxStatusLines = 20
)

type SystemContext struct {
	GitStatus    string
	CacheBreaker string
}

func (s *service) renderSystemContext(ctx context.Context, in StartInput) string {
	return formatSystemContext(s.buildSystemContext(ctx, in))
}

func (s *service) buildSystemContext(ctx context.Context, in StartInput) SystemContext {
	return SystemContext{
		GitStatus:    loadSystemContextGitStatus(ctx, in),
		CacheBreaker: systemContextCacheBreaker(s.cfg),
	}
}

func loadSystemContextGitStatus(ctx context.Context, in StartInput) string {
	dir := systemContextRepoDir(in)
	if dir == "" {
		return ""
	}
	return runSystemContextGitStatus(ctx, dir)
}

func systemContextRepoDir(in StartInput) string {
	for _, value := range []string{strings.TrimSpace(in.CWD), strings.TrimSpace(in.GitRoot)} {
		if value != "" {
			return value
		}
	}
	return ""
}

func runSystemContextGitStatus(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, systemContextCommandTimeout)
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

func formatSystemContext(ctx SystemContext) string {
	if strings.TrimSpace(ctx.GitStatus) == "" && strings.TrimSpace(ctx.CacheBreaker) == "" {
		return ""
	}
	lines := []string{"# System Context"}
	if gitStatus := strings.TrimSpace(ctx.GitStatus); gitStatus != "" {
		lines = append(lines, "Git status:", gitStatus)
	}
	if cacheBreaker := strings.TrimSpace(ctx.CacheBreaker); cacheBreaker != "" {
		lines = append(lines, "Cache breaker: "+cacheBreaker)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func systemContextCacheBreaker(cfg *Config) string {
	if cfg == nil || !cfg.EnableSystemContextCacheBreaker {
		return ""
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}
