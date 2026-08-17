package gate

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

// ConfigureBoundedCommand applies the Gate process-group cancellation and WaitDelay owner.
func ConfigureBoundedCommand(command *exec.Cmd) error {
	if command == nil {
		return errors.New("bounded command is required")
	}
	return gateprivate.ConfigureBoundedCommand(command)
}

// BoundedGitOutput runs one read-only Git query under the Gate process-group watchdog.
func BoundedGitOutput(ctx context.Context, gitBinary, repositoryRoot string, args ...string) ([]byte, error) {
	return boundedGitOutput(ctx, gitBinary, repositoryRoot, false, args...)
}

// BoundedGitCombinedOutput preserves Git stderr while retaining bounded cancellation.
func BoundedGitCombinedOutput(ctx context.Context, gitBinary, repositoryRoot string, args ...string) ([]byte, error) {
	return boundedGitOutput(ctx, gitBinary, repositoryRoot, true, args...)
}

func boundedGitOutput(ctx context.Context, gitBinary, repositoryRoot string, combined bool, args ...string) ([]byte, error) {
	if ctx == nil || gitBinary == "" || repositoryRoot == "" {
		return nil, errors.New("bounded Git command inputs are required")
	}
	if !filepath.IsAbs(gitBinary) {
		return nil, errors.New("bounded Git binary must be absolute")
	}
	command := exec.CommandContext(ctx, gitBinary, args...)
	command.Dir = repositoryRoot
	command.Env = gateprivate.TrustedGitEnvironment()
	if err := ConfigureBoundedCommand(command); err != nil {
		return nil, err
	}
	var output []byte
	var err error
	if combined {
		output, err = command.CombinedOutput()
	} else {
		output, err = command.Output()
	}
	if err != nil && ctx.Err() != nil {
		return output, fmt.Errorf("bounded Git command: %w", errors.Join(ctx.Err(), err))
	}
	return output, err
}

// BoundedTrustedGitCombinedOutput resolves the sole trusted Git owner and runs
// one closed-environment query with bounded cancellation.
func BoundedTrustedGitCombinedOutput(ctx context.Context, repositoryRoot string, args ...string) ([]byte, error) {
	command, err := gateprivate.TrustedGitCommand(ctx, repositoryRoot, args...)
	if err != nil {
		return nil, err
	}
	if err := ConfigureBoundedCommand(command); err != nil {
		return nil, err
	}
	output, runErr := command.CombinedOutput()
	if runErr != nil && ctx.Err() != nil {
		return output, fmt.Errorf("bounded trusted Git command: %w", errors.Join(ctx.Err(), runErr))
	}
	return output, runErr
}
