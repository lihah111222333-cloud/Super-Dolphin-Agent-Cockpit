package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	rootProtocolPath       = "docs/automation/全仓夜间门禁健康巡检协议.md"
	ledgerProtocolPath     = "docs/automation/门禁问题台账接管协议.md"
	repairProtocolPath     = "docs/automation/授权问题修复与验证协议.md"
	bootstrapContractID    = "automation-5-immutable-bootstrap"
	bootstrapVersion       = "1.0.0"
	externalPromptLocator  = "automation-5"
	nightlyProtocolCommand = "go run ./scripts/nightly_protocol_validator"
)

var protocolVersionPattern = regexp.MustCompile(`^1\.[0-9]+\.[0-9]+$`)

type protocolFrontmatter struct {
	Protocol struct {
		ID            string `yaml:"id"`
		Version       string `yaml:"version"`
		SchemaVersion int    `yaml:"schema_version"`
		Status        string `yaml:"status"`
	} `yaml:"protocol"`
	Resources struct {
		LedgerHandoffProtocol    string `yaml:"ledger_handoff_protocol"`
		AuthorizedRepairProtocol string `yaml:"authorized_repair_protocol"`
		RootProtocol             string `yaml:"root_protocol"`
		RepairProtocol           string `yaml:"repair_protocol"`
		LedgerProtocol           string `yaml:"ledger_protocol"`
		ImmutableBootstrap       struct {
			ID                    string `yaml:"id"`
			Version               string `yaml:"version"`
			ExternalPromptLocator string `yaml:"external_prompt_locator"`
		} `yaml:"immutable_bootstrap_contract"`
	} `yaml:"resources"`
	Source struct {
		CanonicalFile string `yaml:"canonical_file"`
	} `yaml:"source"`
}

type protocolExpectation struct {
	Path            string
	ID              string
	FrontmatterRefs map[string]string
	MarkdownLinks   []string
}

type ciWorkflow struct {
	Jobs map[string]ciJob `yaml:"jobs"`
}

type ciJob struct {
	Steps []ciStep `yaml:"steps"`
}

type ciStep struct {
	Run string `yaml:"run"`
}

var protocolExpectations = []protocolExpectation{
	{
		Path: rootProtocolPath,
		ID:   "repository-nightly-gate-health",
		FrontmatterRefs: map[string]string{
			"ledger_handoff_protocol":    ledgerProtocolPath,
			"authorized_repair_protocol": repairProtocolPath,
		},
		MarkdownLinks: []string{"(门禁问题台账接管协议.md)", "(授权问题修复与验证协议.md)"},
	},
	{
		Path: ledgerProtocolPath,
		ID:   "gate-issue-ledger-handoff",
		FrontmatterRefs: map[string]string{
			"root_protocol":   rootProtocolPath,
			"repair_protocol": repairProtocolPath,
		},
		MarkdownLinks: []string{"(全仓夜间门禁健康巡检协议.md)", "(授权问题修复与验证协议.md)"},
	},
	{
		Path: repairProtocolPath,
		ID:   "authorized-issue-repair-and-verification",
		FrontmatterRefs: map[string]string{
			"root_protocol":   rootProtocolPath,
			"ledger_protocol": ledgerProtocolPath,
		},
		MarkdownLinks: []string{"(全仓夜间门禁健康巡检协议.md)", "(门禁问题台账接管协议.md)"},
	},
}

func main() {
	root, err := findRepoRoot(".")
	if err == nil {
		err = validateNightlyProtocols(root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "nightly protocol validation failed:", err)
		os.Exit(1)
	}
	fmt.Println("nightly protocol validation passed")
}

func validateNightlyProtocols(repoRoot string) error {
	for _, expectation := range protocolExpectations {
		if err := validateProtocol(repoRoot, expectation); err != nil {
			return err
		}
	}
	return validateCIWorkflowOwner(repoRoot)
}

// validateCIWorkflowOwner 解析 CI YAML 并验证真实 step 执行 nightly 协议门禁。
func validateCIWorkflowOwner(repoRoot string) error {
	workflow, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		return fmt.Errorf("read CI owner: %w", err)
	}
	runs, err := workflowRunsCommand(workflow, nightlyProtocolCommand)
	if err != nil {
		return fmt.Errorf("parse CI owner: %w", err)
	}
	if !runs {
		return fmt.Errorf("CI must execute %s", nightlyProtocolCommand)
	}
	return nil
}

// validateProtocol 解析一个规范文件并验证身份、引用、正文链接和不可变引导契约。
func validateProtocol(repoRoot string, expectation protocolExpectation) error {
	path := filepath.Join(repoRoot, filepath.FromSlash(expectation.Path))
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read canonical protocol %s: %w", expectation.Path, err)
	}
	frontmatterBytes, body, err := splitFrontmatter(data)
	if err != nil {
		return fmt.Errorf("%s: %w", expectation.Path, err)
	}
	var frontmatter protocolFrontmatter
	if err := yaml.Unmarshal(frontmatterBytes, &frontmatter); err != nil {
		return fmt.Errorf("%s: parse YAML frontmatter: %w", expectation.Path, err)
	}
	if err := validateProtocolIdentity(frontmatter, expectation); err != nil {
		return err
	}
	if err := validateProtocolReferences(repoRoot, frontmatter, expectation); err != nil {
		return err
	}
	if err := validateMarkdownLinks(body, expectation); err != nil {
		return err
	}
	return validateImmutableBootstrap(frontmatter, expectation)
}

// validateProtocolIdentity 验证规范身份、主版本、schema、状态和 canonical 路径。
func validateProtocolIdentity(frontmatter protocolFrontmatter, expectation protocolExpectation) error {
	if frontmatter.Protocol.ID != expectation.ID {
		return fmt.Errorf("%s: protocol.id=%q, want %q", expectation.Path, frontmatter.Protocol.ID, expectation.ID)
	}
	if !protocolVersionPattern.MatchString(frontmatter.Protocol.Version) {
		return fmt.Errorf("%s: protocol.version=%q is not compatible with major version 1", expectation.Path, frontmatter.Protocol.Version)
	}
	if frontmatter.Protocol.SchemaVersion != 1 || frontmatter.Protocol.Status != "active" {
		return fmt.Errorf("%s: require schema_version=1 and status=active", expectation.Path)
	}
	if frontmatter.Source.CanonicalFile != expectation.Path {
		return fmt.Errorf("%s: source.canonical_file=%q", expectation.Path, frontmatter.Source.CanonicalFile)
	}
	return nil
}

// validateProtocolReferences 验证 frontmatter 交叉引用值及其文件目标都存在。
func validateProtocolReferences(repoRoot string, frontmatter protocolFrontmatter, expectation protocolExpectation) error {
	refs := protocolReferences(frontmatter)
	for key, want := range expectation.FrontmatterRefs {
		if refs[key] != want {
			return fmt.Errorf("%s: resources.%s=%q, want %q", expectation.Path, key, refs[key], want)
		}
		if info, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(want))); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%s: resources.%s target is not a regular file: %s", expectation.Path, key, want)
		}
	}
	return nil
}

// protocolReferences 将固定资源字段转换为可按契约名称核验的索引。
func protocolReferences(frontmatter protocolFrontmatter) map[string]string {
	return map[string]string{
		"ledger_handoff_protocol":    frontmatter.Resources.LedgerHandoffProtocol,
		"authorized_repair_protocol": frontmatter.Resources.AuthorizedRepairProtocol,
		"root_protocol":              frontmatter.Resources.RootProtocol,
		"repair_protocol":            frontmatter.Resources.RepairProtocol,
		"ledger_protocol":            frontmatter.Resources.LedgerProtocol,
	}
}

// validateMarkdownLinks 验证正文保留可迁移的相对协议链接。
func validateMarkdownLinks(body string, expectation protocolExpectation) error {
	for _, link := range expectation.MarkdownLinks {
		if !strings.Contains(body, link) {
			return fmt.Errorf("%s: missing stable relative Markdown link %s", expectation.Path, link)
		}
	}
	return nil
}

// validateImmutableBootstrap 验证根协议绑定外部不可变引导标识与版本。
func validateImmutableBootstrap(frontmatter protocolFrontmatter, expectation protocolExpectation) error {
	if expectation.Path != rootProtocolPath {
		return nil
	}
	bootstrap := frontmatter.Resources.ImmutableBootstrap
	if bootstrap.ID != bootstrapContractID ||
		bootstrap.Version != bootstrapVersion ||
		bootstrap.ExternalPromptLocator != externalPromptLocator {
		return fmt.Errorf("%s: immutable bootstrap must bind %s %s through external locator %s", expectation.Path, bootstrapContractID, bootstrapVersion, externalPromptLocator)
	}
	return nil
}

// workflowRunsCommand 解析工作流 jobs.steps.run，并按独立 shell 行匹配规范命令。
func workflowRunsCommand(data []byte, command string) (bool, error) {
	var workflow ciWorkflow
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		return false, err
	}
	for _, job := range workflow.Jobs {
		for _, step := range job.Steps {
			for line := range strings.SplitSeq(step.Run, "\n") {
				if strings.TrimSpace(line) == command {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// splitFrontmatter 分离规范文件开头的 YAML frontmatter 与 Markdown 正文。
func splitFrontmatter(data []byte) ([]byte, string, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, "", errors.New("missing opening YAML frontmatter delimiter")
	}
	rest := strings.TrimPrefix(text, "---\n")
	frontmatter, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return nil, "", errors.New("missing closing YAML frontmatter delimiter")
	}
	return []byte(frontmatter), body, nil
}

// findRepoRoot 从起点向上查找包含 go.mod 的仓库根目录。
func findRepoRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(current, "go.mod")); err == nil && info.Mode().IsRegular() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("repository root not found from %s", start)
		}
		current = parent
	}
}
