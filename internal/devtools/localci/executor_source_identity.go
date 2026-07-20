package localci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

type runtimeDepsLock struct {
	SchemaVersion      string             `json:"schema_version"`
	RegistryPullPolicy string             `json:"registry_pull_policy"`
	Images             []runtimeDepsImage `json:"images"`
	Inputs             runtimeDepsInputs  `json:"inputs"`
	Paths              runtimeDepsPaths   `json:"paths"`
}

type runtimeDepsInputs struct {
	Dockerfile          string `json:"dockerfile_sha256"`
	ToolchainLock       string `json:"toolchain_lock_sha256"`
	GoMod               string `json:"go_mod_sha256"`
	GoSum               string `json:"go_sum_sha256"`
	NilnessRunner       string `json:"nilness_runner_sha256"`
	NilnessGuard        string `json:"nilness_guard_sha256"`
	FrontendPackageLock string `json:"frontend_package_lock_sha256"`
	LSPPackageLock      string `json:"lsp_package_lock_sha256"`
	ProxyGoMod          string `json:"proxy_go_mod_sha256"`
	ProxyGoSum          string `json:"proxy_go_sum_sha256"`
	ToolsGoMod          string `json:"tools_go_mod_sha256"`
	ToolsGoSum          string `json:"tools_go_sum_sha256"`
	ManifestBuilder     string `json:"manifest_builder_sha256"`
	ManifestAPI         string `json:"manifest_api_sha256"`
}

type runtimeDepsPaths struct {
	Manifest            string `json:"manifest"`
	Vendor              string `json:"vendor"`
	GoModuleProxy       string `json:"go_module_proxy"`
	FrontendNodeModules string `json:"frontend_node_modules"`
	PlaywrightBrowsers  string `json:"playwright_browsers"`
	LSPNodeModules      string `json:"lsp_node_modules"`
	SQLC                string `json:"sqlc"`
	Ripgrep             string `json:"ripgrep"`
	Sqruff              string `json:"sqruff"`
	Gopls               string `json:"gopls"`
	Go                  string `json:"go"`
	Node                string `json:"node"`
	NPM                 string `json:"npm"`
	Git                 string `json:"git"`
	Make                string `json:"make"`
}

type runtimeDepsInputBinding struct {
	path   string
	digest string
}

// validateRuntimeDepsClosure 将 schema3 的每个依赖摘要绑定到候选输入闭包。
func validateRuntimeDepsClosure(lock runtimeDepsLock, closure map[string]sourceexport.TreeEntry) error {
	if lock.Paths != canonicalRuntimeDepsPaths() {
		return errors.New("runtime dependencies paths drifted from the executor contract")
	}
	for _, binding := range runtimeDepsInputBindings(lock.Inputs) {
		if err := validateRuntimeDepsInput(binding, closure); err != nil {
			return err
		}
	}
	return nil
}

// validateRuntimeDepsInput 拒绝缺失、格式错误或与候选内容不一致的依赖摘要。
func validateRuntimeDepsInput(binding runtimeDepsInputBinding, closure map[string]sourceexport.TreeEntry) error {
	if err := validateDigest("runtime dependency input "+binding.path, binding.digest); err != nil {
		return err
	}
	entry, exists := closure[binding.path]
	if !exists {
		return fmt.Errorf("runtime dependency input %q is outside the candidate closure", binding.path)
	}
	sum := sha256.Sum256(entry.Data)
	actual := "sha256:" + hex.EncodeToString(sum[:])
	if actual != binding.digest {
		return fmt.Errorf("runtime dependency input %q digest does not match candidate closure", binding.path)
	}
	return nil
}

func runtimeDepsInputBindings(inputs runtimeDepsInputs) []runtimeDepsInputBinding {
	return []runtimeDepsInputBinding{
		{"build/gate/runtime-deps.Dockerfile", inputs.Dockerfile},
		{"build/gate/toolchain.lock", inputs.ToolchainLock},
		{"go.mod", inputs.GoMod}, {"go.sum", inputs.GoSum},
		{"internal/devtools/nilnessrunner/runner.go", inputs.NilnessRunner},
		{"scripts/nilness_guard.go", inputs.NilnessGuard},
		{"frontend-app/package-lock.json", inputs.FrontendPackageLock},
		{"build/gate/runtime-lsp/package-lock.json", inputs.LSPPackageLock},
		{"build/gate/runtime-proxy/go.mod", inputs.ProxyGoMod},
		{"build/gate/runtime-proxy/go.sum", inputs.ProxyGoSum},
		{"build/gate/runtime-tools/go.mod", inputs.ToolsGoMod},
		{"build/gate/runtime-tools/go.sum", inputs.ToolsGoSum},
		{"build/gate/cmd/runtime-seed-manifest/main.go", inputs.ManifestBuilder},
		{"internal/devtools/gate/executor_seed.go", inputs.ManifestAPI},
	}
}

func canonicalRuntimeDepsPaths() runtimeDepsPaths {
	return runtimeDepsPaths{
		Manifest: "/opt/super-dolphin-gate/runtime/manifest.json", Vendor: "/opt/super-dolphin-gate/runtime/vendor",
		GoModuleProxy:       "/opt/super-dolphin-gate/runtime/go-proxy",
		FrontendNodeModules: "/opt/super-dolphin-gate/runtime/frontend/node_modules",
		PlaywrightBrowsers:  "/opt/super-dolphin-gate/runtime/frontend/node_modules/.cache/ms-playwright",
		LSPNodeModules:      "/opt/super-dolphin-gate/runtime/lsp/node_modules",
		SQLC:                "/opt/super-dolphin-gate/runtime/bin/sqlc", Ripgrep: "/opt/super-dolphin-gate/runtime/bin/rg",
		Sqruff: "/opt/super-dolphin-gate/runtime/bin/sqruff", Gopls: "/usr/local/bin/gopls",
		Go: "/usr/local/go/bin/go", Node: "/usr/local/bin/node", NPM: "/usr/local/bin/npm",
		Git: "/usr/bin/git", Make: "/usr/bin/make",
	}
}

// verifyObjectClosure 要求导入仓中的指定 commits 具备完整且严格有效的对象闭包。
func verifyObjectClosure(ctx context.Context, bareRoot string, commits ...string) error {
	args := []string{"fsck", "--full", "--strict", "--no-reflogs", "--"}
	output, err := runGitOutput(ctx, bareRoot, nil, append(args, commits...)...)
	return rejectGitOutput(output, err, "verify source object closure")
}

// createSourceBundle 只从受控 refs 构造 bundle，并将产物权限收紧为只读。
func createSourceBundle(ctx context.Context, bareRoot string, bundlePath string, includeBase bool) error {
	args := []string{"bundle", "create", bundlePath, "--end-of-options", sourceBundleRef}
	if includeBase {
		args = append(args, sourceBundleBaseRef)
	}
	output, err := runGitOutput(ctx, bareRoot, nil, args...)
	if err := rejectGitOutput(output, err, "create source bundle"); err != nil {
		return err
	}
	if err := os.Chmod(bundlePath, privateSourceFileMode); err != nil {
		return fmt.Errorf("protect source bundle: %w", err)
	}
	return nil
}

// rejectGitOutput 要求无 stdout 的 Git plumbing 命令同时成功且保持静默。
func rejectGitOutput(output []byte, err error, action string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	if len(output) != 0 {
		return fmt.Errorf("%s returned unexpected output: %s", action, strings.TrimSpace(string(output)))
	}
	return nil
}

// importAndVerifyBundle 在新建 bare repo 中导入 bundle 并复核广告 ref 与对象闭包。
func importAndVerifyBundle(ctx context.Context, bundlePath string, tempParent string, manifest SourceMaterializationManifest) (err error) {
	importRoot, err := os.MkdirTemp(tempParent, ".source-import-")
	if err != nil {
		return fmt.Errorf("create source import root: %w", err)
	}
	defer func() { err = errors.Join(err, removeSourceTemp(importRoot)) }()
	if err := os.Chmod(importRoot, privateSourceDirMode); err != nil {
		return fmt.Errorf("protect source import root: %w", err)
	}
	bareRoot := filepath.Join(importRoot, "verify.git")
	if err := initBareRepository(ctx, importRoot, bareRoot, manifest.ObjectFormat); err != nil {
		return err
	}
	if _, err := runGitOutput(ctx, bareRoot, nil, "bundle", "verify", bundlePath); err != nil {
		return fmt.Errorf("verify source bundle: %w", err)
	}
	output, err := runGitOutput(ctx, bareRoot, nil, "bundle", "unbundle", bundlePath)
	if err != nil {
		return fmt.Errorf("import source bundle: %w", err)
	}
	if string(output) != expectedBundleRefs(manifest) {
		return errors.New("source bundle advertised unexpected or trailing refs")
	}
	return verifyImportedSource(ctx, bareRoot, manifest)
}

// expectedBundleRefs 编码 bundle 必须广告的 materialized 与可选可信 base refs。
func expectedBundleRefs(manifest SourceMaterializationManifest) string {
	head := fmt.Sprintf("%s %s\n", manifest.MaterializedCommitSHA, sourceBundleRef)
	if base := trustedSourceBase(manifest); base != "" {
		return head + fmt.Sprintf("%s %s\n", base, sourceBundleBaseRef)
	}
	return head
}

// verifyImportedSource 复核 bundle 中 head、可选可信 base 及其完整对象闭包。
func verifyImportedSource(ctx context.Context, bareRoot string, manifest SourceMaterializationManifest) error {
	object, err := readSourceObject(ctx, bareRoot, manifest.MaterializedCommitSHA)
	if err != nil {
		return err
	}
	_, parents, err := parseCommitObject(object, manifest.SourceTreeSHA)
	if err != nil {
		return err
	}
	if err := verifyImportedParentIdentity(manifest, parents); err != nil {
		return err
	}
	commits := []string{manifest.MaterializedCommitSHA}
	if base := trustedSourceBase(manifest); base != "" {
		object, err := readSourceObject(ctx, bareRoot, base)
		if err != nil || object.kind != "commit" {
			return errors.Join(errors.New("imported trusted source base is not a commit"), err)
		}
		commits = append(commits, base)
	}
	return verifyObjectClosure(ctx, bareRoot, commits...)
}

// trustedSourceBase 从已校验 manifest 中提取 materializer 明确发布的 canonical base。
func trustedSourceBase(manifest SourceMaterializationManifest) string {
	return manifest.TrustedBaseCommitSHA
}

// verifyImportedParentIdentity 约束 canonical base 与导入 commit 的真实 parent 身份一致。
func verifyImportedParentIdentity(manifest SourceMaterializationManifest, parents []string) error {
	switch manifest.Source.Kind {
	case gate.SourceKindCommit:
		if len(parents) == 1 {
			if manifest.TrustedBaseCommitSHA != parents[0] {
				return errors.New("imported commit parent does not match trusted source base")
			}
		} else if manifest.TrustedBaseCommitSHA != "" {
			return errors.New("parentless or merge commit must not advertise a trusted source base")
		}
	case gate.SourceKindTree:
		if manifest.Source.Tree.ParentCommitSHA == "" {
			return nil
		}
		if len(parents) != 1 || parents[0] != manifest.Source.Tree.ParentCommitSHA {
			return errors.New("imported synthetic commit parent does not match SourceSpec")
		}
	}
	return nil
}
