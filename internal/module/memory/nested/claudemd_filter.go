package nested

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func shouldSkipInjectedSource(source ClaudeMdSource, gate GateSnapshot) bool {
	switch source.Type {
	case sourceTypeAutoMem:
		return !gate.InjectMemoryIndex
	case sourceTypeTeamMem:
		return !gate.InjectMemoryIndex || !gate.InjectTeamMemIndex
	default:
		return false
	}
}

func shouldExcludeClaudeMdSource(source ClaudeMdSource, patterns []string) bool {
	if len(patterns) == 0 || !isExcludeEligibleClaudeMdSource(source) {
		return false
	}
	target := filepath.ToSlash(cleanClaudeMdPath(source.Path))
	for _, pattern := range patterns {
		if matchClaudeMdExclude(pattern, target) {
			return true
		}
	}
	return false
}

func isExcludeEligibleClaudeMdSource(source ClaudeMdSource) bool {
	if source.Origin == sourceOriginAddDir {
		return false
	}
	switch source.Type {
	case sourceTypeUser, sourceTypeProject, sourceTypeLocal:
		return true
	default:
		return false
	}
}

type projectSourceFilter struct {
	enabled      bool
	worktree     bool
	worktreeRoot string
}

func resolveProjectSourceFilter(buildCtx contract.BuildCtx, gate GateSnapshot) projectSourceFilter {
	return projectSourceFilter{
		enabled:      gate.SkipProjectLocalClaudeMd,
		worktree:     buildCtx.IsWorktree,
		worktreeRoot: cleanClaudeMdPath(buildCtx.GitRoot),
	}
}

func shouldSkipProjectSource(source ClaudeMdSource, filter projectSourceFilter) bool {
	if !filter.enabled || !isCheckedInProjectClaudeMdSource(source) {
		return false
	}
	if !filter.worktree || filter.worktreeRoot == "" {
		return true
	}
	baseDir := cleanClaudeMdPath(source.BaseDir)
	if baseDir == "" {
		baseDir = cleanClaudeMdPath(filepath.Dir(source.Path))
	}
	return baseDir != "" && baseDir != filter.worktreeRoot && isAncestorOrSame(baseDir, filter.worktreeRoot)
}

func isCheckedInProjectClaudeMdSource(source ClaudeMdSource) bool {
	if source.Origin == sourceOriginAddDir {
		return false
	}
	return source.Type == sourceTypeProject
}

func normalizeClaudeMdExcludePatterns(excludes []string) []string {
	patterns := make([]string, 0, len(excludes))
	for _, exclude := range excludes {
		exclude = filepath.ToSlash(cleanClaudeMdPath(exclude))
		if exclude != "" {
			patterns = append(patterns, exclude)
		}
	}
	return normalizeStringSlice(patterns)
}

func matchClaudeMdExclude(pattern, target string) bool {
	regex := globPatternToRegexp(pattern)
	matched, err := regexp.MatchString(regex, target)
	return err == nil && matched
}

func globPatternToRegexp(pattern string) string {
	var builder strings.Builder
	builder.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				builder.WriteString(".*")
				i++
				continue
			}
			builder.WriteString("[^/]*")
		case '?':
			builder.WriteString(".")
		default:
			builder.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	builder.WriteString("$")
	return builder.String()
}
