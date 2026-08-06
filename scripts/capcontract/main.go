package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

// defaultCapabilityRoots 是 capability manifest 默认扫描的跨模块契约根目录。
func defaultCapabilityRoots() []string {
	return []string{
		"internal/contract",
		"internal/provider",
		"cmd/mcp-orch/orchestration",
		"cmd/mcp-orch/tools",
	}
}

// capabilityManifestPath 是生成文件的规范位置，check 模式会用它比对工作区状态。
const capabilityManifestPath = "docs/doc/codemap/capability-contract/capability_manifest.json"

// main 解析 capability manifest 命令行参数，并执行生成或只检查模式。
func main() {
	check := flag.Bool("check", false, "verify generated capability manifest without modifying the worktree")
	rootsFlag := newCapabilityRootsFlag(flag.CommandLine)
	outFlag := flag.String("out", capabilityManifestPath, "manifest output path")
	printPathRules := flag.Bool("print-path-rules", false, "print capability-contract path rules as kind<TAB>path and exit")
	flag.Parse()

	repoRoot, pathRules, err := loadCapabilityPathRules()
	if err != nil {
		fmt.Fprintf(os.Stderr, "capcontract: %v\n", err)
		os.Exit(1)
	}
	if *printPathRules {
		if err := printCapabilityPathRules(pathRules); err != nil {
			fmt.Fprintf(os.Stderr, "capcontract: render path rules: %v\n", err)
			os.Exit(1)
		}
		return
	}
	roots := selectedCapabilityRoots(pathRules, *rootsFlag, flagWasSet(flag.CommandLine, "roots"))
	errorPrefix, err := runCapabilityManifest(repoRoot, roots, *outFlag, *check)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", errorPrefix, err)
		os.Exit(1)
	}
}

// newCapabilityRootsFlag 在指定 FlagSet 上注册隔离的 --roots 默认值。
// defaultCapabilityRoots 仍是 path rules AST 读取的唯一规范函数。
func newCapabilityRootsFlag(flagSet *flag.FlagSet) *string {
	return flagSet.String("roots", strings.Join(defaultCapabilityRoots(), ","), "comma-separated roots to scan")
}

func flagWasSet(flagSet *flag.FlagSet, name string) bool {
	wasSet := false
	flagSet.Visit(func(value *flag.Flag) {
		if value.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

// loadCapabilityPathRules 定位仓库并加载 generator AST 派生的路径规则。
func loadCapabilityPathRules() (string, capcontract.PathRules, error) {
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		return "", capcontract.PathRules{}, err
	}
	pathRules, err := capcontract.LoadPathRules(repoRoot)
	return repoRoot, pathRules, err
}

// printCapabilityPathRules 输出共享 shell selector 使用的稳定 TSV。
func printCapabilityPathRules(pathRules capcontract.PathRules) error {
	lines, err := pathRules.MachineLines()
	if err != nil {
		return err
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	return nil
}

// selectedCapabilityRoots 仅在调用方显式传入 --roots 时覆盖 AST 默认根目录。
func selectedCapabilityRoots(pathRules capcontract.PathRules, rootsFlag string, rootsWasSet bool) []string {
	if rootsWasSet {
		return parseRoots(rootsFlag)
	}
	return pathRules.DefaultRoots
}

// runCapabilityManifest 执行 manifest 检查或刷新，并返回错误对应的命令前缀。
func runCapabilityManifest(repoRoot string, roots []string, outFlag string, check bool) (string, error) {
	manifest, data, err := buildCapabilityManifest(repoRoot, roots, outFlag)
	if err != nil {
		return "capcontract", err
	}
	outPath := filepath.Join(repoRoot, filepath.FromSlash(outFlag))
	if check {
		if err := checkCapabilityManifest(outPath, data); err != nil {
			return "capcontract-check", err
		}
		fmt.Printf("capcontract: %d packages, %d functions, %d methods, %d interfaces (up to date)\n",
			manifest.Summary.TotalPackages, manifest.Summary.TotalFunctions, manifest.Summary.TotalMethods, manifest.Summary.TotalInterfaces)
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "capcontract", fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return "capcontract", fmt.Errorf("write manifest: %w", err)
	}
	fmt.Printf("capcontract: %d packages, %d functions, %d methods, %d interfaces\n",
		manifest.Summary.TotalPackages, manifest.Summary.TotalFunctions, manifest.Summary.TotalMethods, manifest.Summary.TotalInterfaces)
	return "", nil
}

// buildCapabilityManifest 扫描源码并返回 manifest 及其稳定 JSON 表示。
// 已有 manifest 的 generated_at 会被复用，避免 check 模式因为日期漂移失败。
func buildCapabilityManifest(repoRoot string, roots []string, outPath string) (*capcontract.Manifest, []byte, error) {
	generatedAt := time.Now().Format("2006-01-02")
	if existing, ok := existingCapabilityGeneratedAt(filepath.Join(repoRoot, filepath.FromSlash(outPath))); ok {
		generatedAt = existing
	}
	manifest, err := capcontract.Scan(capcontract.ScanOptions{RepoRoot: repoRoot, Roots: roots, GeneratedAt: generatedAt})
	if err != nil {
		return nil, nil, err
	}
	data, err := capcontract.MarshalManifest(manifest)
	if err != nil {
		return nil, nil, err
	}
	return manifest, data, nil
}

// checkCapabilityManifest 对比磁盘文件和新生成内容，不匹配时提示调用生成命令。
func checkCapabilityManifest(path string, want []byte) error {
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%s differs from generated output; run `go run scripts/capcontract.go`", path)
	}
	return nil
}

// parseRoots 解析逗号分隔的扫描根目录，并统一为 slash 路径。
func parseRoots(raw string) []string {
	parts := strings.Split(raw, ",")
	roots := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			roots = append(roots, filepath.ToSlash(part))
		}
	}
	return roots
}

// existingCapabilityGeneratedAt 读取已有 manifest 日期，读取失败时返回 ok=false。
func existingCapabilityGeneratedAt(path string) (string, bool) {
	manifest, err := capcontract.LoadManifest(path)
	if err != nil || strings.TrimSpace(manifest.GeneratedAt) == "" {
		return "", false
	}
	return strings.TrimSpace(manifest.GeneratedAt), true
}
