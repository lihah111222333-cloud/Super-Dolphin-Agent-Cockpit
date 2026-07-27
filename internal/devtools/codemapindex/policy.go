package codemapindex

import (
	"fmt"
	"path/filepath"
	"strings"
)

type codemapPolicy struct {
	historicalRoots []string
	shardFiles      map[string]string
}

// loadCodemapPolicy 读取 JS 生成器与 Go 校验器共享的严格行协议。
func loadCodemapPolicy(root string) (codemapPolicy, error) {
	lines, err := readLines(filepath.Join(root, "scripts", "codemap_policy.txt"))
	if err != nil {
		return codemapPolicy{}, fmt.Errorf("read codemap policy: %w", err)
	}
	policy := codemapPolicy{shardFiles: make(map[string]string)}
	shardNames := make(map[string]struct{})
	schemaSeen := false
	for index, line := range lines {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if err := applyPolicyFields(&policy, shardNames, fields, &schemaSeen); err != nil {
			return codemapPolicy{}, fmt.Errorf("codemap policy line %d: %w", index+1, err)
		}
	}
	if !schemaSeen || len(policy.historicalRoots) == 0 || len(policy.shardFiles) == 0 {
		return codemapPolicy{}, fmt.Errorf("codemap policy must declare schema, historical roots, and shards")
	}
	return policy, nil
}

// applyPolicyFields 解析单行策略，未知或重复 schema 一律 fail-fast。
func applyPolicyFields(policy *codemapPolicy, shardNames map[string]struct{}, fields []string, schemaSeen *bool) error {
	if len(fields) == 2 && fields[0] == "schema" {
		if *schemaSeen {
			return fmt.Errorf("duplicate schema declaration")
		}
		*schemaSeen = true
		if fields[1] != "1" {
			return fmt.Errorf("unsupported schema %q", fields[1])
		}
		return nil
	}
	if len(fields) == 2 && fields[0] == "historical" {
		return addHistoricalPolicyRoot(policy, fields[1])
	}
	if len(fields) == 3 && fields[0] == "shard" {
		return addPolicyShard(policy, shardNames, fields[1], fields[2])
	}
	return fmt.Errorf("malformed declaration")
}

// addHistoricalPolicyRoot 校验历史根为规范 repo 路径且不存在重复或父子重叠。
func addHistoricalPolicyRoot(policy *codemapPolicy, root string) error {
	if !policyRootRe.MatchString(root) || normalizeRepoRelative(root) != root {
		return fmt.Errorf("invalid historical root %q", root)
	}
	for _, existing := range policy.historicalRoots {
		if pathsOverlap(existing, root) {
			return fmt.Errorf("duplicate or overlapping historical root %q", root)
		}
	}
	policy.historicalRoots = append(policy.historicalRoots, root)
	return nil
}

// pathsOverlap 判断两个规范目录路径是否相同或互为祖先。
func pathsOverlap(left, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

// addPolicyShard 校验 domain/file 映射唯一且使用规范 TSV 文件名。
func addPolicyShard(policy *codemapPolicy, shardNames map[string]struct{}, domain, file string) error {
	if !policyDomainRe.MatchString(domain) || !policyShardRe.MatchString(file) {
		return fmt.Errorf("invalid shard mapping %q=%q", domain, file)
	}
	if _, ok := policy.shardFiles[domain]; ok {
		return fmt.Errorf("duplicate shard domain %q", domain)
	}
	if _, ok := shardNames[file]; ok {
		return fmt.Errorf("duplicate shard file %q", file)
	}
	policy.shardFiles[domain] = file
	shardNames[file] = struct{}{}
	return nil
}

// isHistoricalDocumentPath 判断路径是否属于默认只做追溯的文档目录。
func isHistoricalDocumentPath(relative string, policy codemapPolicy) bool {
	relative = filepath.ToSlash(relative)
	for _, root := range policy.historicalRoots {
		if relative == root || strings.HasPrefix(relative, root+"/") {
			return true
		}
	}
	return false
}
