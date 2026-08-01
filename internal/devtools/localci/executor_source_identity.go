package localci

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- Git SHA-1 object IDs require SHA-1 compatibility verification.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

type runtimeDepsLock struct {
	SchemaVersion string            `json:"schema_version"`
	BuildMode     string            `json:"build_mode"`
	CacheScope    string            `json:"cache_scope"`
	Inputs        runtimeDepsInputs `json:"inputs"`
	Paths         runtimeDepsPaths  `json:"paths"`
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
	RuntimeSeedWorker   string `json:"runtime_seed_worker_sha256"`
	RuntimeSeedRecipe   string `json:"runtime_seed_recipe_sha256"`
	RuntimeSeedScript   string `json:"runtime_seed_script_sha256"`
	RuntimeSeedBrowser  string `json:"runtime_seed_script_browser_sha256"`
	RuntimeSeedRuntime  string `json:"runtime_seed_script_runtime_sha256"`
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

// validateRuntimeDepsClosure 将 node-local 依赖摘要绑定到候选输入闭包。
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
		{"internal/devtools/gate/executor_seed.go", inputs.RuntimeSeedWorker},
		{"cmd/super-dolphin-gate/remote_refresh_seed.go", inputs.RuntimeSeedRecipe},
		{"cmd/super-dolphin-gate/remote_refresh_seed_script.go", inputs.RuntimeSeedScript},
		{"cmd/super-dolphin-gate/remote_refresh_seed_script_browser.go", inputs.RuntimeSeedBrowser},
		{"cmd/super-dolphin-gate/remote_refresh_seed_script_runtime.go", inputs.RuntimeSeedRuntime},
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

type canonicalContext struct {
	Tar           []byte
	ContextDigest string
	InputDigest   string
}

// buildCanonicalContext 将已验证 Git blob 规范化为稳定 tar 和输入摘要。
func buildCanonicalContext(sourceEntries []sourceexport.TreeEntry) (canonicalContext, error) {
	if len(sourceEntries) == 0 {
		return canonicalContext{}, errors.New("canonical context requires at least one source entry")
	}
	entries := append([]sourceexport.TreeEntry(nil), sourceEntries...)
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Path < entries[right].Path })

	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	var manifest []byte
	seenPaths := make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := validateContextEntry(entry, seenPaths); err != nil {
			return canonicalContext{}, err
		}
		mode := int64(0o644)
		if entry.Mode == "100755" {
			mode = 0o755
		}
		header := &tar.Header{
			Name: entry.Path, Mode: mode, Size: int64(len(entry.Data)), Typeflag: tar.TypeReg,
			ModTime: time.Unix(0, 0), Uid: 0, Gid: 0, Format: tar.FormatPAX,
		}
		if err := writer.WriteHeader(header); err != nil {
			return canonicalContext{}, fmt.Errorf("write canonical header %q: %w", entry.Path, err)
		}
		if _, err := writer.Write(entry.Data); err != nil {
			return canonicalContext{}, fmt.Errorf("write canonical content %q: %w", entry.Path, err)
		}
		contentHash := sha256.Sum256(entry.Data)
		manifest = appendManifestField(manifest, entry.Path)
		manifest = appendManifestField(manifest, entry.Mode)
		manifest = appendManifestField(manifest, entry.Hash)
		manifest = appendManifestField(manifest, hex.EncodeToString(contentHash[:]))
	}
	if err := writer.Close(); err != nil {
		return canonicalContext{}, fmt.Errorf("close canonical context: %w", err)
	}
	contextHash := sha256.Sum256(archive.Bytes())
	inputHash := sha256.Sum256(manifest)
	return canonicalContext{
		Tar:           archive.Bytes(),
		ContextDigest: "sha256:" + hex.EncodeToString(contextHash[:]),
		InputDigest:   "sha256:" + hex.EncodeToString(inputHash[:]),
	}, nil
}

func validateContextEntry(entry sourceexport.TreeEntry, seenPaths map[string]string) error {
	if err := validateContextPath(entry.Path, seenPaths); err != nil {
		return err
	}
	if entry.Mode != "100644" && entry.Mode != "100755" {
		return fmt.Errorf("source entry %q has unsupported mode %q", entry.Path, entry.Mode)
	}
	return validateContextBlob(entry)
}

// validateContextPath 拒绝非规范路径和大小写折叠冲突。
func validateContextPath(entryPath string, seenPaths map[string]string) error {
	if entryPath == "" || entryPath == "." || path.Clean(entryPath) != entryPath || path.IsAbs(entryPath) {
		return fmt.Errorf("source entry path %q is not canonical", entryPath)
	}
	if strings.HasPrefix(entryPath, "../") || strings.ContainsAny(entryPath, "\\\x00") {
		return fmt.Errorf("source entry path %q is not canonical", entryPath)
	}
	foldedPath := strings.ToLower(entryPath)
	if previousPath, exists := seenPaths[foldedPath]; exists {
		return fmt.Errorf("source entry path %q collides with %q", entryPath, previousPath)
	}
	seenPaths[foldedPath] = entryPath
	return nil
}

func validateContextBlob(entry sourceexport.TreeEntry) error {
	if entry.Hash == "" {
		return fmt.Errorf("source entry %q is missing verified Git object hash", entry.Path)
	}
	calculatedHash, err := gitBlobHash(entry.Hash, entry.Data)
	if err != nil {
		return fmt.Errorf("source entry %q: %w", entry.Path, err)
	}
	if calculatedHash != entry.Hash {
		return fmt.Errorf("source entry %q data does not match Git blob %s", entry.Path, entry.Hash)
	}
	return nil
}

func gitBlobHash(objectID string, data []byte) (string, error) {
	payload := fmt.Appendf(nil, "blob %d\x00", len(data))
	payload = append(payload, data...)
	switch len(objectID) {
	case sha1.Size * 2:
		sum := sha1.Sum(payload) // #nosec G401 -- this verifies Git's object format, not a security signature.
		return hex.EncodeToString(sum[:]), nil
	case sha256.Size * 2:
		sum := sha256.Sum256(payload)
		return hex.EncodeToString(sum[:]), nil
	default:
		return "", fmt.Errorf("Git blob object ID length %d is unsupported", len(objectID))
	}
}

func appendManifestField(manifest []byte, value string) []byte {
	manifest = append(manifest, value...)
	return append(manifest, 0)
}

type gitTreeNode struct {
	files map[string]sourceexport.TreeEntry
	dirs  map[string]*gitTreeNode
}

type gitTreeItem struct {
	name      string
	mode      string
	oid       []byte
	directory bool
}

// calculateGitTreeOID 从扁平 blob 条目重建 Git tree 对象并计算根 OID。
func calculateGitTreeOID(format gate.GitObjectFormat, entries []sourceexport.TreeEntry) (string, error) {
	root := newGitTreeNode()
	seenPaths := make(map[string]string, len(entries))
	expectedLength, err := gitOIDHexLength(format)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if err := validateGitTreeEntry(entry, expectedLength, seenPaths); err != nil {
			return "", err
		}
		if err := root.insert(entry); err != nil {
			return "", err
		}
	}
	oid, err := hashGitTreeNode(format, root)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(oid), nil
}

func validateGitTreeEntry(entry sourceexport.TreeEntry, expectedLength int, seenPaths map[string]string) error {
	if err := validateContextEntry(entry, seenPaths); err != nil {
		return err
	}
	if len(entry.Hash) != expectedLength || strings.ToLower(entry.Hash) != entry.Hash {
		return fmt.Errorf("source entry %q has an object ID incompatible with the SourceSpec object format", entry.Path)
	}
	if _, err := hex.DecodeString(entry.Hash); err != nil {
		return fmt.Errorf("source entry %q has an invalid Git object ID: %w", entry.Path, err)
	}
	return nil
}

func newGitTreeNode() *gitTreeNode {
	return &gitTreeNode{files: make(map[string]sourceexport.TreeEntry), dirs: make(map[string]*gitTreeNode)}
}

// insert 将规范文件路径插入 tree，不允许文件与目录互相遮蔽。
func (node *gitTreeNode) insert(entry sourceexport.TreeEntry) error {
	parts := strings.Split(entry.Path, "/")
	current := node
	for _, directory := range parts[:len(parts)-1] {
		if _, exists := current.files[directory]; exists {
			return fmt.Errorf("source path %q crosses file %q", entry.Path, directory)
		}
		next, exists := current.dirs[directory]
		if !exists {
			next = newGitTreeNode()
			current.dirs[directory] = next
		}
		current = next
	}
	name := parts[len(parts)-1]
	if _, exists := current.dirs[name]; exists {
		return fmt.Errorf("source path %q conflicts with a directory", entry.Path)
	}
	if _, exists := current.files[name]; exists {
		return fmt.Errorf("source path %q is duplicated", entry.Path)
	}
	current.files[name] = entry
	return nil
}

// hashGitTreeNode 按 Git tree 排序和二进制编码递归计算节点 OID。
func hashGitTreeNode(format gate.GitObjectFormat, node *gitTreeNode) ([]byte, error) {
	items := make([]gitTreeItem, 0, len(node.files)+len(node.dirs))
	for name, entry := range node.files {
		oid, err := hex.DecodeString(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("decode blob object %q: %w", entry.Path, err)
		}
		items = append(items, gitTreeItem{name: name, mode: entry.Mode, oid: oid})
	}
	for name, child := range node.dirs {
		oid, err := hashGitTreeNode(format, child)
		if err != nil {
			return nil, err
		}
		items = append(items, gitTreeItem{name: name, mode: "40000", oid: oid, directory: true})
	}
	sort.Slice(items, func(left int, right int) bool { return gitTreeItemLess(items[left], items[right]) })
	var body bytes.Buffer
	for _, item := range items {
		fmt.Fprintf(&body, "%s %s", item.mode, item.name)
		body.WriteByte(0)
		body.Write(item.oid)
	}
	return hashGitObject(format, "tree", body.Bytes())
}

func gitTreeItemLess(left gitTreeItem, right gitTreeItem) bool {
	leftSuffix := byte(0)
	if left.directory {
		leftSuffix = '/'
	}
	rightSuffix := byte(0)
	if right.directory {
		rightSuffix = '/'
	}
	return bytes.Compare(append([]byte(left.name), leftSuffix), append([]byte(right.name), rightSuffix)) < 0
}

func hashGitObject(format gate.GitObjectFormat, objectType string, body []byte) ([]byte, error) {
	payload := fmt.Appendf(nil, "%s %d\x00", objectType, len(body))
	payload = append(payload, body...)
	switch format {
	case gate.GitObjectFormatSHA1:
		sum := sha1.Sum(payload) // #nosec G401 -- this verifies Git's object format, not a security signature.
		return sum[:], nil
	case gate.GitObjectFormatSHA256:
		sum := sha256.Sum256(payload)
		return sum[:], nil
	default:
		return nil, fmt.Errorf("unsupported Git object format %q", format)
	}
}

func gitOIDHexLength(format gate.GitObjectFormat) (int, error) {
	switch format {
	case gate.GitObjectFormatSHA1:
		return sha1.Size * 2, nil
	case gate.GitObjectFormatSHA256:
		return sha256.Size * 2, nil
	default:
		return 0, fmt.Errorf("unsupported Git object format %q", format)
	}
}

// ReadOnlyGitTree 携带从 SourceSpec 对应 Git tree 取得的只读源码，不读取进程工作目录。
type ReadOnlyGitTree struct {
	Source  gate.SourceSpec
	Entries []sourceexport.TreeEntry
}

// GateImageInputs 是一个 job tree 经校验后的确定性镜像输入视图。
type GateImageInputs struct {
	SubmittedSourceTree string
	PolicyDigest        string
	ImageSchemaVersion  string
	Platform            string
	SourceEntries       []sourceexport.TreeEntry
	ImageInputDigest    string
	ContextDigest       string
	InputManifestDigest string
	ToolchainDigest     string
	DockerfileDigest    string
	GateSourceDigest    string
}

// BaselineGateCompileInputs 固化历史基线树中的 Gate 编译闭包与工具链身份。
type BaselineGateCompileInputs struct {
	ToolchainDigest  string
	GateSourceDigest string
}

func verifyReadOnlyGitTree(tree ReadOnlyGitTree) error {
	if err := tree.Source.Validate(); err != nil {
		return fmt.Errorf("validate image source spec: %w", err)
	}
	if len(tree.Entries) == 0 {
		return errors.New("read-only Git tree entries are required")
	}
	calculated, err := calculateGitTreeOID(tree.Source.ObjectFormat, tree.Entries)
	if err != nil {
		return fmt.Errorf("verify read-only Git tree: %w", err)
	}
	if calculated != tree.Source.SourceTreeSHA {
		return fmt.Errorf("read-only Git tree drift: calculated %s, expected %s", calculated, tree.Source.SourceTreeSHA)
	}
	return nil
}

func cloneTreeEntries(entries []sourceexport.TreeEntry) []sourceexport.TreeEntry {
	cloned := make([]sourceexport.TreeEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].Data = append([]byte(nil), entry.Data...)
		cloned[index].Path = path.Clean(entry.Path)
	}
	return cloned
}
