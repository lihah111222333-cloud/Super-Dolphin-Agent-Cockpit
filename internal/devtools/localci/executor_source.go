package localci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	sourceBundleName        = "source.bundle"
	sourceManifestName      = "source-manifest.json"
	sourceBundleRef         = "refs/source/materialized"
	sourceBundleBaseRef     = "refs/source/base"
	sourceManifestVersion   = 1
	privateSourceFileMode   = 0o400
	privateSourceDirMode    = 0o700
	maxSourceManifestLength = 1 << 20
)

// SourceMaterializationManifest 记录 bundle 的 Git object truth 与完整性摘要。
type SourceMaterializationManifest struct {
	SchemaVersion         uint32               `json:"schema_version"`
	Source                gate.SourceSpec      `json:"source"`
	SourceTreeSHA         string               `json:"source_tree_sha"`
	MaterializedCommitSHA string               `json:"materialized_commit_sha"`
	SyntheticCommitSHA    string               `json:"synthetic_commit_sha,omitempty"`
	TrustedBaseCommitSHA  string               `json:"trusted_base_commit_sha,omitempty"`
	BundleDigest          string               `json:"bundle_digest"`
	ObjectFormat          gate.GitObjectFormat `json:"object_format"`
}

// SourceMaterialization 返回只读 bundle、manifest 及已复核的内存 manifest。
type SourceMaterialization struct {
	BundlePath   string
	ManifestPath string
	Manifest     SourceMaterializationManifest
}

type sourceObject struct {
	oid  string
	kind string
	data []byte
}

type sourcePlan struct {
	roots              []string
	tree               string
	commit             string
	baseCommit         string
	syntheticParentSHA string
}

// sourceBundleImporter is implemented by the sourceexport owner. localci must not parse Git trees or bundles.
// 依赖 sourceexport owner：需公开 commit 与 dangling synthetic-commit bundle 的 ImportAndVerify。
type sourceBundleImporter interface {
	ImportAndVerify(ctx context.Context, bundlePath string, expectedObject string, expectedTree string) (verifiedRepository string, err error)
}

// importSource 只编排 sourceexport owner 的 bundle 导入与对象复验。
func importSource(ctx context.Context, importer sourceBundleImporter, bundlePath string, expectedObject string, expectedTree string) (string, error) {
	if importer == nil || !filepath.IsAbs(bundlePath) || expectedObject == "" || expectedTree == "" {
		return "", errors.New("source importer and absolute bundle/object/tree inputs are required")
	}
	repository, err := importer.ImportAndVerify(ctx, bundlePath, expectedObject, expectedTree)
	if err != nil {
		return "", fmt.Errorf("import and verify source bundle: %w", err)
	}
	if !filepath.IsAbs(repository) {
		return "", errors.New("source importer returned a non-absolute verified repository")
	}
	return repository, nil
}

// MaterializeSource 将已验证 SourceSpec 物化为自包含只读 Git bundle。
func MaterializeSource(ctx context.Context, repoRoot string, spec gate.SourceSpec, outputRoot string) (result SourceMaterialization, err error) {
	if err := validateMaterializationInput(ctx, repoRoot, spec, outputRoot); err != nil {
		return SourceMaterialization{}, err
	}
	spec = cloneSourceSpec(spec)
	if err := verifyRepositoryIdentity(ctx, repoRoot, spec.ObjectFormat); err != nil {
		return SourceMaterialization{}, err
	}
	plan, err := inspectSourcePlan(ctx, repoRoot, spec)
	if err != nil {
		return SourceMaterialization{}, err
	}
	stageRoot, err := os.MkdirTemp(outputRoot, ".source-materializer-")
	if err != nil {
		return SourceMaterialization{}, fmt.Errorf("create source materializer staging root: %w", err)
	}
	defer func() { err = errors.Join(err, removeSourceTemp(stageRoot)) }()
	if err := os.Chmod(stageRoot, privateSourceDirMode); err != nil {
		return SourceMaterialization{}, fmt.Errorf("protect source materializer staging root: %w", err)
	}
	result, err = materializeInStage(ctx, repoRoot, outputRoot, stageRoot, spec, plan)
	return result, err
}

// materializeInStage 在私有 staging root 内完成对象闭包、bundle 与 manifest 组装。
func materializeInStage(ctx context.Context, repoRoot string, outputRoot string, stageRoot string, spec gate.SourceSpec, plan sourcePlan) (SourceMaterialization, error) {
	bareRoot := filepath.Join(stageRoot, "objects.git")
	if err := initBareRepository(ctx, stageRoot, bareRoot, spec.ObjectFormat); err != nil {
		return SourceMaterialization{}, err
	}
	if err := transferObjectClosure(ctx, repoRoot, bareRoot, stageRoot, plan.roots, spec.ObjectFormat); err != nil {
		return SourceMaterialization{}, err
	}
	if spec.Kind == gate.SourceKindTree {
		commit, err := createSyntheticCommit(ctx, bareRoot, plan.tree, plan.syntheticParentSHA, spec.ObjectFormat)
		if err != nil {
			return SourceMaterialization{}, err
		}
		plan.commit = commit
	}
	if err := prepareBundleRefs(ctx, bareRoot, plan.commit, plan.baseCommit, plan.tree, plan.syntheticParentSHA); err != nil {
		return SourceMaterialization{}, err
	}
	bundlePath := filepath.Join(stageRoot, sourceBundleName)
	if err := createSourceBundle(ctx, bareRoot, bundlePath, plan.baseCommit != ""); err != nil {
		return SourceMaterialization{}, err
	}
	manifest, err := buildSourceManifest(bundlePath, spec, plan)
	if err != nil {
		return SourceMaterialization{}, err
	}
	if err := importAndVerifyBundle(ctx, bundlePath, stageRoot, manifest); err != nil {
		return SourceMaterialization{}, err
	}
	manifestPath := filepath.Join(stageRoot, sourceManifestName)
	if err := writeSourceManifest(manifestPath, manifest); err != nil {
		return SourceMaterialization{}, err
	}
	return publishSourceArtifacts(outputRoot, bundlePath, manifestPath, manifest)
}

// ImportAndVerifySourceBundle 在临时 bare repo 中导入 bundle 并复核 commit/tree。
func ImportAndVerifySourceBundle(ctx context.Context, outputRoot string) (SourceMaterializationManifest, error) {
	if err := validateContext(ctx); err != nil {
		return SourceMaterializationManifest{}, err
	}
	if err := validateCanonicalDirectory(outputRoot, true); err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("validate source output root: %w", err)
	}
	bundlePath := filepath.Join(outputRoot, sourceBundleName)
	manifestPath := filepath.Join(outputRoot, sourceManifestName)
	if err := validatePublishedArtifacts(outputRoot, bundlePath, manifestPath); err != nil {
		return SourceMaterializationManifest{}, err
	}
	manifest, err := readSourceManifest(manifestPath)
	if err != nil {
		return SourceMaterializationManifest{}, err
	}
	digest, err := digestSourceFile(bundlePath)
	if err != nil {
		return SourceMaterializationManifest{}, err
	}
	if digest != manifest.BundleDigest {
		return SourceMaterializationManifest{}, errors.New("source bundle digest does not match manifest")
	}
	if err := importAndVerifyBundle(ctx, bundlePath, outputRoot, manifest); err != nil {
		return SourceMaterializationManifest{}, err
	}
	return manifest, nil
}

// validateMaterializationInput 在任何 Git 或文件写入前拒绝不可信入口。
func validateMaterializationInput(ctx context.Context, repoRoot string, spec gate.SourceSpec, outputRoot string) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := errors.Join(spec.Validate(), validateCanonicalDirectory(repoRoot, false), validateCanonicalDirectory(outputRoot, true)); err != nil {
		return fmt.Errorf("validate source materialization input: %w", err)
	}
	entries, err := os.ReadDir(outputRoot)
	if err != nil {
		return fmt.Errorf("read source output root: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("source output root must be empty")
	}
	return nil
}

func validateContext(ctx context.Context) error {
	if ctx == nil {
		return errors.New("source materialization context is required")
	}
	return ctx.Err()
}

func cloneSourceSpec(spec gate.SourceSpec) gate.SourceSpec {
	spec.Commit = cloneSourceValue(spec.Commit)
	spec.Tree = cloneSourceValue(spec.Tree)
	spec.Range = cloneSourceValue(spec.Range)
	return spec
}

func cloneSourceValue[T any](source *T) *T {
	if source == nil {
		return nil
	}
	clone := *source
	return &clone
}

// verifyRepositoryIdentity 确认输入是 canonical Git 顶层且对象格式与 SourceSpec 相等。
func verifyRepositoryIdentity(ctx context.Context, repoRoot string, objectFormat gate.GitObjectFormat) error {
	topLevel, err := runGitOutput(ctx, repoRoot, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("resolve repository top level: %w", err)
	}
	resolvedRoot, err := strictGitLine(topLevel)
	if err != nil {
		return fmt.Errorf("parse repository top level: %w", err)
	}
	if resolvedRoot != repoRoot {
		return fmt.Errorf("repository root %q is not Git top level %q", repoRoot, resolvedRoot)
	}
	formatOutput, err := runGitOutput(ctx, repoRoot, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return fmt.Errorf("read repository object format: %w", err)
	}
	actualFormat, err := strictGitLine(formatOutput)
	if err != nil {
		return fmt.Errorf("parse repository object format: %w", err)
	}
	if actualFormat != string(objectFormat) {
		return fmt.Errorf("repository object format is %q, want %q", actualFormat, objectFormat)
	}
	return nil
}

func inspectSourcePlan(ctx context.Context, repoRoot string, spec gate.SourceSpec) (sourcePlan, error) {
	switch spec.Kind {
	case gate.SourceKindCommit:
		return inspectCommitPlan(ctx, repoRoot, spec.Commit.SHA, spec.SourceTreeSHA)
	case gate.SourceKindTree:
		return inspectTreePlan(ctx, repoRoot, spec.Tree, spec.SourceTreeSHA)
	case gate.SourceKindRange:
		return inspectRangePlan(ctx, repoRoot, spec.Range, spec.SourceTreeSHA)
	default:
		return sourcePlan{}, fmt.Errorf("unsupported source kind %q", spec.Kind)
	}
}

func inspectCommitPlan(ctx context.Context, repoRoot string, commitSHA string, expectedTree string) (sourcePlan, error) {
	commit, err := readSourceObject(ctx, repoRoot, commitSHA)
	if err != nil {
		return sourcePlan{}, err
	}
	tree, parents, err := parseCommitObject(commit, expectedTree)
	if err != nil {
		return sourcePlan{}, err
	}
	plan := sourcePlan{roots: []string{commitSHA}, tree: tree, commit: commitSHA}
	if len(parents) == 1 {
		plan.baseCommit = parents[0]
	}
	return plan, nil
}

// inspectTreePlan 复核显式 tree 与可选 parent commit，不读取 index 或 HEAD。
func inspectTreePlan(ctx context.Context, repoRoot string, source *gate.TreeSource, expectedTree string) (sourcePlan, error) {
	tree, err := readSourceObject(ctx, repoRoot, source.SHA)
	if err != nil {
		return sourcePlan{}, err
	}
	if tree.kind != "tree" {
		return sourcePlan{}, fmt.Errorf("source object %s has type %s, want tree", source.SHA, tree.kind)
	}
	if source.SHA != expectedTree {
		return sourcePlan{}, fmt.Errorf("source tree is %s, want %s", source.SHA, expectedTree)
	}
	roots := []string{source.SHA}
	if source.ParentCommitSHA != "" {
		parent, err := readSourceObject(ctx, repoRoot, source.ParentCommitSHA)
		if err != nil {
			return sourcePlan{}, err
		}
		if parent.kind != "commit" {
			return sourcePlan{}, fmt.Errorf("tree parent object %s has type %s, want commit", source.ParentCommitSHA, parent.kind)
		}
		roots = append(roots, source.ParentCommitSHA)
	}
	return sourcePlan{
		roots:              roots,
		tree:               source.SHA,
		baseCommit:         source.ParentCommitSHA,
		syntheticParentSHA: source.ParentCommitSHA,
	}, nil
}

// inspectRangePlan 复核 range 的 head/base 对象类型与 head tree。
func inspectRangePlan(ctx context.Context, repoRoot string, source *gate.RangeSource, expectedTree string) (sourcePlan, error) {
	head, err := readSourceObject(ctx, repoRoot, source.HeadSHA)
	if err != nil {
		return sourcePlan{}, err
	}
	tree, _, err := parseCommitObject(head, expectedTree)
	if err != nil {
		return sourcePlan{}, fmt.Errorf("validate range head: %w", err)
	}
	roots := []string{source.HeadSHA}
	baseCommit := ""
	if source.BaseKind == gate.BaseKindCommit {
		base, err := readSourceObject(ctx, repoRoot, source.BaseSHA)
		if err != nil {
			return sourcePlan{}, fmt.Errorf("read range base: %w", err)
		}
		if base.kind != "commit" {
			return sourcePlan{}, fmt.Errorf("range base object %s has type %s, want commit", source.BaseSHA, base.kind)
		}
		baseCommit = source.BaseSHA
		roots = append(roots, baseCommit)
	}
	return sourcePlan{roots: roots, tree: tree, commit: source.HeadSHA, baseCommit: baseCommit}, nil
}

// readSourceObject 通过 cat-file batch 读取单个显式 OID 并严格拒绝尾随输出。
func readSourceObject(ctx context.Context, repoRoot string, oid string) (sourceObject, error) {
	output, err := runGitOutput(ctx, repoRoot, strings.NewReader(oid+"\n"), "cat-file", "--batch")
	if err != nil {
		return sourceObject{}, fmt.Errorf("read Git object %s: %w", oid, err)
	}
	return parseSourceObjectOutput(output, oid)
}

// parseSourceObjectOutput 严格分离 cat-file header、payload 与单个终止换行。
func parseSourceObjectOutput(output []byte, oid string) (sourceObject, error) {
	object, consumed, err := parseSourceObjectPrefix(output, oid)
	if err != nil {
		return sourceObject{}, err
	}
	if consumed != len(output) {
		return sourceObject{}, errors.New("git cat-file returned trailing output")
	}
	return object, nil
}

// parseSourceObjectPrefix 从 batch 输出前缀严格读取一个对象，并返回消费字节数。
func parseSourceObjectPrefix(output []byte, oid string) (sourceObject, int, error) {
	headerEnd := bytes.IndexByte(output, '\n')
	if headerEnd < 0 {
		return sourceObject{}, 0, errors.New("git cat-file output is missing header terminator")
	}
	kind, size, err := parseSourceObjectHeader(output[:headerEnd], oid)
	if err != nil {
		return sourceObject{}, 0, err
	}
	dataStart := headerEnd + 1
	dataEnd := dataStart + size
	if dataEnd < dataStart || dataEnd >= len(output) || output[dataEnd] != '\n' {
		return sourceObject{}, 0, errors.New("git cat-file returned truncated output")
	}
	return sourceObject{oid: oid, kind: kind, data: bytes.Clone(output[dataStart:dataEnd])}, dataEnd + 1, nil
}

// parseSourceObjectHeader 拒绝 missing、OID 漂移和不可表示的对象大小。
func parseSourceObjectHeader(header []byte, oid string) (string, int, error) {
	fields := strings.Fields(string(header))
	if len(fields) == 2 && fields[1] == "missing" {
		return "", 0, fmt.Errorf("Git object %s does not exist", oid)
	}
	if len(fields) != 3 {
		return "", 0, errors.New("git cat-file returned an unexpected object header")
	}
	if fields[0] != oid {
		return "", 0, errors.New("git cat-file returned an unexpected object ID")
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 || size > int64(^uint(0)>>1) {
		return "", 0, errors.New("git cat-file returned an invalid object size")
	}
	return fields[1], int(size), nil
}

// parseCommitObject 从原始 commit 对象复核 tree 并提取固定 parent 列表。
func parseCommitObject(object sourceObject, expectedTree string) (string, []string, error) {
	if object.kind != "commit" {
		return "", nil, fmt.Errorf("source object %s has type %s, want commit", object.oid, object.kind)
	}
	header, _, found := bytes.Cut(object.data, []byte("\n\n"))
	if !found {
		return "", nil, fmt.Errorf("commit object %s is missing header terminator", object.oid)
	}
	lines := bytes.Split(header, []byte{'\n'})
	if len(lines) == 0 || !bytes.HasPrefix(lines[0], []byte("tree ")) {
		return "", nil, fmt.Errorf("commit object %s is missing leading tree", object.oid)
	}
	tree := string(bytes.TrimPrefix(lines[0], []byte("tree ")))
	if tree != expectedTree {
		return "", nil, fmt.Errorf("source tree is %s, want %s", tree, expectedTree)
	}
	parents := make([]string, 0, 2)
	for _, line := range lines[1:] {
		if !bytes.HasPrefix(line, []byte("parent ")) {
			break
		}
		parents = append(parents, string(bytes.TrimPrefix(line, []byte("parent "))))
	}
	return tree, parents, nil
}

func initBareRepository(ctx context.Context, commandRoot string, bareRoot string, format gate.GitObjectFormat) error {
	output, err := runGitOutput(ctx, commandRoot, nil, "init", "-q", "--bare", "--object-format="+string(format), "--", bareRoot)
	return rejectGitOutput(output, err, "initialize temporary bare repository")
}

// transferObjectClosure 使用 pack-objects 将显式根的完整闭包送入临时 bare repo。
func transferObjectClosure(ctx context.Context, repoRoot string, bareRoot string, stageRoot string, roots []string, format gate.GitObjectFormat) error {
	packPath := filepath.Join(stageRoot, "objects.pack")
	pack, err := os.OpenFile(packPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source object pack: %w", err)
	}
	rootInput := strings.Join(roots, "\n") + "\n"
	runErr := runGit(ctx, repoRoot, strings.NewReader(rootInput), pack, "pack-objects", "--stdout", "--revs")
	closeErr := pack.Close()
	if runErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("pack source object closure: %w", runErr), closeErr)
	}
	pack, err = os.Open(packPath)
	if err != nil {
		return fmt.Errorf("open source object pack: %w", err)
	}
	output, indexErr := runGitOutput(ctx, bareRoot, pack, "index-pack", "--stdin")
	closeErr = pack.Close()
	if indexErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("index source object pack: %w", indexErr), closeErr)
	}
	line, err := strictGitLine(output)
	if err != nil || !strings.HasPrefix(line, "pack\t") || !validOID(strings.TrimPrefix(line, "pack\t"), format) {
		return errors.New("git index-pack returned unexpected output")
	}
	return nil
}

func createSyntheticCommit(ctx context.Context, bareRoot string, tree string, parent string, format gate.GitObjectFormat) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	output, err := runGitOutput(ctx, bareRoot, strings.NewReader("super-dolphin source materialization\n"), args...)
	if err != nil {
		return "", fmt.Errorf("create synthetic source commit: %w", err)
	}
	commit, err := strictGitLine(output)
	if err != nil || !validOID(commit, format) {
		return "", errors.New("git commit-tree returned an invalid synthetic commit")
	}
	return commit, nil
}

// prepareBundleRefs 只在临时 bare repo 创建固定 refs 并复核全部 commit 闭包。
func prepareBundleRefs(ctx context.Context, bareRoot string, commit string, base string, tree string, parent string) error {
	input := fmt.Sprintf("create %s %s\n", sourceBundleRef, commit)
	commits := []string{commit}
	if base != "" {
		input += fmt.Sprintf("create %s %s\n", sourceBundleBaseRef, base)
		commits = append(commits, base)
	}
	output, err := runGitOutput(ctx, bareRoot, strings.NewReader(input), "update-ref", "--stdin")
	if err := rejectGitOutput(output, err, "create temporary bundle refs"); err != nil {
		return err
	}
	object, err := readSourceObject(ctx, bareRoot, commit)
	if err != nil {
		return err
	}
	actualTree, parents, err := parseCommitObject(object, tree)
	if err != nil || actualTree != tree {
		return errors.Join(errors.New("materialized commit tree verification failed"), err)
	}
	if parent != "" && (len(parents) != 1 || parents[0] != parent) {
		return errors.New("synthetic commit parent verification failed")
	}
	return verifyObjectClosure(ctx, bareRoot, commits...)
}

func buildSourceManifest(bundlePath string, spec gate.SourceSpec, plan sourcePlan) (SourceMaterializationManifest, error) {
	digest, err := digestSourceFile(bundlePath)
	if err != nil {
		return SourceMaterializationManifest{}, err
	}
	manifest := SourceMaterializationManifest{
		SchemaVersion:         sourceManifestVersion,
		Source:                spec,
		SourceTreeSHA:         plan.tree,
		MaterializedCommitSHA: plan.commit,
		TrustedBaseCommitSHA:  plan.baseCommit,
		BundleDigest:          digest,
		ObjectFormat:          spec.ObjectFormat,
	}
	if spec.Kind == gate.SourceKindTree {
		manifest.SyntheticCommitSHA = plan.commit
	}
	if err := manifest.Validate(); err != nil {
		return SourceMaterializationManifest{}, err
	}
	return manifest, nil
}

// Validate 校验 source manifest 与原 SourceSpec 的逐字段身份关系。
func (manifest SourceMaterializationManifest) Validate() error {
	if err := manifest.Source.Validate(); err != nil {
		return fmt.Errorf("validate manifest source: %w", err)
	}
	if manifest.SchemaVersion != sourceManifestVersion || manifest.ObjectFormat != manifest.Source.ObjectFormat ||
		manifest.SourceTreeSHA != manifest.Source.SourceTreeSHA || !validOID(manifest.MaterializedCommitSHA, manifest.ObjectFormat) ||
		!validDigest(manifest.BundleDigest) {
		return errors.New("source manifest identity or digest is invalid")
	}
	if manifest.TrustedBaseCommitSHA != "" && !validOID(manifest.TrustedBaseCommitSHA, manifest.ObjectFormat) {
		return errors.New("source manifest trusted base commit is invalid")
	}
	return validateManifestCommitIdentity(manifest)
}

// validateManifestCommitIdentity 约束真实 commit/head 与 synthetic commit 的互斥身份。
func validateManifestCommitIdentity(manifest SourceMaterializationManifest) error {
	var expected string
	switch manifest.Source.Kind {
	case gate.SourceKindRange:
		expected = manifest.Source.Range.HeadSHA
	case gate.SourceKindCommit:
		expected = manifest.Source.Commit.SHA
	case gate.SourceKindTree:
		expected = manifest.SyntheticCommitSHA
	default:
		return fmt.Errorf("unsupported manifest source kind %q", manifest.Source.Kind)
	}
	if expected == "" || manifest.MaterializedCommitSHA != expected {
		return errors.New("manifest materialized commit does not match source identity")
	}
	if manifest.Source.Kind != gate.SourceKindTree && manifest.SyntheticCommitSHA != "" {
		return errors.New("non-tree manifest must not contain synthetic commit")
	}
	return validateManifestTrustedBase(manifest)
}

// validateManifestTrustedBase 约束显式 base 只来自 SourceSpec 或 commit 的真实单 parent。
func validateManifestTrustedBase(manifest SourceMaterializationManifest) error {
	switch manifest.Source.Kind {
	case gate.SourceKindRange:
		if manifest.Source.Range.BaseKind == gate.BaseKindCommit &&
			manifest.TrustedBaseCommitSHA != manifest.Source.Range.BaseSHA {
			return errors.New("range manifest trusted base does not match SourceSpec")
		}
		if manifest.Source.Range.BaseKind != gate.BaseKindCommit && manifest.TrustedBaseCommitSHA != "" {
			return errors.New("empty-tree range manifest must not contain a trusted base")
		}
	case gate.SourceKindTree:
		if manifest.TrustedBaseCommitSHA != manifest.Source.Tree.ParentCommitSHA {
			return errors.New("tree manifest trusted base does not match SourceSpec")
		}
	}
	return nil
}

// writeSourceManifest 以独占创建和只读权限发布严格 JSON manifest。
func writeSourceManifest(path string, manifest SourceMaterializationManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode source manifest: %w", err)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source manifest: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return closeSourceFile(file, err)
	}
	if err := file.Sync(); err != nil {
		return closeSourceFile(file, err)
	}
	if err := file.Chmod(privateSourceFileMode); err != nil {
		return closeSourceFile(file, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close source manifest: %w", err)
	}
	return nil
}

func readSourceManifest(path string) (SourceMaterializationManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("open source manifest: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSourceManifestLength+1))
	if err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("read source manifest: %w", err)
	}
	if len(data) > maxSourceManifestLength {
		return SourceMaterializationManifest{}, errors.New("source manifest is too large")
	}
	var manifest SourceMaterializationManifest
	if err := gate.DecodeStrictJSON(data, &manifest); err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("decode source manifest: %w", err)
	}
	return manifest, nil
}

// publishSourceArtifacts 原子移动两个只读文件，并在失败时清理局部发布。
func publishSourceArtifacts(outputRoot string, stageBundle string, stageManifest string, manifest SourceMaterializationManifest) (result SourceMaterialization, err error) {
	bundlePath := filepath.Join(outputRoot, sourceBundleName)
	manifestPath := filepath.Join(outputRoot, sourceManifestName)
	defer func() {
		if err != nil {
			err = errors.Join(err, removeSourceFile(bundlePath), removeSourceFile(manifestPath))
		}
	}()
	if err := os.Rename(stageBundle, bundlePath); err != nil {
		return SourceMaterialization{}, fmt.Errorf("publish source bundle: %w", err)
	}
	if err := os.Rename(stageManifest, manifestPath); err != nil {
		return SourceMaterialization{}, fmt.Errorf("publish source manifest: %w", err)
	}
	if err := removeSourceTemp(filepath.Dir(stageBundle)); err != nil {
		return SourceMaterialization{}, err
	}
	if err := validatePublishedArtifacts(outputRoot, bundlePath, manifestPath); err != nil {
		return SourceMaterialization{}, err
	}
	return SourceMaterialization{BundlePath: bundlePath, ManifestPath: manifestPath, Manifest: manifest}, nil
}

// validateCanonicalDirectory 拒绝非绝对、非 canonical、链接或公开输出目录。
func validateCanonicalDirectory(path string, private bool) error {
	if !validCanonicalPath(path) {
		return errors.New("path must be canonical, absolute, and free of control characters")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("canonicalize path: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat path: %w", err)
	}
	if resolved != path || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path must be a real non-symlink directory")
	}
	if private && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("private output root mode %04o exposes group or world access", info.Mode().Perm())
	}
	return nil
}

func validCanonicalPath(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path && !containsControl(path)
}

// validatePublishedArtifacts 要求输出根只含两个 0400 regular artifacts。
func validatePublishedArtifacts(outputRoot string, paths ...string) error {
	entries, err := os.ReadDir(outputRoot)
	if err != nil {
		return fmt.Errorf("read published source artifacts: %w", err)
	}
	if len(entries) != len(paths) {
		return errors.New("source output root contains missing or trailing artifacts")
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("lstat source artifact: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != privateSourceFileMode {
			return fmt.Errorf("source artifact %s must be a 0400 regular non-symlink file", filepath.Base(path))
		}
	}
	return nil
}

func digestSourceFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source bundle for digest: %w", err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.Join(copyErr, closeErr)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validDigest(value string) bool {
	encoded, found := strings.CutPrefix(value, "sha256:")
	_, err := hex.DecodeString(encoded)
	return found && len(encoded) == sha256.Size*2 && encoded == strings.ToLower(encoded) && err == nil
}

// validOID 校验对象格式、长度、小写十六进制并拒绝全零 OID。
func validOID(value string, format gate.GitObjectFormat) bool {
	want := 0
	switch format {
	case gate.GitObjectFormatSHA1:
		want = 40
	case gate.GitObjectFormatSHA256:
		want = 64
	}
	_, err := hex.DecodeString(value)
	return want != 0 && len(value) == want && value == strings.ToLower(value) && strings.Trim(value, "0") != "" && err == nil
}

func strictGitLine(output []byte) (string, error) {
	if len(output) < 2 || output[len(output)-1] != '\n' || bytes.Count(output, []byte{'\n'}) != 1 {
		return "", errors.New("Git output must contain exactly one terminated line")
	}
	return string(output[:len(output)-1]), nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, func(character rune) bool { return character < 0x20 || character == 0x7f }) >= 0
}

func runGitOutput(ctx context.Context, repoRoot string, stdin io.Reader, args ...string) ([]byte, error) {
	var stdout bytes.Buffer
	err := runGit(ctx, repoRoot, stdin, &stdout, args...)
	return stdout.Bytes(), err
}

// runGit 以固定环境执行 Git plumbing，并保留 context 与 stderr 根因。
func runGit(ctx context.Context, repoRoot string, stdin io.Reader, stdout io.Writer, args ...string) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if interfaceValueIsNil(stdout) {
		return errors.New("Git plumbing stdout is required")
	}
	if stdin != nil && interfaceValueIsNil(stdin) {
		return errors.New("Git plumbing stdin must not be typed nil")
	}
	if len(args) == 0 {
		return errors.New("Git plumbing command is required")
	}
	commandArgs := append([]string{"--no-replace-objects", "-C", repoRoot}, args...)
	command := exec.CommandContext(ctx, "git", commandArgs...)
	command.Stdin = stdin
	command.Stdout = stdout
	var stderr bytes.Buffer
	command.Stderr = &stderr
	command.Env = sourceGitEnvironment()
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("git %s: %w", args[0], ctxErr)
		}
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// sourceGitEnvironment 仅继承进程定位变量，清除全部 Git repository-local 重定向环境。
func sourceGitEnvironment() []string {
	environment := make([]string, 0, 16)
	for _, key := range []string{"HOME", "PATH", "TMPDIR", "SystemRoot"} {
		if value, present := os.LookupEnv(key); present {
			environment = append(environment, key+"="+value)
		}
	}
	return append(environment,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_AUTHOR_NAME=Super Dolphin Source Materializer",
		"GIT_AUTHOR_EMAIL=source-materializer.invalid",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=Super Dolphin Source Materializer",
		"GIT_COMMITTER_EMAIL=source-materializer.invalid",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
		"LC_ALL=C",
	)
}

func closeSourceFile(file *os.File, cause error) error {
	return errors.Join(cause, file.Close())
}

func removeSourceTemp(path string) error {
	if path == "" {
		return nil
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove source temporary directory: %w", err)
	}
	return nil
}

func removeSourceFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove partial source artifact: %w", err)
	}
	return nil
}
