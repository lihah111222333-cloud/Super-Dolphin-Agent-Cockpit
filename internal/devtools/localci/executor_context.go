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
	"path"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

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
			ModTime: time.Unix(0, 0), Uid: 0, Gid: 0, Format: tar.FormatUSTAR,
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

// ResolveGateImageInputs 校验传入的 Git tree，并推导不依赖活动工作区的规范构建闭包。
func ResolveGateImageInputs(tree ReadOnlyGitTree, policyDigest string, platform string) (GateImageInputs, error) {
	if err := verifyReadOnlyGitTree(tree); err != nil {
		return GateImageInputs{}, err
	}
	request := CandidateRequest{
		SourceTreeSHA: tree.Source.SourceTreeSHA, PolicyDigest: policyDigest,
		ImageSchemaVersion: imageInputSchemaVersion, SourceEntries: cloneTreeEntries(tree.Entries), Platform: platform,
	}
	prepared, err := prepareCandidate(request)
	if err != nil {
		return GateImageInputs{}, fmt.Errorf("resolve gate image input closure: %w", err)
	}
	result := prepared.result
	return GateImageInputs{
		SubmittedSourceTree: result.SourceTreeSHA, PolicyDigest: policyDigest,
		ImageSchemaVersion: imageInputSchemaVersion, Platform: platform, SourceEntries: cloneTreeEntries(tree.Entries),
		ImageInputDigest: result.InputDigest, ContextDigest: result.ContextDigest,
		InputManifestDigest: result.InputManifestDigest, ToolchainDigest: result.ToolchainDigest,
		DockerfileDigest: result.DockerfileDigest,
	}, nil
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

func cloneTreeEntries(entries []sourceexport.TreeEntry) []sourceexport.TreeEntry {
	cloned := make([]sourceexport.TreeEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].Data = append([]byte(nil), entry.Data...)
		cloned[index].Path = path.Clean(entry.Path)
	}
	return cloned
}

// LoadReadOnlyGitTree 从已验证 SourceSpec 的 Git object tree 读取镜像输入，不读取工作区文件。
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

// LoadReadOnlyBootstrapTree 从已验证的 bare authority 读取首次自举镜像输入。
func LoadReadOnlyBootstrapTree(
	ctx context.Context,
	repository string,
	spec gate.SourceSpec,
) (ReadOnlyGitTree, error) {
	if err := errors.Join(validateContext(ctx), spec.Validate(), validateCanonicalDirectory(repository, false)); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("validate bootstrap Git tree input: %w", err)
	}
	if err := verifyBootstrapBareRepository(ctx, repository, spec.ObjectFormat); err != nil {
		return ReadOnlyGitTree{}, err
	}
	plan, err := inspectSourcePlan(ctx, repository, spec)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	entries, err := loadReadOnlyTreeEntries(ctx, repository, plan.tree)
	if err != nil {
		return ReadOnlyGitTree{}, err
	}
	tree := ReadOnlyGitTree{Source: cloneSourceSpec(spec), Entries: entries}
	if err := verifyReadOnlyGitTree(tree); err != nil {
		return ReadOnlyGitTree{}, fmt.Errorf("verify bootstrap Git tree: %w", err)
	}
	return tree, nil
}

// verifyBootstrapBareRepository 固定 bare Git 目录与 object format，拒绝工作树输入。
func verifyBootstrapBareRepository(
	ctx context.Context,
	repository string,
	objectFormat gate.GitObjectFormat,
) error {
	bareOutput, err := runGitOutput(ctx, repository, nil, "rev-parse", "--is-bare-repository")
	if err != nil {
		return fmt.Errorf("inspect bootstrap bare repository: %w", err)
	}
	bare, err := strictGitLine(bareOutput)
	if err != nil || bare != "true" {
		return errors.Join(errors.New("bootstrap repository must be bare"), err)
	}
	gitDirOutput, err := runGitOutput(ctx, repository, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("resolve bootstrap Git directory: %w", err)
	}
	gitDir, err := strictGitLine(gitDirOutput)
	if err != nil || gitDir != repository {
		return errors.Join(errors.New("bootstrap repository Git directory drifted"), err)
	}
	return verifyBootstrapObjectFormat(ctx, repository, objectFormat)
}

func verifyBootstrapObjectFormat(
	ctx context.Context,
	repository string,
	objectFormat gate.GitObjectFormat,
) error {
	output, err := runGitOutput(ctx, repository, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return fmt.Errorf("read bootstrap repository object format: %w", err)
	}
	actual, err := strictGitLine(output)
	if err != nil {
		return fmt.Errorf("parse bootstrap repository object format: %w", err)
	}
	if actual != string(objectFormat) {
		return fmt.Errorf("bootstrap repository object format is %q, want %q", actual, objectFormat)
	}
	return nil
}

// loadReadOnlyTreeEntries 读取稳定排序的 blob 记录并从 Git object database 取得内容。
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
		object, readErr := readSourceObject(ctx, repoRoot, entry.Hash)
		if readErr != nil {
			return nil, readErr
		}
		if object.kind != "blob" {
			return nil, fmt.Errorf("read-only Git tree entry %q is %q, want blob", entry.Path, object.kind)
		}
		entry.Data = append([]byte(nil), object.data...)
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseReadOnlyTreeEntry(record []byte) (sourceexport.TreeEntry, error) {
	metadata, path, found := bytes.Cut(record, []byte{'\t'})
	if !found || len(path) == 0 {
		return sourceexport.TreeEntry{}, errors.New("read-only Git tree record is missing its path")
	}
	fields := bytes.Fields(metadata)
	if len(fields) != 3 || string(fields[1]) != "blob" {
		return sourceexport.TreeEntry{}, fmt.Errorf("read-only Git tree entry %q is not a blob", path)
	}
	return sourceexport.TreeEntry{
		Path: string(path), Mode: string(fields[0]), Hash: string(fields[2]),
	}, nil
}
