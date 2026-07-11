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
var defaultCapabilityRoots = []string{
	"internal/contract",
	"internal/provider",
	"cmd/mcp-orch/orchestration",
	"cmd/mcp-orch/tools",
}

// capabilityManifestPath 是生成文件的规范位置，check 模式会用它比对工作区状态。
const capabilityManifestPath = "docs/doc/codemap/capability-contract/capability_manifest.json"

// main 解析 capability manifest 命令行参数，并执行生成或只检查模式。
func main() {
	check := flag.Bool("check", false, "verify generated capability manifest without modifying the worktree")
	rootsFlag := flag.String("roots", strings.Join(defaultCapabilityRoots, ","), "comma-separated roots to scan")
	outFlag := flag.String("out", capabilityManifestPath, "manifest output path")
	flag.Parse()

	repoRoot, err := findRepoRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "capcontract: %v\n", err)
		os.Exit(1)
	}
	manifest, data, err := buildCapabilityManifest(repoRoot, parseRoots(*rootsFlag), *outFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "capcontract: %v\n", err)
		os.Exit(1)
	}
	outPath := filepath.Join(repoRoot, filepath.FromSlash(*outFlag))
	if *check {
		if err := checkCapabilityManifest(outPath, data); err != nil {
			fmt.Fprintf(os.Stderr, "capcontract-check: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("capcontract: %d packages, %d functions, %d methods, %d interfaces (up to date)\n",
			manifest.Summary.TotalPackages, manifest.Summary.TotalFunctions, manifest.Summary.TotalMethods, manifest.Summary.TotalInterfaces)
		return
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "capcontract: create output dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "capcontract: write manifest: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("capcontract: %d packages, %d functions, %d methods, %d interfaces\n",
		manifest.Summary.TotalPackages, manifest.Summary.TotalFunctions, manifest.Summary.TotalMethods, manifest.Summary.TotalInterfaces)
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

// findRepoRoot 从 start 向上查找包含 go.mod 和 CLAUDE.md 的仓库根目录。
func findRepoRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "CLAUDE.md")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", start)
		}
		dir = parent
	}
}

// fileExists 判断路径是否存在且不是目录。
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
