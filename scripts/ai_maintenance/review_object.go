package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	reviewModeRange     = "range"
	reviewModeStaged    = "staged"
	reviewModeWorktree  = "worktree"
	reviewModePathsOnly = "paths-only"
)

type reviewObject struct {
	Mode                 string `json:"mode"`
	BaseTree             string `json:"base_tree,omitempty"`
	CandidateTree        string `json:"candidate_tree,omitempty"`
	BaseCommit           string `json:"base_commit,omitempty"`
	ComparisonBaseCommit string `json:"comparison_base_commit,omitempty"`
	CandidateCommit      string `json:"candidate_commit,omitempty"`
	Executable           bool   `json:"executable"`
}

type reviewObjectOptions struct {
	Base     string
	Head     string
	Staged   bool
	Worktree bool
}

// pathsOnlyReviewObject 标记仅用于展示路由的路径集合，禁止进入执行器。
func pathsOnlyReviewObject() reviewObject {
	return reviewObject{Mode: reviewModePathsOnly, Executable: false}
}

// resolveReviewObject 把互斥 CLI 模式冻结为同一对 exact Git tree OID。
func resolveReviewObject(repoRoot string, options reviewObjectOptions) (reviewObject, error) {
	if err := validateReviewObjectOptions(options); err != nil {
		return reviewObject{}, err
	}
	if options.Staged {
		return resolveStagedReviewObject(repoRoot)
	}
	if options.Worktree {
		return resolveWorktreeReviewObject(repoRoot)
	}
	base := options.Base
	if base == "" {
		base = "HEAD~1"
	}
	head := options.Head
	if head == "" {
		head = "HEAD"
	}
	return resolveRangeReviewObject(repoRoot, base, head)
}

// validateReviewObjectOptions 拒绝会产生多个候选对象来源的 flag 组合。
func validateReviewObjectOptions(options reviewObjectOptions) error {
	if options.Staged && options.Worktree {
		return errors.New("review object modes --staged and --worktree are mutually exclusive")
	}
	if (options.Staged || options.Worktree) && (options.Base != "" || options.Head != "") {
		return errors.New("range flags --base/--head cannot be combined with --staged or --worktree")
	}
	return nil
}

// resolveRangeReviewObject 冻结请求两端及其 merge-base，保持三点 provenance 可追溯。
func resolveRangeReviewObject(repoRoot, base, head string) (reviewObject, error) {
	baseCommit, err := resolveGitCommit(repoRoot, base)
	if err != nil {
		return reviewObject{}, fmt.Errorf("resolve range base %q: %w", base, err)
	}
	candidateCommit, err := resolveGitCommit(repoRoot, head)
	if err != nil {
		return reviewObject{}, fmt.Errorf("resolve range candidate %q: %w", head, err)
	}
	comparisonBaseCommit, err := resolveGitMergeBase(repoRoot, baseCommit, candidateCommit)
	if err != nil {
		return reviewObject{}, fmt.Errorf("resolve range merge-base: %w", err)
	}
	baseTree, err := resolveGitTree(repoRoot, comparisonBaseCommit)
	if err != nil {
		return reviewObject{}, err
	}
	candidateTree, err := resolveGitTree(repoRoot, candidateCommit)
	if err != nil {
		return reviewObject{}, err
	}
	return newExactReviewObject(reviewModeRange, baseTree, candidateTree, baseCommit, comparisonBaseCommit, candidateCommit)
}

func resolveStagedReviewObject(repoRoot string) (reviewObject, error) {
	baseCommit, err := resolveGitCommit(repoRoot, "HEAD")
	if err != nil {
		return reviewObject{}, fmt.Errorf("resolve staged base: %w", err)
	}
	baseTree, err := resolveGitTree(repoRoot, baseCommit)
	if err != nil {
		return reviewObject{}, fmt.Errorf("resolve staged base tree: %w", err)
	}
	candidateTree, err := gitSingleLine(repoRoot, stagedReviewGitEnvironment(), "write-tree")
	if err != nil {
		return reviewObject{}, fmt.Errorf("capture staged candidate tree: %w", err)
	}
	return newExactReviewObject(reviewModeStaged, baseTree, candidateTree, baseCommit, "", "")
}

func resolveWorktreeReviewObject(repoRoot string) (reviewObject, error) {
	baseCommit, err := resolveGitCommit(repoRoot, "HEAD")
	if err != nil {
		return reviewObject{}, fmt.Errorf("resolve worktree base: %w", err)
	}
	baseTree, err := resolveGitTree(repoRoot, baseCommit)
	if err != nil {
		return reviewObject{}, fmt.Errorf("resolve worktree base tree: %w", err)
	}
	candidateTree, err := captureWorktreeTree(repoRoot, baseTree)
	if err != nil {
		return reviewObject{}, err
	}
	return newExactReviewObject(reviewModeWorktree, baseTree, candidateTree, baseCommit, "", "")
}

func newExactReviewObject(mode, baseTree, candidateTree, baseCommit, comparisonBaseCommit, candidateCommit string) (reviewObject, error) {
	object := reviewObject{
		Mode: mode, BaseTree: baseTree, CandidateTree: candidateTree,
		BaseCommit: baseCommit, ComparisonBaseCommit: comparisonBaseCommit,
		CandidateCommit: candidateCommit, Executable: true,
	}
	if err := object.validateExecutable(); err != nil {
		return reviewObject{}, err
	}
	return object, nil
}

// validateExecutable 验证执行对象具备完整且模式一致的不可移动身份。
func (object reviewObject) validateExecutable() error {
	if object.Mode == reviewModePathsOnly {
		return errors.New("paths-only review object is not executable")
	}
	if !object.Executable {
		return errors.New("review object is not executable")
	}
	if !executableReviewMode(object.Mode) {
		return fmt.Errorf("unsupported review object mode %q", object.Mode)
	}
	if !validGitOID(object.BaseTree) {
		return errors.New("executable review object requires exact base tree OID")
	}
	if !validGitOID(object.CandidateTree) {
		return errors.New("executable review object requires exact candidate tree OID")
	}
	return object.validateCommitProvenance()
}

func executableReviewMode(mode string) bool {
	switch mode {
	case reviewModeRange, reviewModeStaged, reviewModeWorktree:
		return true
	default:
		return false
	}
}

// validateCommitProvenance 校验各模式允许声明的 commit provenance 字段。
func (object reviewObject) validateCommitProvenance() error {
	if !validGitOID(object.BaseCommit) {
		return errors.New("executable review object requires resolved base commit provenance")
	}
	switch object.Mode {
	case reviewModeRange:
		if !validGitOID(object.CandidateCommit) {
			return errors.New("range review object requires resolved candidate commit provenance")
		}
		if !validGitOID(object.ComparisonBaseCommit) {
			return errors.New("range review object requires resolved merge-base commit provenance")
		}
	case reviewModeStaged, reviewModeWorktree:
		if object.CandidateCommit != "" {
			return errors.New("staged/worktree review object cannot claim candidate commit provenance")
		}
		if object.ComparisonBaseCommit != "" {
			return errors.New("staged/worktree review object cannot claim merge-base provenance")
		}
	default:
		return errors.New("executable review object requires exact base and candidate tree OIDs")
	}
	return nil
}

func validGitOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// changedFilesFromReviewObject 只比较计划中已冻结的 tree pair，不读取 live refs 或工作树。
func changedFilesFromReviewObject(repoRoot string, object reviewObject) ([]string, error) {
	if err := object.verifyRepositoryObjects(repoRoot); err != nil {
		return nil, err
	}
	out, err := gitOutput(repoRoot, nil, "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", object.BaseTree, object.CandidateTree, "--")
	if err != nil {
		return nil, fmt.Errorf("collect exact review object changed files: %w", err)
	}
	return normalizeFiles(nulSeparatedPaths(out)), nil
}

// checkReviewObjectWhitespace 把 whitespace 判定绑定到与 changed-files 相同的 tree pair。
func checkReviewObjectWhitespace(repoRoot string, object reviewObject) error {
	if err := object.verifyRepositoryObjects(repoRoot); err != nil {
		return err
	}
	out, err := gitOutput(repoRoot, nil, "diff", "--no-ext-diff", "--no-renames", "--check", object.BaseTree, object.CandidateTree, "--")
	if err != nil {
		return fmt.Errorf("check exact review object whitespace: %w", err)
	}
	if len(out) != 0 {
		return fmt.Errorf("check exact review object whitespace produced unexpected output: %s", out)
	}
	return nil
}

// verifyRepositoryObjects 在目标仓库复核 commit、merge-base 与 tree 的绑定关系。
func (object reviewObject) verifyRepositoryObjects(repoRoot string) error {
	if err := object.validateExecutable(); err != nil {
		return err
	}
	baseTreeCommit := object.BaseCommit
	if object.Mode == reviewModeRange {
		comparisonBaseCommit, err := resolveGitMergeBase(repoRoot, object.BaseCommit, object.CandidateCommit)
		if err != nil {
			return fmt.Errorf("verify range merge-base provenance: %w", err)
		}
		if comparisonBaseCommit != object.ComparisonBaseCommit {
			return errors.New("review merge-base provenance drifted")
		}
		baseTreeCommit = comparisonBaseCommit
	}
	baseTree, err := resolveGitTree(repoRoot, baseTreeCommit)
	if err != nil {
		return fmt.Errorf("verify review base commit provenance: %w", err)
	}
	if baseTree != object.BaseTree {
		return errors.New("review base commit provenance does not match base tree")
	}
	if object.Mode != reviewModeRange {
		return nil
	}
	candidateTree, err := resolveGitTree(repoRoot, object.CandidateCommit)
	if err != nil {
		return fmt.Errorf("verify review candidate commit provenance: %w", err)
	}
	if candidateTree != object.CandidateTree {
		return errors.New("review candidate commit provenance does not match candidate tree")
	}
	return nil
}

func resolveGitTree(repoRoot, revision string) (string, error) {
	tree, err := gitSingleLine(repoRoot, nil, "rev-parse", "--verify", "--end-of-options", revision+"^{tree}")
	if err != nil {
		return "", err
	}
	if !validGitOID(tree) {
		return "", fmt.Errorf("revision %q resolved invalid tree OID %q", revision, tree)
	}
	return tree, nil
}

func resolveGitCommit(repoRoot, revision string) (string, error) {
	commit, err := gitSingleLine(repoRoot, nil, "rev-parse", "--verify", "--end-of-options", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	if !validGitOID(commit) {
		return "", fmt.Errorf("revision %q resolved invalid commit OID %q", revision, commit)
	}
	return commit, nil
}

func resolveGitMergeBase(repoRoot, baseCommit, candidateCommit string) (string, error) {
	commit, err := gitSingleLine(repoRoot, nil, "merge-base", "--", baseCommit, candidateCommit)
	if err != nil {
		return "", err
	}
	if !validGitOID(commit) {
		return "", fmt.Errorf("merge-base resolved invalid commit OID %q", commit)
	}
	return commit, nil
}

// captureWorktreeTree 用隔离 index 一次冻结当前 tracked、deleted 与 untracked 内容。
func captureWorktreeTree(repoRoot, baseTree string) (tree string, resultErr error) {
	indexFile, err := os.CreateTemp("", "ai-maintenance-index-*")
	if err != nil {
		return "", fmt.Errorf("create worktree review index: %w", err)
	}
	indexPath := indexFile.Name()
	if err := indexFile.Close(); err != nil {
		return "", errors.Join(fmt.Errorf("close worktree review index: %w", err), removeReviewIndex(indexPath))
	}
	if err := os.Remove(indexPath); err != nil {
		return "", fmt.Errorf("prepare worktree review index: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, removeReviewIndex(indexPath)) }()
	environment := replaceEnvironment(reviewGitEnvironment(), "GIT_INDEX_FILE", indexPath)
	if _, err := gitOutput(repoRoot, environment, "read-tree", baseTree); err != nil {
		return "", fmt.Errorf("seed worktree review index: %w", err)
	}
	if _, err := gitOutput(repoRoot, environment, "add", "-A", "--", "."); err != nil {
		return "", fmt.Errorf("capture worktree files: %w", err)
	}
	tree, err = gitSingleLine(repoRoot, environment, "write-tree")
	if err != nil {
		return "", fmt.Errorf("write worktree candidate tree: %w", err)
	}
	return tree, nil
}

func removeReviewIndex(indexPath string) error {
	var result error
	for _, path := range []string{indexPath, indexPath + ".lock"} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove review index %q: %w", path, err))
		}
	}
	return result
}

// materializeReviewObject 把 candidate tree 放入仓库内受控 worktree，并以 synthetic parent 保留 base 边界。
func materializeReviewObject(repoRoot string, object reviewObject) (string, func() error, error) {
	if err := object.verifyRepositoryObjects(repoRoot); err != nil {
		return "", nil, err
	}
	repository, err := canonicalReviewRepository(repoRoot)
	if err != nil {
		return "", nil, err
	}
	baseCommit, err := createReviewCommit(repository, object.BaseTree, "", "review base")
	if err != nil {
		return "", nil, err
	}
	candidateCommit, err := createReviewCommit(repository, object.CandidateTree, baseCommit, "review candidate")
	if err != nil {
		return "", nil, err
	}
	worktreeRoot, sourceRoot, err := reserveReviewWorktree(repository)
	if err != nil {
		return "", nil, err
	}
	hooksRoot, err := os.MkdirTemp(worktreeRoot, ".ai-maintenance-disabled-hooks-*")
	if err != nil {
		return "", nil, errors.Join(fmt.Errorf("reserve disabled hooks directory: %w", err), removeReservedReviewPath(worktreeRoot, sourceRoot))
	}
	cleanup := func() error {
		return errors.Join(
			removeReviewWorktree(repository, worktreeRoot, sourceRoot),
			removeReservedReviewPath(worktreeRoot, hooksRoot),
		)
	}
	if err := populateReviewWorktree(repository, sourceRoot, hooksRoot, candidateCommit, object.CandidateTree); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	if err := verifyMaterializedReviewObject(sourceRoot, object); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return sourceRoot, cleanup, nil
}

// populateReviewWorktree 禁用本地 hook，并从 candidate tree 显式填充隔离 worktree。
func populateReviewWorktree(repository, sourceRoot, hooksRoot, candidateCommit, candidateTree string) error {
	if _, err := gitOutput(repository, nil, "-c", "core.hooksPath="+hooksRoot,
		"worktree", "add", "--quiet", "--detach", "--no-checkout", "--", sourceRoot, candidateCommit); err != nil {
		return fmt.Errorf("materialize exact review worktree: %w", err)
	}
	if _, err := gitOutput(sourceRoot, nil, "read-tree", candidateTree); err != nil {
		return fmt.Errorf("seed exact review worktree index: %w", err)
	}
	if _, err := gitOutput(sourceRoot, nil, "checkout-index", "--all", "--force"); err != nil {
		return fmt.Errorf("populate exact review worktree: %w", err)
	}
	return nil
}

// canonicalReviewRepository 校验调用方路径与 Git 识别的仓库根完全一致。
func canonicalReviewRepository(repoRoot string) (string, error) {
	absolute, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve review repository path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize review repository path: %w", err)
	}
	topLevel, err := gitSingleLine(canonical, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("verify review repository root: %w", err)
	}
	canonicalTopLevel, err := filepath.EvalSymlinks(topLevel)
	if err != nil {
		return "", fmt.Errorf("canonicalize Git review repository root: %w", err)
	}
	if canonicalTopLevel != canonical {
		return "", fmt.Errorf("review repository root mismatch: path=%s git=%s", canonical, canonicalTopLevel)
	}
	return canonical, nil
}

// reserveReviewWorktree 仅在仓库内物理 .worktrees 目录预留唯一目标路径。
func reserveReviewWorktree(repository string) (string, string, error) {
	worktreeRoot := filepath.Join(repository, ".worktrees")
	if err := os.MkdirAll(worktreeRoot, 0o755); err != nil {
		return "", "", fmt.Errorf("create review worktree root: %w", err)
	}
	info, err := os.Lstat(worktreeRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", fmt.Errorf("review worktree root must be a physical directory: %s", worktreeRoot)
	}
	reserved, err := os.MkdirTemp(worktreeRoot, "ai-maintenance-review-*")
	if err != nil {
		return "", "", fmt.Errorf("reserve review worktree path: %w", err)
	}
	if err := validateReviewWorktreePath(worktreeRoot, reserved); err != nil {
		return "", "", errors.Join(err, os.Remove(reserved))
	}
	if err := os.Remove(reserved); err != nil {
		return "", "", fmt.Errorf("prepare review worktree path: %w", err)
	}
	return worktreeRoot, reserved, nil
}

// validateReviewWorktreePath 拒绝根目录自身、绝对相对值和任何越界目标。
func validateReviewWorktreePath(worktreeRoot, target string) error {
	relative, err := filepath.Rel(worktreeRoot, target)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("review worktree target escaped .worktrees: %s", target)
	}
	return nil
}

func verifyMaterializedReviewObject(sourceRoot string, object reviewObject) error {
	actualTree, err := resolveGitTree(sourceRoot, "HEAD")
	if err != nil {
		return fmt.Errorf("verify materialized review tree: %w", err)
	}
	if actualTree != object.CandidateTree {
		return fmt.Errorf("materialized review tree drifted: got %s want %s", actualTree, object.CandidateTree)
	}
	status, err := gitOutput(sourceRoot, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("verify materialized review worktree status: %w", err)
	}
	if len(status) != 0 {
		return errors.New("materialized review worktree differs from exact candidate tree")
	}
	return nil
}

func removeReservedReviewPath(worktreeRoot, sourceRoot string) error {
	if err := validateReviewWorktreePath(worktreeRoot, sourceRoot); err != nil {
		return err
	}
	if err := os.Remove(sourceRoot); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove reserved review worktree path: %w", err)
	}
	return nil
}

// removeReviewWorktree 优先撤销 Git 注册，再安全清理未注册的精确预留路径。
func removeReviewWorktree(repository, worktreeRoot, sourceRoot string) error {
	if err := validateReviewWorktreePath(worktreeRoot, sourceRoot); err != nil {
		return err
	}
	if _, err := gitOutput(repository, nil, "worktree", "remove", "--force", "--", sourceRoot); err != nil {
		registered, inspectErr := reviewWorktreeRegistered(repository, sourceRoot)
		if inspectErr != nil || registered {
			return errors.Join(fmt.Errorf("remove exact review worktree: %w", err), inspectErr)
		}
		if removeErr := removeUnregisteredReviewPath(worktreeRoot, sourceRoot); removeErr != nil {
			return errors.Join(fmt.Errorf("remove unregistered exact review path after git failure: %w", err), removeErr)
		}
	}
	if _, err := os.Lstat(sourceRoot); err == nil || !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("exact review worktree path still exists after cleanup: %s", sourceRoot)
	}
	return nil
}

func reviewWorktreeRegistered(repository, sourceRoot string) (bool, error) {
	out, err := gitOutput(repository, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("inspect exact review worktree registration: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.TrimPrefix(line, "worktree ") == sourceRoot && strings.HasPrefix(line, "worktree ") {
			return true, nil
		}
	}
	return false, nil
}

func removeUnregisteredReviewPath(worktreeRoot, sourceRoot string) error {
	if err := validateReviewWorktreePath(worktreeRoot, sourceRoot); err != nil {
		return err
	}
	if err := os.RemoveAll(sourceRoot); err != nil {
		return fmt.Errorf("remove unregistered exact review path: %w", err)
	}
	return nil
}

func createReviewCommit(repoRoot, tree, parent, message string) (string, error) {
	args := []string{"commit-tree", tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	environment := reviewGitEnvironment()
	for key, value := range map[string]string{
		"GIT_AUTHOR_NAME": "ai-maintenance", "GIT_AUTHOR_EMAIL": "ai-maintenance@example.invalid",
		"GIT_COMMITTER_NAME": "ai-maintenance", "GIT_COMMITTER_EMAIL": "ai-maintenance@example.invalid",
		"GIT_AUTHOR_DATE": "2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE": "2000-01-01T00:00:00Z",
	} {
		environment = replaceEnvironment(environment, key, value)
	}
	out, err := gitOutputWithInput(repoRoot, environment, message+"\n", args...)
	if err != nil {
		return "", fmt.Errorf("create synthetic %s commit: %w", message, err)
	}
	commit := strings.TrimSpace(string(out))
	if !validGitOID(commit) {
		return "", fmt.Errorf("synthetic %s commit returned invalid OID %q", message, commit)
	}
	return commit, nil
}

func gitSingleLine(repoRoot string, environment []string, args ...string) (string, error) {
	out, err := gitOutput(repoRoot, environment, args...)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(out))
	if value == "" || strings.Contains(value, "\n") {
		return "", fmt.Errorf("git %s returned non-canonical single line %q", strings.Join(args, " "), value)
	}
	return value, nil
}

func gitOutput(repoRoot string, environment []string, args ...string) ([]byte, error) {
	return gitOutputWithInput(repoRoot, environment, "", args...)
}

func gitOutputWithInput(repoRoot string, environment []string, input string, args ...string) ([]byte, error) {
	commandArgs := []string{"--no-replace-objects"}
	if repoRoot != "" {
		commandArgs = append(commandArgs, "-C", repoRoot)
	}
	commandArgs = append(commandArgs, args...)
	command := exec.Command("git", commandArgs...)
	if environment == nil {
		environment = reviewGitEnvironment()
	}
	command.Env = environment
	if input != "" {
		command.Stdin = strings.NewReader(input)
	}
	out, err := command.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w\n%s", strings.Join(commandArgs, " "), err, out)
	}
	return out, nil
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, prefix+value)
}

func reviewGitEnvironment() []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if reviewGitLocalEnvironmentKey(key) {
			continue
		}
		environment = append(environment, entry)
	}
	return environment
}

func stagedReviewGitEnvironment() []string {
	environment := reviewGitEnvironment()
	if indexFile, ok := os.LookupEnv("GIT_INDEX_FILE"); ok && indexFile != "" {
		environment = replaceEnvironment(environment, "GIT_INDEX_FILE", indexFile)
	}
	return environment
}

func reviewGitLocalEnvironmentKey(key string) bool {
	switch key {
	case "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_COMMON_DIR", "GIT_CONFIG", "GIT_CONFIG_COUNT",
		"GIT_CONFIG_PARAMETERS", "GIT_DIR", "GIT_GRAFT_FILE", "GIT_IMPLICIT_WORK_TREE",
		"GIT_INDEX_FILE", "GIT_NAMESPACE", "GIT_NO_REPLACE_OBJECTS", "GIT_OBJECT_DIRECTORY",
		"GIT_PREFIX", "GIT_QUARANTINE_PATH", "GIT_REPLACE_REF_BASE", "GIT_SHALLOW_FILE", "GIT_WORK_TREE":
		return true
	default:
		return strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_")
	}
}
