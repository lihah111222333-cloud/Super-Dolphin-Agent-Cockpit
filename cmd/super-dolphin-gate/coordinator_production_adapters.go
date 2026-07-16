package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

type truthImageEnsureService interface {
	EnsureImage(context.Context, localci.TruthImageEnsureRequest) (localci.TruthImageEnsureResult, error)
}

type dockerFreshContainerService interface {
	RunFreshContainer(context.Context, localci.FreshContainerRequest) (localci.FreshContainerResult, error)
	RecoverFreshContainer(context.Context, localci.FreshContainerRecoveryRequest) (localci.FreshContainerResult, error)
	ProbeFreshContainerRecovery(context.Context, localci.FreshContainerRecoveryRequest) (localci.FreshContainerRecoveryObservation, error)
	CleanupUnprovedFreshContainer(context.Context, localci.FreshContainerCleanupRequest) (localci.FreshContainerResult, error)
}

type productionImageEnsurer struct {
	truth    truthImageEnsureService
	platform string
}

// EnsureImage 从提交的 Git object tree 解析镜像输入，并且只映射 accepted runnable identity。
func (ensurer *productionImageEnsurer) EnsureImage(
	ctx context.Context,
	request imageEnsureRequest,
) (ensuredImage, error) {
	if ensurer == nil || ensurer.truth == nil || ensurer.platform == "" {
		return ensuredImage{}, errors.New("production image ensurer is not configured")
	}
	tree, err := localci.LoadReadOnlyGitTree(ctx, request.RepositoryRoot, request.Plan.Source)
	if err != nil {
		return ensuredImage{}, fmt.Errorf("load submitted image input tree: %w", err)
	}
	if tree.Source.SourceTreeSHA != request.JobSourceTreeSHA {
		return ensuredImage{}, errors.New("submitted image input tree does not match job source tree")
	}
	result, err := ensurer.truth.EnsureImage(ctx, localci.TruthImageEnsureRequest{
		Tree: tree, PolicyDigest: request.Plan.PolicyDigest, Platform: ensurer.platform,
	})
	if err != nil {
		return ensuredImage{}, err
	}
	if result.Status != localci.TruthImageEnsureAccepted || result.SubmittedJobSourceTree != request.JobSourceTreeSHA {
		return ensuredImage{}, errors.New("truth image ensurer did not return an accepted image for the submitted tree")
	}
	return ensuredImage{
		Identity: result.Image,
		Truth: localci.FreshContainerImageTruth{
			PolicyDigest: result.PolicyDigest, BuildSourceTreeSHA: result.AcceptedImageBuildSourceTree,
			InputDigest: result.ImageInputDigest, ToolchainDigest: result.ToolchainDigest,
			SchemaVersion: result.ImageSchemaVersion,
		},
		ImageProvenanceSourceTreeSHA: result.AcceptedImageBuildSourceTree,
	}, nil
}

type productionSourceMaterializer struct {
	gitPath string
}

// Materialize 把 SourceSpec 封装为 bundle 后检出到一次性私有快照。
func (materializer *productionSourceMaterializer) Materialize(
	ctx context.Context,
	request sourceMaterializeRequest,
) (result materializedJobSource, retErr error) {
	if materializer == nil || materializer.gitPath == "" || ctx == nil {
		return materializedJobSource{}, errors.New("production source materializer is not configured")
	}
	if err := os.Mkdir(request.OutputRoot, 0o700); err != nil {
		return materializedJobSource{}, fmt.Errorf("create source output root: %w", err)
	}
	defer func() {
		if retErr != nil {
			retErr = errors.Join(retErr, os.RemoveAll(request.OutputRoot))
		}
	}()
	materialized, err := localci.MaterializeSource(ctx, request.RepositoryRoot, request.Source, request.OutputRoot)
	if err != nil {
		return materializedJobSource{}, err
	}
	snapshotDir, err := materializer.checkoutSnapshot(ctx, request.OutputRoot, materialized)
	if err != nil {
		return materializedJobSource{}, err
	}
	return materializedJobSource{
		SnapshotDir: snapshotDir, SourceTreeSHA: materialized.Manifest.SourceTreeSHA,
		Cleanup: func() error { return os.RemoveAll(request.OutputRoot) },
	}, nil
}

// checkoutSnapshot 从自包含 bundle 导入固定 refs 并检出 materialized commit。
func (materializer *productionSourceMaterializer) checkoutSnapshot(
	ctx context.Context,
	outputRoot string,
	materialized localci.SourceMaterialization,
) (string, error) {
	snapshotDir := filepath.Join(outputRoot, "snapshot")
	if err := os.Mkdir(snapshotDir, 0o700); err != nil {
		return "", fmt.Errorf("create source snapshot: %w", err)
	}
	if err := materializer.git(ctx, outputRoot, "init", "-q", "--object-format="+string(materialized.Manifest.ObjectFormat), "--", snapshotDir); err != nil {
		return "", err
	}
	if err := materializer.git(
		ctx, snapshotDir, "fetch", "-q", "--no-tags", "--no-write-fetch-head", "--",
		materialized.BundlePath, "refs/source/*:refs/source/*",
	); err != nil {
		return "", err
	}
	if err := materializer.verifySnapshotIdentity(ctx, snapshotDir, materialized.Manifest); err != nil {
		return "", err
	}
	if err := materializer.git(ctx, snapshotDir, "checkout", "-q", "--detach", materialized.Manifest.MaterializedCommitSHA); err != nil {
		return "", err
	}
	return snapshotDir, nil
}

func (materializer *productionSourceMaterializer) verifySnapshotIdentity(
	ctx context.Context,
	snapshotDir string,
	manifest localci.SourceMaterializationManifest,
) error {
	commit, err := materializer.gitLine(
		ctx, snapshotDir, "rev-parse", "--verify", "--end-of-options", "refs/source/materialized^{commit}",
	)
	if err != nil {
		return err
	}
	if commit != manifest.MaterializedCommitSHA {
		return errors.New("materialized source snapshot commit mismatch")
	}
	tree, err := materializer.gitLine(ctx, snapshotDir, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return err
	}
	if tree != manifest.SourceTreeSHA {
		return errors.New("materialized source snapshot tree mismatch")
	}
	return nil
}

func (materializer *productionSourceMaterializer) git(ctx context.Context, directory string, args ...string) error {
	output, err := materializer.gitCommand(ctx, directory, args...).CombinedOutput()
	if err != nil || len(output) != 0 {
		return errors.Join(
			fmt.Errorf("materialize source Git %s: %s", args[0], strings.TrimSpace(string(output))),
			err,
		)
	}
	return nil
}

func (materializer *productionSourceMaterializer) gitLine(
	ctx context.Context,
	directory string,
	args ...string,
) (string, error) {
	output, err := materializer.gitCommand(ctx, directory, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("materialize source Git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	line := strings.TrimSuffix(string(output), "\n")
	if line == "" || strings.TrimSpace(line) != line || strings.ContainsAny(line, "\r\n\x00") {
		return "", fmt.Errorf("materialize source Git %s returned non-canonical output", args[0])
	}
	return line, nil
}

func (materializer *productionSourceMaterializer) gitCommand(
	ctx context.Context,
	directory string,
	args ...string,
) *exec.Cmd {
	commandArgs := append([]string{"-C", directory}, args...)
	command := exec.CommandContext(ctx, materializer.gitPath, commandArgs...)
	command.Env = []string{
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C", "PATH=" + os.Getenv("PATH"),
	}
	return command
}

type productionFreshContainerRunner struct {
	runner dockerFreshContainerService
}

// RunFreshContainer 校验 accepted provenance 后转交权威 Docker 一次性容器 runner。
func (runner *productionFreshContainerRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerResult{}, errors.New("production fresh container runner is not configured")
	}
	if request.ImageProvenanceSourceTreeSHA == "" ||
		request.ImageProvenanceSourceTreeSHA != request.ImageTruth.BuildSourceTreeSHA {
		return localci.FreshContainerResult{}, errors.New("accepted image provenance tree does not match runner image truth")
	}
	return runner.runner.RunFreshContainer(ctx, localci.FreshContainerRequest{
		Image: request.Image, ImageTruth: request.ImageTruth,
		SourceTreeSHA: request.JobSourceTreeSHA, SourceSnapshotDir: request.SourceSnapshotDir,
		Profile: request.Profile, Plan: request.Plan, GateID: request.GateID,
		ContainerLabels: request.ContainerLabels, Deadline: request.Deadline,
		LifecycleHook: request.LifecycleHook,
	})
}

// RecoverFreshContainer 将已证明身份的容器交给 Docker runner 接续观察。
func (runner *productionFreshContainerRunner) RecoverFreshContainer(
	ctx context.Context,
	request localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerResult, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerResult{}, errors.New("production fresh container runner is not configured")
	}
	return runner.runner.RecoverFreshContainer(ctx, request)
}

// ProbeFreshContainerRecovery 在 owner reconcile 阶段只读取原容器状态。
func (runner *productionFreshContainerRunner) ProbeFreshContainerRecovery(
	ctx context.Context,
	request localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerRecoveryObservation, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerRecoveryObservation{}, errors.New("production fresh container runner is not configured")
	}
	return runner.runner.ProbeFreshContainerRecovery(ctx, request)
}

// CleanupUnprovedFreshContainer 清理无法证明同一执行的旧容器。
func (runner *productionFreshContainerRunner) CleanupUnprovedFreshContainer(
	ctx context.Context,
	request localci.FreshContainerCleanupRequest,
) (localci.FreshContainerResult, error) {
	if runner == nil || runner.runner == nil {
		return localci.FreshContainerResult{}, errors.New("production fresh container runner is not configured")
	}
	return runner.runner.CleanupUnprovedFreshContainer(ctx, request)
}
