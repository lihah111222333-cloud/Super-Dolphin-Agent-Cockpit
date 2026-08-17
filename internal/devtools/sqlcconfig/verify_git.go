package sqlcconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

func verifyGeneratedGitState(ctx context.Context, repoRoot, file string, git GitRunner, mode generatedVerificationMode) error {
	switch mode {
	case generatedVerificationShape:
		return verifyWorktreeGitVisiblePath(ctx, repoRoot, file, git)
	case generatedVerificationExact:
		return verifyGitVisiblePath(ctx, repoRoot, file, git)
	default:
		return errors.New("unknown generated SQLC verification mode")
	}
}

// verifyWorktreeGitVisiblePath 保留 ignore、可见性和 index flag 门禁，同时允许普通 worktree 漂移。
func verifyWorktreeGitVisiblePath(ctx context.Context, repoRoot, relative string, git GitRunner) error {
	ignored, err := gitPathIgnored(ctx, repoRoot, relative, git)
	if err != nil {
		return err
	}
	if ignored {
		return fmt.Errorf("generated SQLC file %s is Git-ignored", relative)
	}
	tracked, err := gitPathTracked(ctx, repoRoot, relative, git)
	if err != nil {
		return err
	}
	if tracked {
		return verifyTrackedGitIndexFlags(ctx, repoRoot, relative, git)
	}
	visible, err := gitPathStatus(ctx, repoRoot, relative, git)
	if err != nil {
		return err
	}
	if !visible {
		return fmt.Errorf("generated SQLC file %s is neither tracked nor visible to Git status", relative)
	}
	return nil
}

// verifyGitVisiblePath 拒绝被 ignore 的产物，同时允许未 tracked 但会被 git status 看见的产物。
func verifyGitVisiblePath(ctx context.Context, repoRoot, relative string, git GitRunner) error {
	ignored, err := gitPathIgnored(ctx, repoRoot, relative, git)
	if err != nil {
		return err
	}
	if ignored {
		return fmt.Errorf("generated SQLC file %s is Git-ignored", relative)
	}
	tracked, err := gitPathTracked(ctx, repoRoot, relative, git)
	if err != nil {
		return err
	}
	if tracked {
		return verifyTrackedGitPath(ctx, repoRoot, relative, git)
	}
	visible, err := gitPathStatus(ctx, repoRoot, relative, git)
	if err != nil {
		return err
	}
	if !visible {
		return fmt.Errorf("generated SQLC file %s is neither tracked nor visible to Git status", relative)
	}
	return fmt.Errorf("generated SQLC file %s is visible but not tracked", relative)
}

// gitPathIgnored 返回 Git 是否将仓库相对路径视为 ignored，并对其他退出码 fail-closed。
func gitPathIgnored(ctx context.Context, repoRoot, relative string, git GitRunner) (bool, error) {
	err := git.run(ctx, repoRoot, "check-ignore", "--no-index", "--quiet", "--", relative)
	if err == nil {
		return true, nil
	}
	if gitExitCode(err, 1) {
		return false, nil
	}
	return false, fmt.Errorf("check Git ignore status for SQLC file %s: %w", relative, err)
}

// gitPathTracked 返回 Git index 是否已经记录仓库相对路径。
func gitPathTracked(ctx context.Context, repoRoot, relative string, git GitRunner) (bool, error) {
	err := git.run(ctx, repoRoot, "ls-files", "--error-unmatch", "--", relative)
	if err == nil {
		return true, nil
	}
	if gitExitCode(err, 1) {
		return false, nil
	}
	return false, fmt.Errorf("inspect Git tracked state for SQLC file %s: %w", relative, err)
}

// gitPathStatus 返回 git status 是否能看见未 tracked 或已修改的仓库相对路径。
func gitPathStatus(ctx context.Context, repoRoot, relative string, git GitRunner) (bool, error) {
	output, err := git.output(ctx, repoRoot, "status", "--porcelain=v1", "--untracked-files=all", "--", relative)
	if err != nil {
		return false, fmt.Errorf("inspect Git status for SQLC file %s: %w", relative, err)
	}
	return len(bytes.TrimSpace(output)) != 0, nil
}

func verifyTrackedGitPath(ctx context.Context, repoRoot, relative string, git GitRunner) error {
	if err := verifyTrackedGitIndexFlags(ctx, repoRoot, relative, git); err != nil {
		return err
	}
	if err := git.run(ctx, repoRoot, "diff", "--quiet", "--", relative); err != nil {
		return fmt.Errorf("generated SQLC file %s differs between index and worktree: %w", relative, err)
	}
	if err := git.run(ctx, repoRoot, "diff", "--cached", "--quiet", "HEAD", "--", relative); err != nil {
		return fmt.Errorf("generated SQLC file %s differs between HEAD and index: %w", relative, err)
	}
	return nil
}

// verifyTrackedGitIndexFlags 拒绝 assume-unchanged 与 skip-worktree 等隐藏产物漂移的 index 标志。
func verifyTrackedGitIndexFlags(ctx context.Context, repoRoot, relative string, git GitRunner) error {
	flags, err := git.output(ctx, repoRoot, "ls-files", "-v", "--", relative)
	if err != nil {
		return fmt.Errorf("inspect Git index flags for SQLC file %s: %w", relative, err)
	}
	return validateTrackedGitIndexFlags(flags, relative)
}

func validateTrackedGitIndexFlags(flags []byte, relative string) error {
	want := []byte(relative)
	count := 0
	for line := range bytes.Lines(flags) {
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(line) == 0 {
			continue
		}
		count++
		if len(line) < 3 || line[0] != 'H' || line[1] != ' ' || !bytes.Equal(line[2:], want) {
			return fmt.Errorf("generated SQLC file %s carries forbidden Git index flags or non-canonical record %q", relative, line)
		}
	}
	if count != 1 {
		return fmt.Errorf("generated SQLC file %s has %d Git index records, want exactly one", relative, count)
	}
	return nil
}
