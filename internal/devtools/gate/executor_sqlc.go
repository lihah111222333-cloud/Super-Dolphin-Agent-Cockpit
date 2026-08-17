package gate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sqlcconfig"
)

// runSQLCVerify 使用固定 sqlc 二进制生成两套代码并拒绝任何产物漂移。
func runSQLCVerify(
	ctx context.Context,
	gitBinary string,
	sqlcBinary string,
	gofmtBinary string,
	sourceCopy string,
	environment []string,
	stdout io.Writer,
	stderr io.Writer,
) (retErr error) {
	if gofmtBinary == "" {
		return errors.New("SQLC trusted gofmt binary is required")
	}
	sqlcEnvironment, err := sqlcEnvironmentWithTrustGuards(environment)
	if err != nil {
		return err
	}
	gitRunner, err := sqlcconfig.NewGitRunner(gitBinary)
	if err != nil {
		return fmt.Errorf("bind trusted SQLC Git runner: %w", err)
	}
	units, err := sqlcconfig.PreflightWithGit(ctx, sourceCopy, gitRunner)
	if err != nil {
		return fmt.Errorf("trusted SQLC preflight: %w", err)
	}
	snapshot, err := captureTrustedSQLCSnapshot(ctx, gitRunner, sourceCopy, units)
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, verifyTrustedSQLCOutputsSnapshot(ctx, sourceCopy, units, snapshot.source))
	}()
	return executeTrustedSQLCVerify(ctx, gitRunner, sqlcBinary, gofmtBinary, sourceCopy, sqlcEnvironment, units, snapshot.git, stdout, stderr)
}

func executeTrustedSQLCVerify(ctx context.Context, gitRunner sqlcconfig.GitRunner, sqlcBinary, gofmtBinary, sourceCopy string, environment []string, units []sqlcconfig.Unit, gitSnapshot trustedTreeManifest, stdout, stderr io.Writer) error {
	if err := prepareTrustedSQLCOutputs(ctx, sourceCopy, units); err != nil {
		return err
	}
	if err := runTrustedSQLCGeneration(ctx, sqlcBinary, sourceCopy, environment, stdout, stderr); err != nil {
		return err
	}
	if err := runTrustedSQLCPostprocess(ctx, gofmtBinary, sourceCopy, units, environment, stdout, stderr); err != nil {
		return err
	}
	if err := verifyTrustedGitSnapshot(ctx, gitRunner, sourceCopy, gitSnapshot); err != nil {
		return err
	}
	if err := sqlcconfig.VerifyGeneratedUnitsWithGit(ctx, sourceCopy, units, gitRunner); err != nil {
		return fmt.Errorf("trusted SQLC generated-output verification: %w", err)
	}
	return verifyTrustedSQLCWorktree(ctx, gitRunner, sourceCopy)
}

func runTrustedSQLCGeneration(
	ctx context.Context,
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
	return nil
}

func verifyTrustedSQLCWorktree(ctx context.Context, gitRunner sqlcconfig.GitRunner, sourceCopy string) error {
	status, err := gitRunner.Output(ctx, sourceCopy, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect sqlc generated state: %w", err)
	}
	if len(status) != 0 {
		return errors.New("sqlc generated output differs from the trusted snapshot: " + string(status))
	}
	return nil
}

func sqlcEnvironmentWithTrustGuards(environment []string) ([]string, error) {
	gitOptionalLocksSeen := false
	goWorkSeen := false
	for _, assignment := range environment {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || key == "" {
			return nil, errors.New("SQLC executor environment is malformed")
		}
		if err := validateSQLCTrustAssignment(key, value, &goWorkSeen, &gitOptionalLocksSeen); err != nil {
			return nil, err
		}
	}
	if !goWorkSeen {
		return nil, errors.New("SQLC executor environment requires canonical GOWORK=off")
	}
	guarded := append([]string(nil), environment...)
	if !gitOptionalLocksSeen {
		guarded = append(guarded, "GIT_OPTIONAL_LOCKS=0")
	}
	return guarded, nil
}

func validateSQLCTrustAssignment(key, value string, goWorkSeen, gitOptionalLocksSeen *bool) error {
	if strings.EqualFold(key, "GOWORK") {
		if *goWorkSeen || key != "GOWORK" || value != "off" {
			return errors.New("SQLC executor environment requires one canonical GOWORK=off")
		}
		*goWorkSeen = true
	}
	if strings.EqualFold(key, "GIT_OPTIONAL_LOCKS") {
		if *gitOptionalLocksSeen || key != "GIT_OPTIONAL_LOCKS" || value != "0" {
			return errors.New("SQLC executor environment requires one canonical GIT_OPTIONAL_LOCKS=0")
		}
		*gitOptionalLocksSeen = true
	}
	return nil
}
