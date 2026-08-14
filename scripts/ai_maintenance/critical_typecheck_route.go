package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const criticalTypecheckRegistryPath = "frontend-app/scripts/critical-typecheck-files.json"

var criticalTypecheckRegistryKeys = [...]string{
	"entrypoints",
	"productionFiles",
	"schemaVersion",
	"surfaces",
	"testFiles",
}

var criticalTypecheckRequiredSurfaces = [...]string{
	"actionFeedback",
	"diagnostics",
	"promptHistory",
	"providerPreference",
	"rpcAdapter",
	"storeBridge",
	"terminalPublicError",
	"uiAction",
}

var criticalTypecheckRequiredTestFiles = [...]string{"scripts/contracts-typecheck-guard.test.mjs"}

type criticalTypecheckRegistry struct {
	SchemaVersion   int                 `json:"schemaVersion"`
	Surfaces        map[string][]string `json:"surfaces"`
	Entrypoints     []string            `json:"entrypoints"`
	ProductionFiles []string            `json:"productionFiles"`
	TestFiles       []string            `json:"testFiles"`
}

// criticalTypecheckRelevant 只把 registry 生产闭包及其执行种子路由到重型严格类型检查。
func criticalTypecheckRelevant(repoRoot, file string) (bool, error) {
	if criticalTypecheckExecutionSeed(file) {
		return true, nil
	}
	if !strings.HasPrefix(file, "frontend-app/src/") {
		return false, nil
	}
	files, err := loadCriticalTypecheckProductionFiles(repoRoot)
	if err != nil {
		return false, err
	}
	return files[strings.TrimPrefix(file, "frontend-app/")], nil
}

func criticalTypecheckExecutionSeed(file string) bool {
	seeds := map[string]bool{
		"frontend-app/jsconfig.json":                              true,
		"frontend-app/package.json":                               true,
		"frontend-app/package-lock.json":                          true,
		"frontend-app/tsconfig.contracts.json":                    true,
		"frontend-app/scripts/contracts-typecheck-guard.test.mjs": true,
		"frontend-app/scripts/critical-typecheck-files.json":      true,
		"frontend-app/scripts/critical-typecheck-guard.mjs":       true,
	}
	return seeds[file]
}

// loadCriticalTypecheckProductionFiles 严格读取现行前端 registry，漂移或非法路径立即阻断计划生成。
func loadCriticalTypecheckProductionFiles(repoRoot string) (map[string]bool, error) {
	registryFile := filepath.Join(repoRoot, filepath.FromSlash(criticalTypecheckRegistryPath))
	raw, err := os.ReadFile(registryFile)
	if err != nil {
		return nil, fmt.Errorf("read critical typecheck registry: %w", err)
	}
	registry, err := decodeCriticalTypecheckRegistry(raw)
	if err != nil {
		return nil, err
	}
	return validateCriticalTypecheckRegistry(repoRoot, registry)
}

// decodeCriticalTypecheckRegistry 先锁定顶层字段集合，再解码 registry 结构，拒绝未知或缺失字段。
func decodeCriticalTypecheckRegistry(raw []byte) (criticalTypecheckRegistry, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return criticalTypecheckRegistry{}, fmt.Errorf("decode critical typecheck registry: %w", err)
	}
	if object == nil {
		return criticalTypecheckRegistry{}, fmt.Errorf("critical typecheck registry must be an object")
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	if err := criticalTypecheckAssertExactSet("critical typecheck registry keys", criticalTypecheckRegistryKeys[:], keys); err != nil {
		return criticalTypecheckRegistry{}, err
	}
	var registry criticalTypecheckRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		return criticalTypecheckRegistry{}, fmt.Errorf("decode critical typecheck registry: %w", err)
	}
	return registry, nil
}

// validateCriticalTypecheckRegistry 镜像前端 guard 的 registry 结构约束，并返回生产闭包索引。
func validateCriticalTypecheckRegistry(repoRoot string, registry criticalTypecheckRegistry) (map[string]bool, error) {
	if registry.SchemaVersion != 1 {
		return nil, fmt.Errorf("critical typecheck registry schemaVersion must be 1")
	}
	surfaceFiles, err := criticalTypecheckSurfaceFiles(registry.Surfaces)
	if err != nil {
		return nil, err
	}
	entrypoints, err := criticalTypecheckSortedUnique(registry.Entrypoints, "critical typecheck entrypoints")
	if err != nil {
		return nil, err
	}
	productionFiles, err := criticalTypecheckSortedUnique(registry.ProductionFiles, "critical typecheck productionFiles")
	if err != nil {
		return nil, err
	}
	testFiles, err := criticalTypecheckSortedUnique(registry.TestFiles, "critical typecheck testFiles")
	if err != nil {
		return nil, err
	}
	if err := criticalTypecheckAssertExactSet("critical typecheck testFiles", criticalTypecheckRequiredTestFiles[:], testFiles); err != nil {
		return nil, err
	}
	if err := criticalTypecheckAssertExactSet("critical typecheck surface entrypoints", entrypoints, criticalTypecheckUniqueSorted(surfaceFiles)); err != nil {
		return nil, err
	}
	return validateCriticalTypecheckRegistryPaths(repoRoot, entrypoints, productionFiles, testFiles)
}

// criticalTypecheckSurfaceFiles 校验风险面名称和每个 surface 的非空路径集合。
func criticalTypecheckSurfaceFiles(surfaces map[string][]string) ([]string, error) {
	if surfaces == nil {
		return nil, fmt.Errorf("critical typecheck registry surfaces must be an object")
	}
	surfaceNames := make([]string, 0, len(surfaces))
	for name := range surfaces {
		surfaceNames = append(surfaceNames, name)
	}
	if err := criticalTypecheckAssertExactSet("critical typecheck surfaces", criticalTypecheckRequiredSurfaces[:], surfaceNames); err != nil {
		return nil, err
	}
	surfaceFiles := make([]string, 0)
	for _, name := range criticalTypecheckRequiredSurfaces {
		files, err := criticalTypecheckSortedUnique(surfaces[name], "surface "+name)
		if err != nil {
			return nil, err
		}
		surfaceFiles = append(surfaceFiles, files...)
	}
	return surfaceFiles, nil
}

// validateCriticalTypecheckRegistryPaths 确认生产、入口和守卫测试路径都绑定在传入仓库根目录。
func validateCriticalTypecheckRegistryPaths(repoRoot string, entrypoints, productionFiles, testFiles []string) (map[string]bool, error) {
	productionSet := make(map[string]bool, len(productionFiles))
	for _, candidate := range productionFiles {
		if err := validateCriticalTypecheckProductionFile(repoRoot, candidate); err != nil {
			return nil, err
		}
		productionSet[candidate] = true
	}
	for _, entrypoint := range entrypoints {
		if !productionSet[entrypoint] {
			return nil, fmt.Errorf("critical typecheck entrypoint is absent from productionFiles: %s", entrypoint)
		}
	}
	for _, candidate := range testFiles {
		if err := validateCriticalTypecheckTestFile(repoRoot, candidate); err != nil {
			return nil, err
		}
	}
	return productionSet, nil
}

// criticalTypecheckSortedUnique 复现前端 guard 的 trim、slash 归一化和去重规则。
func criticalTypecheckSortedUnique(values []string, label string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty array", label)
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s must contain non-empty strings", label)
		}
		value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
		if seen[value] {
			return nil, fmt.Errorf("%s contains duplicate paths", label)
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized, nil
}

// criticalTypecheckUniqueSorted 将多个 surface 的路径折叠为稳定集合，匹配前端 guard 的 Set 语义。
func criticalTypecheckUniqueSorted(values []string) []string {
	unique := make(map[string]bool, len(values))
	for _, value := range values {
		unique[value] = true
	}
	result := make([]string, 0, len(unique))
	for value := range unique {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

// criticalTypecheckAssertExactSet 以缺失和陈旧两侧差集拒绝 registry 漂移。
func criticalTypecheckAssertExactSet(label string, expected, actual []string) error {
	expectedSet := make(map[string]bool, len(expected))
	actualSet := make(map[string]bool, len(actual))
	for _, value := range expected {
		expectedSet[value] = true
	}
	for _, value := range actual {
		actualSet[value] = true
	}
	missing := make([]string, 0)
	for _, value := range expected {
		if !actualSet[value] {
			missing = append(missing, value)
		}
	}
	stale := make([]string, 0)
	for _, value := range actual {
		if !expectedSet[value] {
			stale = append(stale, value)
		}
	}
	if len(missing) == 0 && len(stale) == 0 {
		return nil
	}
	return fmt.Errorf("%s exact diff failed: missing=[%s] stale=[%s]", label, strings.Join(missing, ", "), strings.Join(stale, ", "))
}

// validateCriticalTypecheckTestFile 校验 guard 测试路径位于前端 scripts 且为普通文件。
func validateCriticalTypecheckTestFile(repoRoot, candidate string) error {
	if candidate == "" || path.IsAbs(candidate) || path.Clean(candidate) != candidate || strings.Contains(candidate, `\`) || !strings.HasPrefix(candidate, "scripts/") {
		return fmt.Errorf("critical typecheck registry test path is invalid: %q", candidate)
	}
	info, err := os.Stat(filepath.Join(repoRoot, "frontend-app", filepath.FromSlash(candidate)))
	if err != nil {
		return fmt.Errorf("stat critical typecheck test path %q: %w", candidate, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("critical typecheck test path is not a regular file: %q", candidate)
	}
	return nil
}

// validateCriticalTypecheckProductionFile 校验生产路径位于前端 src 且为 JS/JSX 普通文件。
func validateCriticalTypecheckProductionFile(repoRoot, candidate string) error {
	if candidate == "" || path.IsAbs(candidate) || path.Clean(candidate) != candidate {
		return fmt.Errorf("critical typecheck registry production path is invalid: %q", candidate)
	}
	if strings.Contains(candidate, "\\") || !strings.HasPrefix(candidate, "src/") {
		return fmt.Errorf("critical typecheck registry production path is invalid: %q", candidate)
	}
	switch path.Ext(candidate) {
	case ".js", ".jsx":
	default:
		return fmt.Errorf("critical typecheck registry production path has unsupported extension: %q", candidate)
	}
	info, err := os.Stat(filepath.Join(repoRoot, "frontend-app", filepath.FromSlash(candidate)))
	if err != nil {
		return fmt.Errorf("stat critical typecheck production path %q: %w", candidate, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("critical typecheck production path is not a regular file: %q", candidate)
	}
	return nil
}
