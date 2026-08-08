package remoteci

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

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
	// commit/baseCommit preserve the original SourceSpec identity for the
	// manifest; they are never used as transport roots or bundle refs.
	commit     string
	baseCommit string
	baseTree   string
	tree       string
	// syntheticBaseCommit 表示相对于 accepted baseline 的 SourceSpec parent/base tree；
	// transportCommit 随后将此 synthetic commit 作为唯一 parent。
	syntheticBaseCommit string
	transportCommit     string
}

// sourceBundleImporter is implemented by the sourceexport owner; materializers must not duplicate Git tree or bundle parsing.
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

// MaterializeSource 将已验证 SourceSpec 物化为相对 accepted image baseline
// 的 Git prerequisite/thin bundle。baseline 是强制参数；不存在自包含或
// 全量 fallback。
func MaterializeSource(ctx context.Context, repoRoot string, spec gate.SourceSpec, outputRoot string, baseline SourceBaseline) (result SourceMaterialization, err error) {
	if err := validateMaterializationInput(ctx, repoRoot, spec, outputRoot); err != nil {
		return SourceMaterialization{}, err
	}
	spec = cloneSourceSpec(spec)
	if err := verifyRepositoryIdentity(ctx, repoRoot, spec.ObjectFormat); err != nil {
		return SourceMaterialization{}, err
	}
	if err := validateSourceBaseline(ctx, baseline, spec.ObjectFormat); err != nil {
		return SourceMaterialization{}, err
	}
	plan, err := inspectSourcePlan(ctx, repoRoot, spec, &baseline)
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
	result, err = materializeInStage(ctx, repoRoot, outputRoot, stageRoot, spec, plan, baseline)
	return result, err
}

// materializeInStage 在私有 staging root 内完成对象闭包、bundle 与 manifest 组装。
func materializeInStage(ctx context.Context, repoRoot string, outputRoot string, stageRoot string, spec gate.SourceSpec, plan sourcePlan, baseline SourceBaseline) (SourceMaterialization, error) {
	bareRoot := filepath.Join(stageRoot, "objects.git")
	if err := initBareRepository(ctx, stageRoot, bareRoot, spec.ObjectFormat); err != nil {
		return SourceMaterialization{}, err
	}
	if err := configureObjectStoreAlternate(ctx, bareRoot, baseline); err != nil {
		return SourceMaterialization{}, err
	}
	if err := ensureSourceEmptyTree(ctx, bareRoot, plan.baseTree, spec.ObjectFormat); err != nil {
		return SourceMaterialization{}, err
	}
	var err error
	plan, err = prepareSourceTransport(ctx, repoRoot, bareRoot, stageRoot, spec, plan, baseline)
	if err != nil {
		return SourceMaterialization{}, err
	}
	bundlePath := filepath.Join(stageRoot, sourceBundleName)
	if err := createSourceBundle(ctx, bareRoot, bundlePath, baseline, plan.transportCommit); err != nil {
		return SourceMaterialization{}, err
	}
	manifest, err := buildSourceManifest(bundlePath, spec, plan, baseline)
	if err != nil {
		return SourceMaterialization{}, err
	}
	if err := importAndVerifyBundle(ctx, bundlePath, stageRoot, manifest, baseline); err != nil {
		return SourceMaterialization{}, err
	}
	manifestPath := filepath.Join(stageRoot, sourceManifestName)
	if err := writeSourceManifest(manifestPath, manifest); err != nil {
		return SourceMaterialization{}, err
	}
	return publishSourceArtifacts(outputRoot, bundlePath, manifestPath, manifest)
}

// prepareSourceTransport 在 staging bare repository 中传输对象闭包并创建确定性提交引用。
func prepareSourceTransport(ctx context.Context, repoRoot string, bareRoot string, stageRoot string, spec gate.SourceSpec, plan sourcePlan, baseline SourceBaseline) (sourcePlan, error) {
	roots, err := sourceMaterializationRoots(plan.tree, plan.baseTree, spec.ObjectFormat)
	if err != nil {
		return sourcePlan{}, err
	}
	if err := transferObjectClosure(ctx, repoRoot, bareRoot, stageRoot, roots, []string{baseline.CommitSHA}, spec.ObjectFormat, baseline); err != nil {
		return sourcePlan{}, err
	}
	syntheticBaseCommit, err := createSyntheticBaseCommit(ctx, bareRoot, plan.baseTree, baseline.CommitSHA, spec.ObjectFormat)
	if err != nil {
		return sourcePlan{}, err
	}
	plan.syntheticBaseCommit = syntheticBaseCommit
	transportCommit, err := createTransportCommit(ctx, bareRoot, plan.tree, syntheticBaseCommit, spec.ObjectFormat)
	if err != nil {
		return sourcePlan{}, err
	}
	plan.transportCommit = transportCommit
	if err := prepareBundleRefs(ctx, bareRoot, plan.transportCommit, plan.tree, plan.syntheticBaseCommit, spec.ObjectFormat); err != nil {
		return sourcePlan{}, err
	}
	return plan, nil
}

// sourceMaterializationRoots 返回候选树与非空父树的传输根，避免把确定性空树重复传输。
func sourceMaterializationRoots(tree string, baseTree string, format gate.GitObjectFormat) ([]string, error) {
	roots := []string{tree}
	emptyTree, err := DeterministicSourceEmptyTreeSHA(format)
	if err != nil {
		return nil, err
	}
	if baseTree != emptyTree {
		roots = append(roots, baseTree)
	}
	return roots, nil
}

// ensureSourceEmptyTree 确保 root/empty-tree SourceSpec 所需的确定性空树已写入候选 ODB。
func ensureSourceEmptyTree(ctx context.Context, bareRoot string, tree string, format gate.GitObjectFormat) error {
	emptyTree, err := DeterministicSourceEmptyTreeSHA(format)
	if err != nil {
		return err
	}
	if tree != emptyTree {
		return nil
	}
	output, err := runGitOutput(ctx, bareRoot, bytes.NewReader(nil), "hash-object", "-t", "tree", "-w", "--stdin")
	if err != nil {
		return fmt.Errorf("write deterministic empty source tree: %w", err)
	}
	actual, err := strictGitLine(output)
	if err != nil || actual != emptyTree {
		return errors.New("deterministic empty source tree identity drifted")
	}
	return nil
}

// ImportAndVerifySourceBundle 在显式 accepted image baseline object store 上
// 导入 thin bundle，并复核 manifest、prerequisite、commit/tree 与对象闭包。
// baseline 是强制参数；缺失时无法验证 prerequisite，必须 fail-fast。
func ImportAndVerifySourceBundle(ctx context.Context, outputRoot string, baseline SourceBaseline) (SourceMaterializationManifest, error) {
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
	if err := validateSourceBaseline(ctx, baseline, manifest.ObjectFormat); err != nil {
		return SourceMaterializationManifest{}, err
	}
	if manifest.BaselineCommitSHA != baseline.CommitSHA || manifest.BaselineTreeSHA != baseline.TreeSHA {
		return SourceMaterializationManifest{}, errors.New("source manifest baseline identity does not match accepted image baseline")
	}
	if err := manifest.Validate(); err != nil {
		return SourceMaterializationManifest{}, err
	}
	if err := verifyPublishedSourceBundle(ctx, bundlePath, outputRoot, manifest, baseline); err != nil {
		return SourceMaterializationManifest{}, err
	}
	return manifest, nil
}

// verifyPublishedSourceBundle 检查发布 bundle 的摘要并委托 sourceexport
// owner 完成 thin bundle、prerequisite 和对象闭包复验。
func verifyPublishedSourceBundle(ctx context.Context, bundlePath string, outputRoot string, manifest SourceMaterializationManifest, baseline SourceBaseline) error {
	digest, err := digestSourceFile(bundlePath)
	if err != nil {
		return err
	}
	if digest != manifest.BundleDigest {
		return errors.New("source bundle digest does not match manifest")
	}
	return importAndVerifyBundle(ctx, bundlePath, outputRoot, manifest, baseline)
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

// validateSourceBaseline 校验 accepted image 提供的只读 prerequisite 对象存储；
// proves that the prerequisite object store is the
// explicit read-only Git store from the accepted image. It never creates or
// modifies objects in that store.
func validateSourceBaseline(ctx context.Context, baseline SourceBaseline, objectFormat gate.GitObjectFormat) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if baseline.ObjectFormat != objectFormat {
		return fmt.Errorf("source baseline object format %q does not match candidate %q", baseline.ObjectFormat, objectFormat)
	}
	if !validCanonicalPath(baseline.RepositoryRoot) {
		return errors.New("source baseline repository root must be canonical and absolute")
	}
	if err := validateCanonicalDirectory(baseline.RepositoryRoot, false); err != nil {
		return fmt.Errorf("validate source baseline repository root: %w", err)
	}
	baselineInfo, err := os.Stat(baseline.RepositoryRoot)
	if err != nil {
		return fmt.Errorf("stat source baseline repository root: %w", err)
	}
	if baselineInfo.Mode().Perm()&0o222 != 0 {
		return errors.New("source baseline repository root must be read-only")
	}
	if err := validateReadOnlyGitTree(baseline.RepositoryRoot); err != nil {
		return err
	}
	return validateSourceBaselineObjects(ctx, baseline, objectFormat)
}

// validateSourceBaselineObjects 复核 accepted baseline 的 Git 格式、确定性
// parentless commit 和 tree 闭包，拒绝任何身份漂移。
func validateSourceBaselineObjects(ctx context.Context, baseline SourceBaseline, objectFormat gate.GitObjectFormat) error {
	if err := validateSourceBaselineRepository(ctx, baseline, objectFormat); err != nil {
		return err
	}
	return validateSourceBaselineCommit(ctx, baseline, objectFormat)
}

// validateSourceBaselineRepository 复核对象 ID、bare repository 标记与
// Git object format，确保 accepted image 的对象存储身份固定。
func validateSourceBaselineRepository(ctx context.Context, baseline SourceBaseline, objectFormat gate.GitObjectFormat) error {
	if !validOID(baseline.CommitSHA, objectFormat) || !validOID(baseline.TreeSHA, objectFormat) {
		return errors.New("source baseline commit and tree must be valid Git object IDs")
	}
	bare, err := runGitOutput(ctx, baseline.RepositoryRoot, nil, "rev-parse", "--is-bare-repository")
	if err != nil || string(bare) != "true\n" {
		return errors.Join(errors.New("source baseline repository must be a bare read-only Git object store"), err)
	}
	format, err := runGitOutput(ctx, baseline.RepositoryRoot, nil, "rev-parse", "--show-object-format")
	if err != nil || string(format) != string(objectFormat)+"\n" {
		return errors.Join(errors.New("source baseline Git object format is not canonical"), err)
	}
	return nil
}

// validateSourceBaselineCommit 校验 accepted baseline commit 的 tree、父级
// 和确定性身份，拒绝历史 commit 或错误 parent。
func validateSourceBaselineCommit(ctx context.Context, baseline SourceBaseline, objectFormat gate.GitObjectFormat) error {
	commit, err := readSourceObject(ctx, baseline.RepositoryRoot, baseline.CommitSHA)
	if err != nil || commit.kind != "commit" {
		return errors.Join(errors.New("source baseline commit object is missing or not a commit"), err)
	}
	tree, parents, err := parseCommitObject(commit, baseline.TreeSHA)
	if err != nil || tree != baseline.TreeSHA {
		return errors.Join(errors.New("source baseline commit does not identify the accepted source tree"), err)
	}
	if len(parents) != 0 {
		return errors.New("source baseline commit must be deterministic and parentless")
	}
	expectedCommit, err := DeterministicSourceBaselineCommitSHA(baseline.TreeSHA, objectFormat)
	if err != nil || baseline.CommitSHA != expectedCommit {
		return errors.New("source baseline commit is not the deterministic accepted baseline identity")
	}
	return nil
}

// validateReadOnlyGitTree 递归确认 accepted image 的 Git 对象存储中没有
// 可写或符号链接条目，避免只读声明被单个 HEAD/config 文件绕过。
func validateReadOnlyGitTree(root string) error {
	entries := 0
	var bytes int64
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("source baseline tree must not contain symlink %s", path)
		}
		entries++
		if entries > maxReadOnlyGitTreeEntries {
			return errors.New("source baseline tree contains too many entries")
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat source baseline entry %s: %w", path, err)
		}
		if info.Mode().Perm()&0o222 != 0 {
			return fmt.Errorf("source baseline entry %s must be read-only", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source baseline tree contains unsupported entry %s", path)
		}
		bytes += info.Size()
		if bytes > maxReadOnlyGitTreeBytes {
			return errors.New("source baseline tree contains too many bytes")
		}
		return nil
	})
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

func inspectSourcePlan(ctx context.Context, repoRoot string, spec gate.SourceSpec, baseline *SourceBaseline) (sourcePlan, error) {
	switch spec.Kind {
	case gate.SourceKindCommit:
		return inspectCommitPlan(ctx, repoRoot, spec.Commit.SHA, spec.SourceTreeSHA, spec.ObjectFormat, baseline)
	case gate.SourceKindTree:
		return inspectTreePlan(ctx, repoRoot, spec.Tree, spec.SourceTreeSHA, spec.ObjectFormat, baseline)
	case gate.SourceKindRange:
		return inspectRangePlan(ctx, repoRoot, spec.Range, spec.SourceTreeSHA, spec.ObjectFormat, baseline)
	default:
		return sourcePlan{}, fmt.Errorf("unsupported source kind %q", spec.Kind)
	}
}

// inspectCommitPlan 解析 commit source 并确定候选提交对应的父树。
func inspectCommitPlan(ctx context.Context, repoRoot string, commitSHA string, expectedTree string, format gate.GitObjectFormat, baseline *SourceBaseline) (sourcePlan, error) {
	commit, err := readSourceObject(ctx, repoRoot, commitSHA)
	if err != nil {
		return sourcePlan{}, err
	}
	tree, parents, err := parseCommitObject(commit, expectedTree)
	if err != nil {
		return sourcePlan{}, err
	}
	baseTree, err := DeterministicSourceEmptyTreeSHA(format)
	if err != nil {
		return sourcePlan{}, err
	}
	plan := sourcePlan{tree: tree, commit: commitSHA, baseTree: baseTree}
	if len(parents) > 1 {
		return sourcePlan{}, errors.New("commit source must have at most one parent for deterministic source diff")
	}
	if len(parents) == 1 {
		plan.baseCommit = parents[0]
		plan.baseTree, err = sourceCommitTree(ctx, repoRoot, parents[0], baseline)
		if err != nil {
			return sourcePlan{}, fmt.Errorf("read commit source parent tree: %w", err)
		}
	}
	return plan, nil
}

// inspectTreePlan 复核显式 tree 与可选 parent commit，不读取 index 或 HEAD。
func inspectTreePlan(ctx context.Context, repoRoot string, source *gate.TreeSource, expectedTree string, format gate.GitObjectFormat, baseline *SourceBaseline) (sourcePlan, error) {
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
	baseTree, err := DeterministicSourceEmptyTreeSHA(format)
	if err != nil {
		return sourcePlan{}, err
	}
	if source.ParentCommitSHA != "" {
		baseTree, err = sourceCommitTree(ctx, repoRoot, source.ParentCommitSHA, baseline)
		if err != nil {
			return sourcePlan{}, fmt.Errorf("read tree source parent tree: %w", err)
		}
	}
	return sourcePlan{
		tree:       source.SHA,
		baseCommit: source.ParentCommitSHA,
		baseTree:   baseTree,
	}, nil
}

// inspectRangePlan 复核 range 的 head/base 对象类型与 head tree。
func inspectRangePlan(ctx context.Context, repoRoot string, source *gate.RangeSource, expectedTree string, format gate.GitObjectFormat, baseline *SourceBaseline) (sourcePlan, error) {
	head, err := readSourceObject(ctx, repoRoot, source.HeadSHA)
	if err != nil {
		return sourcePlan{}, err
	}
	tree, _, err := parseCommitObject(head, expectedTree)
	if err != nil {
		return sourcePlan{}, fmt.Errorf("validate range head: %w", err)
	}
	baseCommit := ""
	baseTree, err := DeterministicSourceEmptyTreeSHA(format)
	if err != nil {
		return sourcePlan{}, err
	}
	if source.BaseKind == gate.BaseKindCommit {
		baseCommit = source.BaseSHA
		baseTree, err = sourceCommitTree(ctx, repoRoot, source.BaseSHA, baseline)
		if err != nil {
			return sourcePlan{}, fmt.Errorf("read range base tree: %w", err)
		}
	}
	return sourcePlan{tree: tree, commit: source.HeadSHA, baseCommit: baseCommit, baseTree: baseTree}, nil
}

// sourceCommitTree 读取一个显式 commit，仅返回其 tree identity；parent history 保留在
// transport bundle 之外。
func sourceCommitTree(ctx context.Context, repoRoot string, commitSHA string, baseline *SourceBaseline) (string, error) {
	if baseline != nil && commitSHA == baseline.CommitSHA {
		return baseline.TreeSHA, nil
	}
	commit, err := readSourceObject(ctx, repoRoot, commitSHA)
	if err != nil {
		return "", err
	}
	tree, _, err := parseCommitObject(commit, "")
	if err != nil {
		return "", err
	}
	return tree, nil
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
	if expectedTree != "" && tree != expectedTree {
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

// sourceObjectDirectory 解析 image 提供的对象目录并拒绝越界间接引用；
// resolves the image-provided object directory and
// rejects indirection outside the canonical baseline repository.
func sourceObjectDirectory(ctx context.Context, baseline SourceBaseline) (string, error) {
	output, err := runGitOutput(ctx, baseline.RepositoryRoot, nil, "rev-parse", "--git-path", "objects")
	if err != nil {
		return "", fmt.Errorf("resolve source baseline object directory: %w", err)
	}
	objectPath, err := strictGitLine(output)
	if err != nil {
		return "", fmt.Errorf("parse source baseline object directory: %w", err)
	}
	if !filepath.IsAbs(objectPath) {
		objectPath = filepath.Join(baseline.RepositoryRoot, objectPath)
	}
	resolved, err := filepath.EvalSymlinks(objectPath)
	if err != nil {
		return "", fmt.Errorf("canonicalize source baseline object directory: %w", err)
	}
	if !validCanonicalPath(resolved) || resolved != objectPath {
		return "", errors.New("source baseline object directory must be canonical and non-symlink")
	}
	info, err := os.Stat(objectPath)
	if err != nil {
		return "", fmt.Errorf("stat source baseline object directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("source baseline object directory is not a directory")
	}
	if info.Mode().Perm()&0o222 != 0 {
		return "", errors.New("source baseline object directory must be read-only")
	}
	return objectPath, nil
}

// configureObjectStoreAlternate 配置 baseline 对象为 staging 仓库的只读 alternate；
// makes baseline objects readable to the
// staging repository without copying them into the candidate bundle.
func configureObjectStoreAlternate(ctx context.Context, bareRoot string, baseline SourceBaseline) error {
	objectPath, err := sourceObjectDirectory(ctx, baseline)
	if err != nil {
		return err
	}
	gitObjects, err := runGitOutput(ctx, bareRoot, nil, "rev-parse", "--git-path", "objects")
	if err != nil {
		return fmt.Errorf("resolve target Git object directory: %w", err)
	}
	targetObjects, err := strictGitLine(gitObjects)
	if err != nil {
		return fmt.Errorf("parse target Git object directory: %w", err)
	}
	if !filepath.IsAbs(targetObjects) {
		targetObjects = filepath.Join(bareRoot, targetObjects)
	}
	if !validCanonicalPath(targetObjects) {
		return errors.New("target Git object directory must be canonical")
	}
	resolved, err := filepath.EvalSymlinks(targetObjects)
	if err != nil || resolved != targetObjects {
		return errors.New("target Git object directory must not be a symlink")
	}
	infoPath := filepath.Join(targetObjects, "info", "alternates")
	if err := os.MkdirAll(filepath.Dir(infoPath), privateSourceDirMode); err != nil {
		return fmt.Errorf("create source object alternates directory: %w", err)
	}
	if err := os.WriteFile(infoPath, []byte(objectPath+"\n"), 0o600); err != nil {
		return fmt.Errorf("write source object alternates: %w", err)
	}
	return nil
}

// runGitWithObjectStore runs Git with one validated read-only image object
// store as an alternate. The alternate is never inherited from the caller's
// environment and cannot silently point at a workspace or remote.
func runGitWithObjectStore(ctx context.Context, repoRoot string, baseline SourceBaseline, stdin io.Reader, stdout io.Writer, args ...string) error {
	objectPath, err := sourceObjectDirectory(ctx, baseline)
	if err != nil {
		return err
	}
	return runGitWithEnvironment(ctx, repoRoot, stdin, stdout, append(sourceGitEnvironment(), "GIT_ALTERNATE_OBJECT_DIRECTORIES="+objectPath), args...)
}

// transferObjectClosure 使用 pack-objects 将显式根的完整闭包送入临时 bare repo。
func transferObjectClosure(ctx context.Context, repoRoot string, bareRoot string, stageRoot string, roots []string, exclusions []string, format gate.GitObjectFormat, baseline SourceBaseline) error {
	packPath := filepath.Join(stageRoot, "objects.pack")
	pack, err := os.OpenFile(packPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source object pack: %w", err)
	}
	arguments := append([]string(nil), roots...)
	for _, exclusion := range exclusions {
		arguments = append(arguments, "^"+exclusion)
	}
	rootInput := strings.Join(arguments, "\n") + "\n"
	runErr := runGitWithObjectStore(ctx, repoRoot, baseline, strings.NewReader(rootInput), pack, "pack-objects", "--stdout", "--revs", "--thin")
	closeErr := pack.Close()
	if runErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("pack source object closure: %w", runErr), closeErr)
	}
	return indexSourceObjectPack(ctx, bareRoot, packPath, format)
}

// indexSourceObjectPack 将 thin pack 导入临时仓库并严格复核 index-pack
// 返回的 pack 身份，保证物化闭包不会静默缺失。
func indexSourceObjectPack(ctx context.Context, bareRoot string, packPath string, format gate.GitObjectFormat) error {
	pack, err := os.Open(packPath)
	if err != nil {
		return fmt.Errorf("open source object pack: %w", err)
	}
	output, indexErr := runGitOutput(ctx, bareRoot, pack, "index-pack", "--stdin", "--fix-thin")
	closeErr := pack.Close()
	if indexErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("index source object pack: %w", indexErr), closeErr)
	}
	line, err := strictGitLine(output)
	if err != nil || !strings.HasPrefix(line, "pack\t") || !validOID(strings.TrimPrefix(line, "pack\t"), format) {
		return errors.New("git index-pack returned unexpected output")
	}
	return nil
}

// createSyntheticBaseCommit 写入 deterministic candidate-parent commit。
// 其 tree 是 SourceSpec parent/base tree，唯一 parent 是 accepted baseline，
// 因此 bundle 保持 thin 且只有一个 prerequisite。
func createSyntheticBaseCommit(ctx context.Context, bareRoot string, tree string, baseline string, format gate.GitObjectFormat) (string, error) {
	expected, err := DeterministicSourceSyntheticBaseCommitSHA(tree, baseline, format)
	if err != nil {
		return "", err
	}
	output, err := runGitOutput(ctx, bareRoot, bytes.NewReader(deterministicSourceSyntheticBasePayload(tree, baseline)), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("write deterministic source synthetic base commit: %w", err)
	}
	actual, err := strictGitLine(output)
	if err != nil || actual != expected {
		return "", errors.New("deterministic source synthetic base commit identity drifted")
	}
	return expected, nil
}

// createTransportCommit writes the deterministic transport commit. Git can
// then produce a standard bundle with exactly one prerequisite because the
// transport commit 将 candidate-parent synthetic base 命名为其 parent。
func createTransportCommit(ctx context.Context, bareRoot string, tree string, syntheticBase string, format gate.GitObjectFormat) (string, error) {
	expected, err := DeterministicSourceTransportCommitSHA(tree, syntheticBase, format)
	if err != nil {
		return "", err
	}
	output, err := runGitOutput(ctx, bareRoot, bytes.NewReader(deterministicSourceTransportPayload(tree, syntheticBase)), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("write deterministic source transport commit: %w", err)
	}
	actual, err := strictGitLine(output)
	if err != nil || actual != expected {
		return "", errors.New("deterministic source transport commit identity drifted")
	}
	return expected, nil
}

// prepareBundleRefs 只创建确定性的 transport ref；synthetic base 作为
// transport 的可达历史随 bundle 携带但不广告额外 ref，accepted baseline
// 仅作为唯一 prerequisite。
func prepareBundleRefs(ctx context.Context, bareRoot string, commit string, tree string, parent string, format gate.GitObjectFormat) error {
	input := fmt.Sprintf("create %s %s\n", sourceBundleRef, commit)
	commits := []string{commit}
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
		return errors.Join(errors.New("transport commit tree verification failed"), err)
	}
	if len(parents) != 1 || parents[0] != parent {
		return errors.New("transport commit must have exactly the synthetic base as parent")
	}
	expected, err := DeterministicSourceTransportCommitSHA(tree, parent, format)
	if err != nil || commit != expected {
		return errors.New("transport commit is not deterministic")
	}
	return verifyObjectClosure(ctx, bareRoot, commits...)
}
