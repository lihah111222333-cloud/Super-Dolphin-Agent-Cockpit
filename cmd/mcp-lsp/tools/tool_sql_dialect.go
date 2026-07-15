package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"gopkg.in/yaml.v3"
)

const sqliteSQLLanguageID = "sql"

type sqlcDialectConfig struct {
	SQL []sqlcDialectEntry `yaml:"sql"`
}

type sqlcDialectEntry struct {
	Engine  string         `yaml:"engine"`
	Queries sqlcQueryPaths `yaml:"queries"`
}

type sqlcQueryPaths []string

// UnmarshalYAML 兼容 sqlc queries 的单路径与路径列表写法。
func (p *sqlcQueryPaths) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var value string
		if err := node.Decode(&value); err != nil {
			return err
		}
		*p = sqlcQueryPaths{value}
		return nil
	case yaml.SequenceNode:
		var values []string
		if err := node.Decode(&values); err != nil {
			return err
		}
		*p = sqlcQueryPaths(values)
		return nil
	default:
		return fmt.Errorf("sqlc queries must be a string or string list")
	}
}

// resolveLanguageIDForFile 将 SQL 文件统一路由到 SQLite sqruff 服务，并拒绝非 SQLite sqlc owner。
func resolveLanguageIDForFile(ctx context.Context, filePath, languageID string) (string, error) {
	override := normalizeLanguageIDOverride(languageID)
	detected := lspmanager.DetectLanguageID(filePath)
	if detected != "sql" {
		return override, nil
	}
	if override != "" && override != sqliteSQLLanguageID {
		return "", fmt.Errorf("invalid language_id %q for SQL file %s: SQLite SQL requires language_id %q", languageID, filePath, sqliteSQLLanguageID)
	}
	if !strings.EqualFold(filepath.Ext(filePath), ".sql") {
		return override, nil
	}
	engine, err := sqlFileEngine(ctx, filePath)
	if err != nil {
		return "", err
	}
	if engine == "" || engine == "sqlite" {
		return sqliteSQLLanguageID, nil
	}
	return "", fmt.Errorf("unsupported sqlc engine %q for %s: mcp-lsp currently supports SQLite SQL only", engine, filePath)
}

// routeSQLDiagnosticsInput 根据 sqlc 所有权把 SQLite 单文件诊断切到 sqruff 服务。
func routeSQLDiagnosticsInput(ctx context.Context, input fileToolInput, targets []diagnosticTarget) (fileToolInput, error) {
	if len(targets) != 1 || !strings.EqualFold(filepath.Ext(targets[0].AbsPath), ".sql") {
		return input, nil
	}
	languageID, err := resolveLanguageIDForFile(ctx, targets[0].AbsPath, input.LanguageID)
	if err != nil {
		return fileToolInput{}, err
	}
	if languageID == sqliteSQLLanguageID {
		input.LanguageID = languageID
	}
	return input, nil
}

// diagnosticsInputsBySQLDialect 为包含 SQL 的批量诊断生成逐文件 SQLite 路由输入。
func diagnosticsInputsBySQLDialect(ctx context.Context, input fileToolInput, targets []diagnosticTarget) ([]fileToolInput, bool, error) {
	if len(targets) <= 1 {
		routed, err := routeSQLDiagnosticsInput(ctx, input, targets)
		return []fileToolInput{routed}, false, err
	}
	inputs := make([]fileToolInput, 0, len(targets))
	hasSQLite := false
	for _, target := range targets {
		single := input
		single.FilePath = target.AbsPath
		single.FilePaths = nil
		routed, err := routeSQLDiagnosticsInput(ctx, single, []diagnosticTarget{target})
		if err != nil {
			return nil, false, err
		}
		hasSQLite = hasSQLite || routed.LanguageID == sqliteSQLLanguageID
		inputs = append(inputs, routed)
	}
	if !hasSQLite {
		return []fileToolInput{input}, false, nil
	}
	return inputs, true, nil
}

// sqlFileEngine 只在目标所属的可信工作区根内查找最近的 owning sqlc 配置。
func sqlFileEngine(ctx context.Context, filePath string) (string, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	workspaceRoot, err := containingSQLWorkspaceRoot(ctx, absPath)
	if err != nil {
		return "", err
	}
	for dir := filepath.Dir(absPath); ; dir = filepath.Dir(dir) {
		engines, err := sqlcEnginesAtDir(dir, workspaceRoot, absPath)
		if err != nil {
			return "", err
		}
		engine, owned, err := uniqueSQLEngine(engines, absPath, dir)
		if err != nil || owned {
			return engine, err
		}
		if filepath.Clean(dir) == filepath.Clean(workspaceRoot) {
			return "", nil
		}
		parent := filepath.Dir(dir)
		if parent == dir || !pathWithinRoot(workspaceRoot, parent) {
			return "", nil
		}
	}
}

// uniqueSQLEngine 对最近 owning 配置执行唯一 engine 裁决，歧义时 fail-fast。
func uniqueSQLEngine(engines map[string]struct{}, filePath, configDir string) (string, bool, error) {
	if len(engines) == 0 {
		return "", false, nil
	}
	if len(engines) > 1 {
		return "", false, fmt.Errorf("ambiguous SQL dialect owners for %s in %s: %s", filePath, configDir, strings.Join(sortedStrings(engines), ", "))
	}
	for engine := range engines {
		return engine, true, nil
	}
	return "", false, nil
}

// containingSQLWorkspaceRoot 返回包含目标文件且路径最具体的可信可读根。
func containingSQLWorkspaceRoot(ctx context.Context, filePath string) (string, error) {
	root, additional, err := toolReadableRoots(ctx)
	if err != nil {
		return "", err
	}
	roots := append([]string{root}, additional...)
	best := ""
	for _, candidate := range roots {
		candidate = filepath.Clean(candidate)
		if pathWithinRoot(candidate, filePath) && len(candidate) > len(best) {
			best = candidate
		}
	}
	if best == "" {
		return "", fmt.Errorf("SQL file %s is outside trusted workspace roots", filePath)
	}
	return best, nil
}

// sqlcEnginesAtDir 汇总同一目录两种 sqlc 配置名中实际拥有目标的 engine。
func sqlcEnginesAtDir(dir, workspaceRoot, filePath string) (map[string]struct{}, error) {
	engines := make(map[string]struct{})
	for _, name := range []string{"sqlc.yaml", "sqlc.yml"} {
		configPath := filepath.Join(dir, name)
		info, err := os.Lstat(configPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("inspect SQL dialect config %s: %w", configPath, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("SQL dialect config must be a regular non-symlink file: %s", configPath)
		}
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read SQL dialect config %s: %w", configPath, err)
		}
		var config sqlcDialectConfig
		if err := yaml.Unmarshal(raw, &config); err != nil {
			return nil, fmt.Errorf("parse SQL dialect config %s: %w", configPath, err)
		}
		owned, err := sqlcConfigEnginesForFile(config, dir, workspaceRoot, filePath)
		if err != nil {
			return nil, fmt.Errorf("resolve SQL dialect config %s: %w", configPath, err)
		}
		for engine := range owned {
			engines[engine] = struct{}{}
		}
	}
	return engines, nil
}

// sqlcConfigEnginesForFile 返回单个 sqlc 配置中覆盖目标文件的全部 engine。
func sqlcConfigEnginesForFile(config sqlcDialectConfig, configDir, workspaceRoot, filePath string) (map[string]struct{}, error) {
	engines := make(map[string]struct{})
	for _, entry := range config.SQL {
		for _, queryPath := range entry.Queries {
			owns, err := sqlcQueryPathOwnsFile(configDir, workspaceRoot, queryPath, filePath)
			if err != nil {
				return nil, err
			}
			if !owns {
				continue
			}
			engine := strings.ToLower(strings.TrimSpace(entry.Engine))
			if engine == "" {
				return nil, fmt.Errorf("sqlc entry owning %s has empty engine", filePath)
			}
			engines[engine] = struct{}{}
			break
		}
	}
	return engines, nil
}

func sqlcQueryPathOwnsFile(configDir, workspaceRoot, queryPath, filePath string) (bool, error) {
	queryPath = strings.TrimSpace(queryPath)
	if queryPath == "" {
		return false, nil
	}
	resolved := queryPath
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(configDir, resolved)
	}
	resolved = filepath.Clean(resolved)
	if !pathWithinRoot(workspaceRoot, resolved) {
		return false, fmt.Errorf("sqlc query path %s escapes workspace root %s", resolved, workspaceRoot)
	}
	return pathWithinRoot(resolved, filePath), nil
}

func pathWithinRoot(root, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func sortedStrings(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
