package sourceexport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

const (
	policyVersion        = 1
	canonicalProductName = "Super Dolphin Agent"
	canonicalRepository  = "github.com/lihah111222333-cloud/super-dolphin-agent"
	canonicalLicense     = "Apache-2.0"
)

// ProjectIdentity 声明公开项目必须保持一致的产品身份。
type ProjectIdentity struct {
	ProductName string `json:"product_name"`
	Repository  string `json:"repository"`
	GoModule    string `json:"go_module"`
	License     string `json:"license"`
}

// PathRule 声明一个精确文件或 glob 路径规则。
type PathRule struct {
	Pattern string `json:"pattern"`
	Kind    string `json:"kind"`
}

// Policy 是公开候选的版本化默认拒绝策略。
type Policy struct {
	SchemaVersion          int        `json:"schema_version"`
	CanonicalProductName   string     `json:"canonical_product_name"`
	CanonicalRepository    string     `json:"canonical_repository"`
	CanonicalModulePath    string     `json:"canonical_module_path"`
	LicenseSPDX            string     `json:"license_spdx"`
	RequiredRootFiles      []string   `json:"required_root_files"`
	AllowRules             []PathRule `json:"allow_rules"`
	DenyRules              []PathRule `json:"deny_rules"`
	ForbiddenIdentities    []string   `json:"forbidden_identities"`
	RequiredReadmes        []string   `json:"required_readmes"`
	RequiredREADMESections []string   `json:"required_readme_sections"`
	ForbiddenFileNames     []string   `json:"forbidden_file_names"`
	GeneratedFiles         []string   `json:"generated_files"`
}

// LoadPolicy 从磁盘严格解析并校验公开策略。
func LoadPolicy(filePath string) (Policy, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return Policy{}, policyError(filePath, err)
	}
	defer file.Close()

	var policy Policy
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, policyError(filePath, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Policy{}, policyError(filePath, errors.New("trailing JSON payload"))
	}
	if err := ValidatePolicy(policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// ValidatePolicy 检查策略版本、项目身份、规则和路径字段。
func ValidatePolicy(policy Policy) error {
	if policy.SchemaVersion != policyVersion {
		return policyError("schema_version", fmt.Errorf("must equal %d", policyVersion))
	}
	if err := validateIdentity(policy.projectIdentity()); err != nil {
		return err
	}
	if err := validateRules("allow_rules", policy.AllowRules, true); err != nil {
		return err
	}
	if err := validateRules("deny_rules", policy.DenyRules, false); err != nil {
		return err
	}
	for name, values := range map[string][]string{
		"required_root_files": policy.RequiredRootFiles,
		"required_readmes":    policy.RequiredReadmes,
		"generated_files":     policy.GeneratedFiles,
	} {
		if err := validatePathList(name, values); err != nil {
			return err
		}
	}
	for name, values := range map[string][]string{
		"forbidden_identities":     policy.ForbiddenIdentities,
		"required_readme_sections": policy.RequiredREADMESections,
		"forbidden_file_names":     policy.ForbiddenFileNames,
	} {
		if len(values) == 0 {
			return policyError(name, errors.New("must contain at least one value"))
		}
		if err := validateNonEmptyUnique(name, values); err != nil {
			return err
		}
	}
	return nil
}

func (policy Policy) projectIdentity() ProjectIdentity {
	return ProjectIdentity{
		ProductName: policy.CanonicalProductName,
		Repository:  policy.CanonicalRepository,
		GoModule:    policy.CanonicalModulePath,
		License:     policy.LicenseSPDX,
	}
}

func validateIdentity(identity ProjectIdentity) error {
	if identity.License != canonicalLicense {
		return &Error{Code: CodeLicenseMismatch, Path: "license_spdx", Err: fmt.Errorf("must equal %q", canonicalLicense)}
	}
	if identity.GoModule != canonicalRepository {
		return &Error{Code: CodeModulePathMismatch, Path: "canonical_module_path", Err: fmt.Errorf("must equal %q", canonicalRepository)}
	}
	if identity.Repository != canonicalRepository {
		return policyError("canonical_repository", fmt.Errorf("must equal %q", canonicalRepository))
	}
	if identity.ProductName != canonicalProductName {
		return policyError("canonical_product_name", fmt.Errorf("must equal %q", canonicalProductName))
	}
	return nil
}

// validateRules 校验规则集合，并拒绝重复的 kind 与 pattern 组合。
func validateRules(name string, rules []PathRule, required bool) error {
	if required && len(rules) == 0 {
		return policyError(name, errors.New("must contain at least one rule"))
	}
	seen := make(map[string]struct{}, len(rules))
	for index, rule := range rules {
		field := fmt.Sprintf("%s[%d]", name, index)
		if err := validateRule(field, rule); err != nil {
			return err
		}
		key := rule.Kind + "\x00" + rule.Pattern
		if _, exists := seen[key]; exists {
			return policyError(field, errors.New("duplicate rule"))
		}
		seen[key] = struct{}{}
	}
	return nil
}

// validateRule 校验单条规则的类型、规范路径和 glob 语法。
func validateRule(field string, rule PathRule) error {
	if rule.Kind != "file" && rule.Kind != "glob" {
		return policyError(field+".kind", errors.New("must be file or glob"))
	}
	if err := validatePolicyPath(field+".pattern", rule.Pattern); err != nil {
		return err
	}
	if rule.Kind == "file" && strings.ContainsAny(rule.Pattern, "*?[") {
		return policyError(field+".pattern", errors.New("file rule cannot contain glob syntax"))
	}
	if rule.Kind == "glob" {
		if _, err := path.Match(rule.Pattern, "candidate"); err != nil {
			return policyError(field+".pattern", fmt.Errorf("invalid glob: %w", err))
		}
	}
	return nil
}

func validatePathList(name string, values []string) error {
	if len(values) == 0 {
		return policyError(name, errors.New("must contain at least one path"))
	}
	if err := validateNonEmptyUnique(name, values); err != nil {
		return err
	}
	for index, value := range values {
		if err := validatePolicyPath(fmt.Sprintf("%s[%d]", name, index), value); err != nil {
			return err
		}
	}
	return nil
}

func validateNonEmptyUnique(name string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		field := fmt.Sprintf("%s[%d]", name, index)
		if value == "" {
			return policyError(field, errors.New("must not be empty"))
		}
		if _, exists := seen[value]; exists {
			return policyError(field, errors.New("duplicate value"))
		}
		seen[value] = struct{}{}
	}
	return nil
}

// validatePolicyPath 校验策略路径是 slash-normalized 的仓库相对路径。
func validatePolicyPath(field string, value string) error {
	if value == "" || value == "." {
		return policyError(field, errors.New("must be a non-empty repository-relative path"))
	}
	if strings.Contains(value, `\`) || strings.HasPrefix(value, "/") {
		return policyError(field, errors.New("must use a slash-normalized relative path"))
	}
	if path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return policyError(field, errors.New("must not contain traversal or redundant segments"))
	}
	return nil
}

func policyError(field string, err error) error {
	return &Error{Code: CodePolicyInvalid, Path: field, Err: err}
}
