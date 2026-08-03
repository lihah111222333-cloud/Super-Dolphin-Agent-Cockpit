package remoteci

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" // #nosec G505 -- Git SHA-1 object IDs require SHA-1 compatibility verification.
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

// verifyObjectClosure 要求导入仓中的指定 commits 具备完整且严格有效的对象闭包。
func verifyObjectClosure(ctx context.Context, bareRoot string, commits ...string) error {
	args := []string{"fsck", "--full", "--strict", "--no-reflogs", "--"}
	output, err := runGitOutput(ctx, bareRoot, nil, append(args, commits...)...)
	return rejectGitOutput(output, err, "verify source object closure")
}

// createSourceBundle 只从候选 ref 构造带单一 accepted baseline prerequisite
// 的 Git bundle；baseline objects 由 worker 的只读 image store 提供。
func createSourceBundle(ctx context.Context, bareRoot string, bundlePath string, baseline SourceBaseline, materializedCommit string) error {
	if !validOID(baseline.CommitSHA, baseline.ObjectFormat) || !validOID(materializedCommit, baseline.ObjectFormat) {
		return errors.New("source bundle baseline prerequisite commit is invalid")
	}
	args := []string{"bundle", "create", bundlePath, "--end-of-options", sourceBundleRef, "^" + baseline.CommitSHA}
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
func importAndVerifyBundle(ctx context.Context, bundlePath string, tempParent string, manifest SourceMaterializationManifest, baseline SourceBaseline) (err error) {
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
	if err := configureObjectStoreAlternate(ctx, bareRoot, baseline); err != nil {
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
	if err := verifyBundlePrerequisites(bundlePath, manifest, baseline); err != nil {
		return err
	}
	return verifyImportedSource(ctx, bareRoot, manifest, baseline)
}

// expectedBundleRefs 编码 bundle 必须广告的唯一候选 ref；baseline 是
// prerequisite，不得作为 bundle ref 或实际上传对象出现。
func expectedBundleRefs(manifest SourceMaterializationManifest) string {
	return fmt.Sprintf("%s %s\n", manifest.TransportCommitSHA, sourceBundleRef)
}

// verifyImportedSource 复核 deterministic transport commit 的 tree 与唯一
// accepted baseline parent，并验证其候选 tree 闭包。原始 SourceSpec commit /
// range / tree identity 仅由 manifest.Source 保存，绝不被 transport ref 替代。
func verifyImportedSource(ctx context.Context, bareRoot string, manifest SourceMaterializationManifest, baseline SourceBaseline) error {
	object, err := readSourceObject(ctx, bareRoot, manifest.TransportCommitSHA)
	if err != nil {
		return err
	}
	_, parents, err := parseCommitObject(object, manifest.SourceTreeSHA)
	if err != nil {
		return err
	}
	if len(parents) != 1 || parents[0] != baseline.CommitSHA {
		return errors.New("transport commit parent does not match accepted image baseline")
	}
	expected, err := DeterministicSourceTransportCommitSHA(manifest.SourceTreeSHA, baseline.CommitSHA, manifest.ObjectFormat)
	if err != nil || manifest.TransportCommitSHA != expected {
		return errors.New("transport commit identity is not deterministic")
	}
	return verifyObjectClosure(ctx, bareRoot, manifest.TransportCommitSHA)
}

// verifyBundlePrerequisites 验证 bundle 头部只包含 accepted baseline 前置条件和候选 ref。
func verifyBundlePrerequisites(bundlePath string, manifest SourceMaterializationManifest, baseline SourceBaseline) error {
	header, err := readSourceBundleHeader(bundlePath, baseline.ObjectFormat, manifest.ObjectFormat)
	if err != nil {
		return err
	}
	if len(header.prerequisites) != 1 || header.prerequisites[0] != baseline.CommitSHA {
		return errors.New("source bundle prerequisite does not match accepted image baseline commit")
	}
	if len(header.refs) != 1 || header.refs[0] != strings.TrimSuffix(expectedBundleRefs(manifest), "\n") {
		return errors.New("source bundle must advertise exactly one candidate ref")
	}
	return nil
}

type sourceBundleHeader struct {
	prerequisites []string
	refs          []string
}

// readSourceBundleHeader 读取并解析受限长度的 bundle 头部，不信任 Git 的人类可读输出。
func readSourceBundleHeader(bundlePath string, prerequisiteFormat gate.GitObjectFormat, refFormat gate.GitObjectFormat) (sourceBundleHeader, error) {
	file, err := os.Open(bundlePath)
	if err != nil {
		return sourceBundleHeader{}, fmt.Errorf("open source bundle header: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReader(io.LimitReader(file, maxSourceBundleHeaderLength))
	line, err := reader.ReadString('\n')
	if err != nil || strings.TrimSuffix(line, "\n") != "# v2 git bundle" {
		return sourceBundleHeader{}, errors.New("source bundle header version is not v2")
	}
	header := sourceBundleHeader{
		prerequisites: make([]string, 0, 1),
		refs:          make([]string, 0, 1),
	}
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return sourceBundleHeader{}, fmt.Errorf("read source bundle header: %w", readErr)
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "-") {
			prerequisite, parseErr := parseSourceBundlePrerequisite(line, prerequisiteFormat)
			if parseErr != nil {
				return sourceBundleHeader{}, parseErr
			}
			header.prerequisites = append(header.prerequisites, prerequisite)
			continue
		}
		ref, parseErr := parseSourceBundleRef(line, refFormat)
		if parseErr != nil {
			return sourceBundleHeader{}, parseErr
		}
		header.refs = append(header.refs, ref)
	}
	return header, nil
}

// parseSourceBundlePrerequisite 校验 bundle prerequisite 的对象 ID 格式。
func parseSourceBundlePrerequisite(line string, format gate.GitObjectFormat) (string, error) {
	fields := strings.Fields(strings.TrimPrefix(line, "-"))
	if len(fields) == 0 || !validOID(fields[0], format) {
		return "", errors.New("source bundle prerequisite has invalid Git object ID")
	}
	return fields[0], nil
}

// parseSourceBundleRef 校验 bundle 广告 ref 必须是唯一标准候选 ref。
func parseSourceBundleRef(line string, format gate.GitObjectFormat) (string, error) {
	fields := strings.Fields(line)
	if len(fields) != 2 || !validOID(fields[0], format) || fields[1] != sourceBundleRef {
		return "", errors.New("source bundle header advertises an unexpected ref")
	}
	return line, nil
}

type canonicalContext struct {
	ContextDigest string
	InputDigest   string
}

// canonicalContextDigests derives canonical compile-context identity while
// retaining no archive. Source transport is owned by source.bundle materialization.
func canonicalContextDigests(sourceEntries []sourceexport.TreeEntry) (canonicalContext, error) {
	contextDigest, inputDigest, err := writeCanonicalContext(sourceEntries, io.Discard)
	if err != nil {
		return canonicalContext{}, err
	}
	return canonicalContext{ContextDigest: contextDigest, InputDigest: inputDigest}, nil
}

func writeCanonicalContext(sourceEntries []sourceexport.TreeEntry, output io.Writer) (string, string, error) {
	if len(sourceEntries) == 0 {
		return "", "", errors.New("canonical context requires at least one source entry")
	}
	entries := append([]sourceexport.TreeEntry(nil), sourceEntries...)
	sort.Slice(entries, func(left int, right int) bool { return entries[left].Path < entries[right].Path })

	hash := sha256.New()
	writer := tar.NewWriter(io.MultiWriter(output, hash))
	var manifest []byte
	seenPaths := make(map[string]string, len(entries))
	for _, entry := range entries {
		if err := validateContextEntry(entry, seenPaths); err != nil {
			return "", "", err
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
			return "", "", fmt.Errorf("write canonical header %q: %w", entry.Path, err)
		}
		if _, err := writer.Write(entry.Data); err != nil {
			return "", "", fmt.Errorf("write canonical content %q: %w", entry.Path, err)
		}
		contentHash := sha256.Sum256(entry.Data)
		manifest = appendManifestField(manifest, entry.Path)
		manifest = appendManifestField(manifest, entry.Mode)
		manifest = appendManifestField(manifest, entry.Hash)
		manifest = appendManifestField(manifest, hex.EncodeToString(contentHash[:]))
	}
	if err := writer.Close(); err != nil {
		return "", "", fmt.Errorf("close canonical context: %w", err)
	}
	inputHash := sha256.Sum256(manifest)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), "sha256:" + hex.EncodeToString(inputHash[:]), nil
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

// LoadReadOnlyGitTree reads a verified SourceSpec tree without consulting a
// worktree. Remote ECI uses this as its only source-tree boundary.
func LoadReadOnlyGitTree(ctx context.Context, repoRoot string, spec gate.SourceSpec) (ReadOnlyGitTree, error) {
	if err := errors.Join(validateContext(ctx), spec.Validate(), validateCanonicalDirectory(repoRoot, false)); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("validate read-only Git tree input: %w", err)
	}
	if err := verifyRepositoryIdentity(ctx, repoRoot, spec.ObjectFormat); err != nil {
		return ReadOnlyGitTree{}, err
	}
	plan, err := inspectSourcePlan(ctx, repoRoot, spec)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	entries, err := loadReadOnlyTreeEntries(ctx, repoRoot, plan.tree)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	tree := ReadOnlyGitTree{Source: cloneSourceSpec(spec), Entries: entries}
	if err := verifyReadOnlyGitTree(tree); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("verify read-only Git tree: %w", err)
	}
	return tree, nil
}

func loadReadOnlyTreeEntries(ctx context.Context, repoRoot string, treeOID string) ([]sourceexport.TreeEntry, error) {
	output, err := runGitOutput(ctx, repoRoot, nil, "ls-tree", "-rz", "--full-tree", treeOID)
	if err != nil {
		return nil, fmt.Errorf("list read-only Git tree: %w", err)
	}
	records := bytes.Split(output, []byte{0})
	entries := make([]sourceexport.TreeEntry, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		entry, parseErr := parseReadOnlyTreeEntry(record)
		if parseErr != nil {
			return nil, parseErr
		}
		entries = append(entries, entry)
	}
	return loadReadOnlyTreeBlobs(ctx, repoRoot, entries)
}

func loadReadOnlyTreeBlobs(ctx context.Context, repoRoot string, entries []sourceexport.TreeEntry) ([]sourceexport.TreeEntry, error) {
	if len(entries) > maxReadOnlyGitTreeEntries {
		return nil, fmt.Errorf("read-only Git tree entry count %d exceeds limit %d", len(entries), maxReadOnlyGitTreeEntries)
	}
	if len(entries) == 0 {
		return entries, nil
	}
	unique := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if _, exists := seen[entry.Hash]; exists {
			continue
		}
		seen[entry.Hash] = struct{}{}
		unique = append(unique, entry.Hash)
	}
	output, err := runGitOutput(ctx, repoRoot, strings.NewReader(strings.Join(unique, "\n")+"\n"), "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("read 0/%d read-only Git tree blobs: %w", len(unique), err)
	}
	blobs, err := parseReadOnlyTreeBlobBatch(output, unique)
	if err != nil {
		return nil, err
	}
	for index := range entries {
		entries[index].Data = blobs[entries[index].Hash]
	}
	return entries, nil
}

func parseReadOnlyTreeBlobBatch(output []byte, expected []string) (map[string][]byte, error) {
	blobs := make(map[string][]byte, len(expected))
	offset, total := 0, 0
	for index, oid := range expected {
		object, consumed, err := parseSourceObjectPrefix(output[offset:], oid)
		if err != nil {
			return nil, fmt.Errorf("read %d/%d read-only Git tree blobs: %w", index, len(expected), err)
		}
		if object.kind != "blob" {
			return nil, fmt.Errorf("read-only Git tree object %s is %q, want blob", oid, object.kind)
		}
		total += len(object.data)
		if total > maxReadOnlyGitTreeBytes {
			return nil, fmt.Errorf("read-only Git tree bytes %d exceed limit %d", total, maxReadOnlyGitTreeBytes)
		}
		blobs[oid] = object.data
		offset += consumed
	}
	if offset != len(output) {
		return nil, errors.New("read-only Git tree cat-file returned trailing output")
	}
	return blobs, nil
}
