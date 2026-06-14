package team

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var ErrTeamMemSecretDetected = errors.New("team memory content contains secrets")

type TeamMemoryGuard struct {
	manager *TeamMemoryManager
}

type TeamMemSecretFinding struct {
	RuleID string
	Line   int
	Match  string
}

type TeamMemSkippedFile struct {
	Path     string
	Findings []TeamMemSecretFinding
}

type TeamMemPrePushScanResult struct {
	Allowed map[string]string
	Skipped []TeamMemSkippedFile
}

type TeamMemSecretError struct {
	Path     string
	Findings []TeamMemSecretFinding
}

type teamSecretRule struct {
	id      string
	pattern *regexp.Regexp
}

var teamSecretRules = []teamSecretRule{
	{id: "private_key", pattern: regexp.MustCompile(`(?m)-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----`)},
	{id: "github_pat", pattern: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}\b`)},
	{id: "github_token", pattern: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`)},
	{id: "openai_key", pattern: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`)},
	{id: "aws_access_key", pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{id: "quoted_secret_assignment", pattern: regexp.MustCompile(`(?im)\b(?:api[_-]?key|access[_-]?token|secret[_-]?key|auth[_-]?token)\b\s*[:=]\s*['"][A-Za-z0-9/_+=.-]{16,}['"]`)},
}

// NewTeamMemoryGuard 创建team记忆守卫。
func NewTeamMemoryGuard(manager *TeamMemoryManager) *TeamMemoryGuard {
	return &TeamMemoryGuard{manager: manager}
}

// Error 返回错误文本。
func (e *TeamMemSecretError) Error() string {
	if len(e.Findings) == 0 {
		return ErrTeamMemSecretDetected.Error()
	}
	first := e.Findings[0]
	return fmt.Sprintf("%s: %s line %d matched %s", ErrTeamMemSecretDetected, e.Path, first.Line, first.RuleID)
}

// Unwrap 返回底层错误。
func (e *TeamMemSecretError) Unwrap() error {
	return ErrTeamMemSecretDetected
}

// ValidateWrite 校验write。
func (g *TeamMemoryGuard) ValidateWrite(path, content string) (string, error) {
	root, err := g.root()
	if err != nil {
		return "", err
	}
	validatedPath, err := validateTeamMemWritePath(root, path)
	if err != nil {
		return "", err
	}
	findings := ScanTeamMemContent(content)
	if len(findings) > 0 {
		return "", &TeamMemSecretError{Path: validatedPath, Findings: findings}
	}
	return validatedPath, nil
}

// FilterPushFiles 处理过滤条件push文件。
func (g *TeamMemoryGuard) FilterPushFiles(files map[string]string) TeamMemPrePushScanResult {
	result := TeamMemPrePushScanResult{Allowed: make(map[string]string, len(files))}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		content := files[path]
		findings := ScanTeamMemContent(content)
		if len(findings) == 0 {
			result.Allowed[path] = content
			continue
		}
		result.Skipped = append(result.Skipped, TeamMemSkippedFile{Path: path, Findings: findings})
	}
	if len(result.Allowed) == 0 {
		result.Allowed = nil
	}
	return result
}

// ScanTeamMemContent 扫描teammem内容。
func ScanTeamMemContent(content string) []TeamMemSecretFinding {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	findings := make([]TeamMemSecretFinding, 0, len(teamSecretRules))
	for _, rule := range teamSecretRules {
		findings = appendTeamSecretRuleFindings(findings, normalized, rule)
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].RuleID < findings[j].RuleID
	})
	if len(findings) == 0 {
		return nil
	}
	return findings
}

func appendTeamSecretRuleFindings(findings []TeamMemSecretFinding, content string, rule teamSecretRule) []TeamMemSecretFinding {
	matches := rule.pattern.FindAllStringIndex(content, -1)
	for _, match := range matches {
		findings = append(findings, TeamMemSecretFinding{
			RuleID: rule.id,
			Line:   teamSecretLineNumber(content, match[0]),
			Match:  truncateSecretMatch(content[match[0]:match[1]]),
		})
	}
	return findings
}

func teamSecretLineNumber(content string, offset int) int {
	return 1 + strings.Count(content[:offset], "\n")
}

func truncateSecretMatch(match string) string {
	match = strings.TrimSpace(match)
	if len(match) <= 48 {
		return match
	}
	return match[:48] + "..."
}

func (g *TeamMemoryGuard) root() (string, error) {
	if g == nil || g.manager == nil {
		return "", ErrTeamMemoryDisabled
	}
	return configuredTeamMemPath(g.manager)
}
