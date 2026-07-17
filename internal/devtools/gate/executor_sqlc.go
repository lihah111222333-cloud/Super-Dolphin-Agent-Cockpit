package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// runSQLCVerify 使用固定 sqlc 二进制生成两套代码并拒绝任何产物漂移。
func runSQLCVerify(
	ctx context.Context,
	gitBinary string,
	sqlcBinary string,
	sourceCopy string,
	environment []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	steps := []resolvedStep{
		{directory: sourceCopy, binary: sqlcBinary, argv: []string{"sqlc", "generate"}},
		{directory: sourceCopy, binary: sqlcBinary, argv: []string{"sqlc", "generate", "-f", "cmd/mcp-orch/sqlc.yaml"}},
	}
	for _, step := range steps {
		if err := runResolvedStep(ctx, step, environment, stdout, stderr); err != nil {
			return fmt.Errorf("sqlc generation %q: %w", step.argv, err)
		}
	}
	status, err := gitOutput(
		ctx, gitBinary, sourceCopy, environment, nil, "status", "--porcelain=v1", "--untracked-files=all",
	)
	if err != nil {
		return fmt.Errorf("inspect sqlc generated state: %w", err)
	}
	if len(status) != 0 {
		return errors.New("sqlc generated output differs from the trusted snapshot: " + string(status))
	}
	return nil
}
