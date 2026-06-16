package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/devtools/capcontract"
)

var defaultCapabilityRoots = []string{
	"internal/contract",
	"internal/provider",
	"internal/sidecar/orch/orchestration",
	"internal/sidecar/orch/tools",
}

const capabilityManifestPath = "docs/doc/codemap/capability-contract/capability_manifest.json"

// main 解析参数并执行命令行入口流程。
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

func existingCapabilityGeneratedAt(path string) (string, bool) {
	manifest, err := capcontract.LoadManifest(path)
	if err != nil || strings.TrimSpace(manifest.GeneratedAt) == "" {
		return "", false
	}
	return strings.TrimSpace(manifest.GeneratedAt), true
}

// findRepoRoot 查找仓库根目录。
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

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
