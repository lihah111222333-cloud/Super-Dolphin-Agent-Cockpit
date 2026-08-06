package team

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ErrTeamMemSecretDetected 表示团队记忆内容命中了密钥扫描规则。
var ErrTeamMemSecretDetected = errors.New("team memory content contains secrets")

// TeamMemoryGuard 负责团队记忆的安全守卫：写操作路径校验和内容密钥扫描。
type TeamMemoryGuard struct {
	manager *TeamMemoryManager
}

// TeamMemSecretFinding 是团队记忆密钥扫描的一处命中。
// Match 已被截断，可进入 UI/日志；完整 secret 不能通过该结构外泄。
type TeamMemSecretFinding struct {
	RuleID string
	Line   int
	Match  string
}

// TeamMemSkippedFile 记录推送前因密钥命中而跳过的文件。
type TeamMemSkippedFile struct {
	Path     string
	Findings []TeamMemSecretFinding
}

// TeamMemPrePushScanResult 是推送前密钥扫描的跨模块结果。
// Allowed 可继续进入远端 push，Skipped 会回传给 UI/RPC 告知用户哪些文件被阻断。
type TeamMemPrePushScanResult struct {
	Allowed map[string]string
	Skipped []TeamMemSkippedFile
}

// TeamMemSecretError 表示单文件写入命中了密钥规则。
// 它实现 Unwrap，调用方可用 errors.Is(err, ErrTeamMemSecretDetected) 统一识别。
type TeamMemSecretError struct {
	Path     string
	Findings []TeamMemSecretFinding
}

// teamSecretRule 是单条密钥检测规则，包含规则 ID 和正则表达式。
type teamSecretRule struct {
	id      string
	pattern *regexp.Regexp
}

// teamSecretRules 创建当前扫描所需的规则快照，避免共享可变集合成为安全策略的第二 owner。
func teamSecretRules() [6]teamSecretRule {
	return [6]teamSecretRule{
		{id: "private_key", pattern: regexp.MustCompile(`(?m)-----BEGIN(?: [A-Z0-9]+)* PRIVATE KEY-----`)},
		{id: "github_pat", pattern: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}\b`)},
		{id: "github_token", pattern: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,255}\b`)},
		{id: "openai_key", pattern: regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{20,}\b`)},
		{id: "aws_access_key", pattern: regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
		{id: "quoted_secret_assignment", pattern: regexp.MustCompile(`(?im)\b(?:api[_-]?key|access[_-]?token|secret[_-]?key|auth[_-]?token)\b\s*[:=]\s*['"][A-Za-z0-9/_+=.-]{16,}['"]`)},
	}
}

// NewTeamMemoryGuard 创建团队记忆写入守卫。
func NewTeamMemoryGuard(manager *TeamMemoryManager) *TeamMemoryGuard {
	return &TeamMemoryGuard{manager: manager}
}

// Error 返回包含首个命中规则和行号的错误文本。
func (e *TeamMemSecretError) Error() string {
	if len(e.Findings) == 0 {
		return ErrTeamMemSecretDetected.Error()
	}
	first := e.Findings[0]
	return fmt.Sprintf("%s: %s line %d matched %s", ErrTeamMemSecretDetected, e.Path, first.Line, first.RuleID)
}

// Unwrap 返回 ErrTeamMemSecretDetected，供 errors.Is 判断。
func (e *TeamMemSecretError) Unwrap() error {
	return ErrTeamMemSecretDetected
}

// ValidateWrite 校验团队记忆写入路径和内容。
// 路径必须落在团队记忆根目录内，内容命中密钥规则时直接阻断写入。
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

// FilterPushFiles 在推送前过滤含密钥的文件。
// 安全文件保留在 Allowed，命中文件进入 Skipped，调用方可继续推送剩余文件。
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

// ScanTeamMemContent 扫描团队记忆内容中的常见密钥模式。
// 返回结果按行号和规则 ID 排序，便于 UI 和日志稳定展示。
func ScanTeamMemContent(content string) []TeamMemSecretFinding {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	rules := teamSecretRules()
	findings := make([]TeamMemSecretFinding, 0, len(rules))
	for _, rule := range rules {
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

// appendTeamSecretRuleFindings 将单条规则的所有命中追加到 findings，并只保存截断后的匹配片段。
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

// teamSecretLineNumber 根据 byte offset 计算 1-based 行号。
func teamSecretLineNumber(content string, offset int) int {
	return 1 + strings.Count(content[:offset], "\n")
}

// truncateSecretMatch 截断命中的密钥片段，避免日志暴露完整 secret。
func truncateSecretMatch(match string) string {
	match = strings.TrimSpace(match)
	if len(match) <= 48 {
		return match
	}
	return match[:48] + "..."
}

// root 返回团队记忆根目录，功能未启用时返回 ErrTeamMemoryDisabled。
func (g *TeamMemoryGuard) root() (string, error) {
	if g == nil || g.manager == nil {
		return "", ErrTeamMemoryDisabled
	}
	return configuredTeamMemPath(g.manager)
}
