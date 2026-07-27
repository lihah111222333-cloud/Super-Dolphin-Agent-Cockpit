package codemapindex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const projectMapShardHeader = "path\tmodule\tdomain\ttype\tsize_bytes\tpurpose\tsearch_keys"

type projectMapManifest struct {
	IndexFiles struct {
		Shards []struct {
			File string `json:"file"`
		} `json:"shards"`
	} `json:"index_files"`
}

// validateProjectMapLifecycle 拒绝分片重新索引历史文档、失效路径或未声明分片。
func validateProjectMapLifecycle(root string, policy codemapPolicy) []string {
	indexDir := filepath.Join(root, "docs", "doc", "codemap", "project-map", "index")
	entries, err := os.ReadDir(indexDir)
	if err != nil {
		return []string{fmt.Sprintf("project-map index cannot be read: %v", err)}
	}
	expected, problems := projectMapManifestShards(root, policy)
	actual := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tsv") {
			continue
		}
		actual[entry.Name()] = struct{}{}
		problems = append(problems, validateProjectMapShard(root, indexDir, entry.Name(), policy)...)
	}
	if len(actual) == 0 {
		problems = append(problems, "project-map index contains no TSV shards")
	}
	return append(problems, compareProjectMapShardSets(expected, actual)...)
}

// validateProjectMapShard 校验单个 TSV 分片头部与每一行。
func validateProjectMapShard(root, indexDir, name string, policy codemapPolicy) []string {
	lines, err := readLines(filepath.Join(indexDir, name))
	if err != nil {
		return []string{fmt.Sprintf("project-map shard %s cannot be read: %v", name, err)}
	}
	if len(lines) == 0 || lines[0] != projectMapShardHeader {
		return []string{fmt.Sprintf("project-map shard %s has invalid or missing header", name)}
	}
	var problems []string
	for index, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		relative, _, _ := strings.Cut(line, "\t")
		problems = append(problems, validateProjectMapEntry(root, name, index+2, relative, policy)...)
	}
	return problems
}

// validateProjectMapEntry 校验一条路径的规范形式、生命周期与存在性。
func validateProjectMapEntry(root, shard string, lineNumber int, relative string, policy codemapPolicy) []string {
	normalized := normalizeRepoRelative(relative)
	var problems []string
	if relative != normalized {
		problems = append(problems, fmt.Sprintf("project-map shard %s:%d has non-canonical path: %s", shard, lineNumber, relative))
	}
	if isHistoricalDocumentPath(normalized, policy) {
		problems = append(problems, fmt.Sprintf("project-map index contains historical document at %s:%d: %s", shard, lineNumber, relative))
	}
	absolute, err := resolveRepoPath(root, relative)
	if err != nil {
		return append(problems, fmt.Sprintf("project-map shard %s:%d has invalid path %s: %v", shard, lineNumber, relative, err))
	}
	if _, err := os.Stat(absolute); err != nil {
		problems = append(problems, fmt.Sprintf("project-map shard %s:%d points to missing path: %s", shard, lineNumber, relative))
	}
	return problems
}

// projectMapManifestShards 读取 manifest 并拒绝未知、重复或逃逸 index 的分片。
func projectMapManifestShards(root string, policy codemapPolicy) (map[string]struct{}, []string) {
	manifest, problem := readProjectMapManifest(root)
	if problem != "" {
		return nil, []string{problem}
	}
	known := policyShardNames(policy)
	expected := make(map[string]struct{}, len(manifest.IndexFiles.Shards))
	var problems []string
	for _, shard := range manifest.IndexFiles.Shards {
		name, problem := validateManifestShardPath(shard.File, known)
		if problem != "" {
			problems = append(problems, problem)
			continue
		}
		if _, duplicate := expected[name]; duplicate {
			problems = append(problems, fmt.Sprintf("project-map manifest repeats shard: %s", name))
		}
		expected[name] = struct{}{}
	}
	problems = append(problems, missingCanonicalShards(known, expected)...)
	if len(expected) == 0 {
		problems = append(problems, "project-map manifest declares no TSV shards")
	}
	return expected, problems
}

// readProjectMapManifest 读取并严格解码 manifest。
func readProjectMapManifest(root string) (projectMapManifest, string) {
	path := filepath.Join(root, "docs", "doc", "codemap", "project-map", "AI_PROJECT_MANIFEST.json")
	body, err := os.ReadFile(path)
	if err != nil {
		return projectMapManifest{}, fmt.Sprintf("project-map manifest cannot be read: %v", err)
	}
	var manifest projectMapManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return projectMapManifest{}, fmt.Sprintf("project-map manifest cannot be decoded: %v", err)
	}
	return manifest, ""
}

// policyShardNames 把 domain->file 策略投影为允许的文件名集合。
func policyShardNames(policy codemapPolicy) map[string]struct{} {
	known := make(map[string]struct{}, len(policy.shardFiles))
	for _, file := range policy.shardFiles {
		known[file] = struct{}{}
	}
	return known
}

// validateManifestShardPath 校验 manifest 分片必须是 index 下的单层规范 TSV。
func validateManifestShardPath(raw string, known map[string]struct{}) (string, string) {
	clean := normalizeRepoRelative(raw)
	prefix := "docs/doc/codemap/project-map/index/"
	name := strings.TrimPrefix(clean, prefix)
	if !strings.HasPrefix(clean, prefix) || strings.Contains(name, "/") {
		return "", fmt.Sprintf("project-map manifest has invalid shard path: %s", raw)
	}
	if _, ok := known[name]; !ok {
		return "", fmt.Sprintf("project-map manifest has unknown shard: %s", name)
	}
	return name, ""
}

// missingCanonicalShards 找出 policy 中存在、manifest 中缺失的分片。
func missingCanonicalShards(known, expected map[string]struct{}) []string {
	var problems []string
	for name := range known {
		if _, ok := expected[name]; !ok {
			problems = append(problems, fmt.Sprintf("project-map manifest omits canonical shard: %s", name))
		}
	}
	return problems
}

// compareProjectMapShardSets 对账 manifest 声明与磁盘分片集合。
func compareProjectMapShardSets(expected, actual map[string]struct{}) []string {
	var problems []string
	for name := range expected {
		if _, ok := actual[name]; !ok {
			problems = append(problems, fmt.Sprintf("project-map manifest shard is missing: %s", name))
		}
	}
	for name := range actual {
		if _, ok := expected[name]; !ok {
			problems = append(problems, fmt.Sprintf("project-map index has undeclared shard: %s", name))
		}
	}
	return problems
}
