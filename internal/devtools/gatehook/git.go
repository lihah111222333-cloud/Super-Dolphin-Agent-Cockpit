package gatehook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type gitRepository struct {
	identity RepositoryIdentity
}

type gitResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// CurrentWorktreeSource 从指定 cwd 固定完整 worktree tree，不读取其他 worktree 的 HEAD。
func CurrentWorktreeSource(ctx context.Context, cwd string) (RepositoryIdentity, gatecontract.SourceSpec, error) {
	repository, err := resolveGitRepository(ctx, cwd)
	if err != nil {
		return RepositoryIdentity{}, gatecontract.SourceSpec{}, err
	}
	treeSHA, parentSHA, err := repository.snapshotStableWorktreeTree(ctx)
	if err != nil {
		return RepositoryIdentity{}, gatecontract.SourceSpec{}, err
	}
	source := treeSource(repository.identity.ObjectFormat, treeSHA, parentSHA)
	if err := source.Validate(); err != nil {
		return RepositoryIdentity{}, gatecontract.SourceSpec{}, fmt.Errorf("validate worktree source: %w", err)
	}
	return repository.identity, source, nil
}

// resolveGitRepository 只按清理过 Git 环境的 cwd 解析活动 worktree。
func resolveGitRepository(ctx context.Context, cwd string) (gitRepository, error) {
	canonicalCWD, err := canonicalDirectory(cwd)
	if err != nil {
		return gitRepository{}, fmt.Errorf("resolve hook cwd: %w", err)
	}
	if err := requireInsideWorktree(ctx, canonicalCWD); err != nil {
		return gitRepository{}, err
	}
	identity, err := inspectRepositoryIdentity(ctx, canonicalCWD)
	if err != nil {
		return gitRepository{}, err
	}
	if err := verifyActiveWorktree(ctx, identity); err != nil {
		return gitRepository{}, err
	}
	return gitRepository{identity: identity}, nil
}

// requireInsideWorktree 拒绝 bare repo 与 cwd 外部解析。
func requireInsideWorktree(ctx context.Context, cwd string) error {
	inside, err := runGit(ctx, cwd, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf("hook cwd is not inside a Git worktree: %w", err)
	}
	if strings.TrimSpace(inside) != "true" {
		return errors.New("hook cwd is not inside a Git worktree")
	}
	return nil
}

// inspectRepositoryIdentity 读取 worktree、git dir、common dir 与对象格式闭包。
func inspectRepositoryIdentity(ctx context.Context, canonicalCWD string) (RepositoryIdentity, error) {
	worktreeRoot, err := gitAbsolutePath(ctx, canonicalCWD, "--show-toplevel")
	if err != nil {
		return RepositoryIdentity{}, err
	}
	if !pathWithin(canonicalCWD, worktreeRoot) {
		return RepositoryIdentity{}, errors.New("hook cwd does not belong to resolved worktree root")
	}
	gitDir, err := gitAbsolutePath(ctx, worktreeRoot, "--git-dir")
	if err != nil {
		return RepositoryIdentity{}, err
	}
	commonDir, err := gitAbsolutePath(ctx, worktreeRoot, "--git-common-dir")
	if err != nil {
		return RepositoryIdentity{}, err
	}
	objectFormatText, err := runGit(ctx, worktreeRoot, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return RepositoryIdentity{}, err
	}
	identity := RepositoryIdentity{
		WorktreeRoot: worktreeRoot,
		GitDir:       gitDir,
		CommonDir:    commonDir,
		ObjectFormat: gatecontract.GitObjectFormat(strings.TrimSpace(objectFormatText)),
	}
	if err := identity.Validate(); err != nil {
		return RepositoryIdentity{}, err
	}
	return identity, nil
}

func gitAbsolutePath(ctx context.Context, cwd, selector string) (string, error) {
	value, err := runGit(ctx, cwd, nil, "rev-parse", "--path-format=absolute", selector)
	if err != nil {
		return "", err
	}
	path, err := canonicalDirectory(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("canonicalize Git %s: %w", selector, err)
	}
	return path, nil
}

// verifyActiveWorktree 要求 canonical root 在 Git worktree inventory 中恰好出现一次。
func verifyActiveWorktree(ctx context.Context, identity RepositoryIdentity) error {
	output, err := runGit(ctx, identity.WorktreeRoot, nil, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	count, err := countTargetWorktree(output, identity.WorktreeRoot)
	if err != nil {
		return err
	}
	return validateTargetWorktreeCount(count, identity.WorktreeRoot)
}

// validateTargetWorktreeCount 拒绝目标 worktree 缺失或重复。
func validateTargetWorktreeCount(count int, targetRoot string) error {
	if count == 0 {
		return fmt.Errorf("resolved worktree %q is not active", targetRoot)
	}
	if count > 1 {
		return errors.New("active worktree appears more than once in Git worktree list")
	}
	return nil
}

// countTargetWorktree 解析换行 porcelain，并严格解码 Git 引用的绝对路径。
func countTargetWorktree(output, targetRoot string) (int, error) {
	if output == "" || output[len(output)-1] != '\n' {
		return 0, errors.New("Git worktree porcelain output is not newline terminated")
	}
	count := 0
	for line := range strings.SplitSeq(output, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		candidate, err := decodeWorktreePath(strings.TrimPrefix(line, "worktree "))
		if err != nil {
			return 0, err
		}
		if candidate == targetRoot {
			count++
		}
	}
	return count, nil
}

func decodeWorktreePath(candidate string) (string, error) {
	if strings.HasPrefix(candidate, "\"") {
		decoded, err := strconv.Unquote(candidate)
		if err != nil {
			return "", fmt.Errorf("decode Git worktree path: %w", err)
		}
		candidate = decoded
	}
	if candidate == "" || !filepath.IsAbs(candidate) {
		return "", errors.New("Git worktree porcelain contains an invalid worktree path")
	}
	return filepath.Clean(candidate), nil
}

func (r gitRepository) headCommit(ctx context.Context) (string, error) {
	result, err := runGitRaw(ctx, r.identity.WorktreeRoot, nil, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	switch result.exitCode {
	case 0:
		sha := strings.TrimSpace(result.stdout)
		if err := r.verifyObject(ctx, sha, "commit"); err != nil {
			return "", err
		}
		return sha, nil
	case 1:
		return "", nil
	default:
		return "", gitExitError([]string{"rev-parse", "--verify", "--quiet", "HEAD^{commit}"}, result)
	}
}

func (r gitRepository) snapshotStableWorktreeTree(ctx context.Context) (string, string, error) {
	parentSHA, err := r.headCommit(ctx)
	if err != nil {
		return "", "", err
	}
	first, err := r.snapshotWorktreeTree(ctx, parentSHA)
	if err != nil {
		return "", "", err
	}
	second, err := r.snapshotWorktreeTree(ctx, parentSHA)
	if err != nil {
		return "", "", err
	}
	if first != second {
		return "", "", fmt.Errorf("worktree tree changed while taking hook snapshot: %s -> %s", first, second)
	}
	return first, parentSHA, nil
}

// snapshotWorktreeTree 使用隔离临时 index 捕获完整 tracked/untracked worktree tree。
func (r gitRepository) snapshotWorktreeTree(ctx context.Context, parentSHA string) (string, error) {
	tempDir, err := os.MkdirTemp("", "super-dolphin-gatehook-index-")
	if err != nil {
		return "", fmt.Errorf("create temporary Git index directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	indexPath := filepath.Join(tempDir, "index")
	environment := []string{"GIT_INDEX_FILE=" + indexPath}
	if parentSHA == "" {
		if _, err := runGit(ctx, r.identity.WorktreeRoot, environment, "read-tree", "--empty"); err != nil {
			return "", err
		}
	} else if _, err := runGit(ctx, r.identity.WorktreeRoot, environment, "read-tree", parentSHA); err != nil {
		return "", err
	}
	if _, err := runGit(ctx, r.identity.WorktreeRoot, environment, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	treeSHA, err := runGit(ctx, r.identity.WorktreeRoot, environment, "write-tree")
	if err != nil {
		return "", err
	}
	treeSHA = strings.TrimSpace(treeSHA)
	if err := r.verifyObject(ctx, treeSHA, "tree"); err != nil {
		return "", err
	}
	return treeSHA, nil
}

func (r gitRepository) verifyObject(ctx context.Context, oid, expectedType string) error {
	objectType, err := runGit(ctx, r.identity.WorktreeRoot, nil, "cat-file", "-t", oid)
	if err != nil {
		return fmt.Errorf("verify Git object %s in active worktree repository: %w", oid, err)
	}
	if strings.TrimSpace(objectType) != expectedType {
		return fmt.Errorf("Git object %s has type %q, want %q", oid, strings.TrimSpace(objectType), expectedType)
	}
	return nil
}

func (r gitRepository) resolveCommit(ctx context.Context, revision string) (string, error) {
	sha, err := runGit(ctx, r.identity.WorktreeRoot, nil, "rev-parse", "--verify", revision+"^{commit}")
	if err != nil {
		return "", err
	}
	sha = strings.TrimSpace(sha)
	if err := r.verifyObject(ctx, sha, "commit"); err != nil {
		return "", err
	}
	return sha, nil
}

func (r gitRepository) commitTree(ctx context.Context, commitSHA string) (string, error) {
	treeSHA, err := runGit(ctx, r.identity.WorktreeRoot, nil, "rev-parse", "--verify", commitSHA+"^{tree}")
	if err != nil {
		return "", err
	}
	treeSHA = strings.TrimSpace(treeSHA)
	if err := r.verifyObject(ctx, treeSHA, "tree"); err != nil {
		return "", err
	}
	return treeSHA, nil
}

func (r gitRepository) isAncestor(ctx context.Context, baseSHA, headSHA string) (bool, error) {
	result, err := runGitRaw(ctx, r.identity.WorktreeRoot, nil, "merge-base", "--is-ancestor", baseSHA, headSHA)
	if err != nil {
		return false, err
	}
	switch result.exitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, gitExitError([]string{"merge-base", "--is-ancestor", baseSHA, headSHA}, result)
	}
}

func treeSource(objectFormat gatecontract.GitObjectFormat, treeSHA, parentSHA string) gatecontract.SourceSpec {
	return gatecontract.SourceSpec{
		Kind:          gatecontract.SourceKindTree,
		ObjectFormat:  objectFormat,
		Tree:          &gatecontract.TreeSource{SHA: treeSHA, ParentCommitSHA: parentSHA},
		SourceTreeSHA: treeSHA,
	}
}

// canonicalDirectory 将现存目录收敛为去除符号链接的绝对路径。
func canonicalDirectory(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", canonical)
	}
	return filepath.Clean(canonical), nil
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func runGit(ctx context.Context, cwd string, extraEnvironment []string, arguments ...string) (string, error) {
	result, err := runGitRaw(ctx, cwd, extraEnvironment, arguments...)
	if err != nil {
		return "", err
	}
	if result.exitCode != 0 {
		return "", gitExitError(arguments, result)
	}
	return result.stdout, nil
}

func runGitRaw(ctx context.Context, cwd string, extraEnvironment []string, arguments ...string) (gitResult, error) {
	commandArguments := append([]string{"-C", cwd}, arguments...)
	command := exec.CommandContext(ctx, "git", commandArguments...)
	command.Env = sanitizedGitEnvironment(extraEnvironment)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := gitResult{stdout: stdout.String(), stderr: strings.TrimSpace(stderr.String())}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.exitCode = exitError.ExitCode()
		return result, nil
	}
	return gitResult{}, fmt.Errorf("start git %s: %w", strings.Join(arguments, " "), err)
}

func sanitizedGitEnvironment(extraEnvironment []string) []string {
	environment := make([]string, 0, len(os.Environ())+len(extraEnvironment))
	for _, entry := range os.Environ() {
		name, _, found := strings.Cut(entry, "=")
		if found && strings.HasPrefix(name, "GIT_") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, extraEnvironment...)
}

func gitExitError(arguments []string, result gitResult) error {
	detail := result.stderr
	if detail == "" {
		detail = "no stderr"
	}
	return fmt.Errorf("git %s exited %d: %s", strings.Join(arguments, " "), result.exitCode, detail)
}

func sha256Identity(parts ...string) string {
	framed := make([]byte, 0)
	for _, part := range parts {
		framed = binary.AppendUvarint(framed, uint64(len(part)))
		framed = append(framed, part...)
	}
	digest := sha256.Sum256(framed)
	return "sha256:" + hex.EncodeToString(digest[:])
}
