package sqlcconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/trustedgitpolicy"
)

// GitRunner executes SQLC ownership checks through one validated Git binary.
type GitRunner struct {
	binary string
}

// NewGitRunner binds SQLC Git semantics to an explicit absolute executable.
func NewGitRunner(binary string) (GitRunner, error) {
	if !filepath.IsAbs(binary) {
		return GitRunner{}, errors.New("SQLC Git binary must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return GitRunner{}, fmt.Errorf("resolve SQLC Git binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return GitRunner{}, fmt.Errorf("inspect SQLC Git binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return GitRunner{}, fmt.Errorf("SQLC Git binary is not an executable regular file: %s", resolved)
	}
	return GitRunner{binary: resolved}, nil
}

// ResolveExternalGit selects a fixed host Git outside the candidate repository.
func ResolveExternalGit(repoRoot string) (GitRunner, error) {
	canonicalRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return GitRunner{}, fmt.Errorf("resolve SQLC repository root: %w", err)
	}
	canonicalRoot, err = filepath.EvalSymlinks(canonicalRoot)
	if err != nil {
		return GitRunner{}, fmt.Errorf("canonicalize SQLC repository root: %w", err)
	}
	for _, candidate := range []string{"/usr/bin/git", "/usr/local/bin/git", "/opt/homebrew/bin/git"} {
		runner, runnerErr := NewGitRunner(candidate)
		if runnerErr != nil {
			continue
		}
		inside, insideErr := pathWithin(canonicalRoot, runner.binary)
		if insideErr != nil {
			return GitRunner{}, insideErr
		}
		if !inside {
			return runner, nil
		}
	}
	return GitRunner{}, errors.New("trusted external Git executable is unavailable")
}

func pathWithin(root, candidate string) (bool, error) {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false, fmt.Errorf("compare trusted tool path: %w", err)
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)), nil
}

func (runner GitRunner) command(ctx context.Context, repoRoot string, args ...string) (*exec.Cmd, error) {
	if runner.binary == "" {
		return nil, errors.New("SQLC Git runner is uninitialized")
	}
	if ctx == nil {
		return nil, errors.New("SQLC Git context is required")
	}
	commandArgs := append([]string{"--no-replace-objects", "-C", repoRoot}, args...)
	command := exec.CommandContext(ctx, runner.binary, commandArgs...)
	command.Env = trustedGitEnvironment()
	return command, nil
}

func trustedGitEnvironment() []string {
	return trustedgitpolicy.Environment()
}

func (runner GitRunner) run(ctx context.Context, repoRoot string, args ...string) error {
	command, err := runner.command(ctx, repoRoot, args...)
	if err != nil {
		return err
	}
	configureGitCommandCancellation(command)
	runErr := command.Run()
	cleanupErr := cleanupGitCommand(command)
	return gitCommandError(ctx, errors.Join(runErr, cleanupErr))
}

func (runner GitRunner) output(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	return runner.Output(ctx, repoRoot, args...)
}

// Output executes one trusted Git query with an explicit closed environment.
func (runner GitRunner) Output(ctx context.Context, repoRoot string, args ...string) ([]byte, error) {
	command, err := runner.command(ctx, repoRoot, args...)
	if err != nil {
		return nil, err
	}
	configureGitCommandCancellation(command)
	output, outputErr := command.Output()
	cleanupErr := cleanupGitCommand(command)
	return output, gitCommandError(ctx, errors.Join(outputErr, cleanupErr))
}

func gitCommandError(ctx context.Context, commandErr error) error {
	if commandErr == nil || ctx.Err() == nil {
		return commandErr
	}
	return errors.Join(ctx.Err(), context.Cause(ctx), commandErr)
}

func gitExitCode(err error, code int) bool {
	var exitError *exec.ExitError
	return errors.As(err, &exitError) && exitError.ExitCode() == code
}
