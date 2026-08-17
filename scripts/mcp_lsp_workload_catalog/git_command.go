package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

const catalogGitTimeout = 30 * time.Second

// BoundedGitOutput executes one read-only Git query with a bounded process lifetime.
func BoundedGitOutput(repoRoot string, args ...string) ([]byte, error) {
	return boundedGitOutput(repoRoot, catalogGitTimeout, args...)
}

func boundedGitOutput(repoRoot string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command, err := gateprivate.TrustedGitCommand(ctx, repoRoot, args...)
	if err != nil {
		return nil, err
	}
	if err := gateprivate.ConfigureBoundedCommand(command); err != nil {
		return nil, err
	}
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("bounded Git query: %w", errors.Join(ctx.Err(), err))
		}
		return nil, err
	}
	return output, nil
}
