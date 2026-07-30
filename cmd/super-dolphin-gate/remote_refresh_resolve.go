package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// resolveRemoteBaselineRefreshInput 固化远端提交、树和运行时输入。
func resolveRemoteBaselineRefreshInput(ctx context.Context, options remoteBaselineRefreshOptions, config remoteRunConfig) (remoteBaselineRefreshInput, error) {
	repositoryRoot, err := resolveRemoteBaselineRepositoryRoot(options.RepositoryRoot)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	commit, err := resolveRemoteRef(repositoryRoot, options.Remote, options.Ref)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	if _, err := remoteGitOutput(repositoryRoot, "fetch", "--no-tags", "--quiet", options.Remote, commit); err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	return resolveRemoteBaselineIdentity(ctx, repositoryRoot, commit, options.Platform, config.Runtime.Image)
}

// resolveRemoteBaselineRepositoryRoot 返回规范化后的 Git 根目录。
func resolveRemoteBaselineRepositoryRoot(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// resolveRemoteBaselineIdentity 从固定提交生成基线身份与 Sqruff 工件输入。
func resolveRemoteBaselineIdentity(ctx context.Context, repositoryRoot, commit, platform, runtimeImage string) (remoteBaselineRefreshInput, error) {
	tree, treeSHA, err := loadRemoteBaselineGitTree(ctx, repositoryRoot, commit)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	registryDigest, err := gatecontract.GateRegistryDigest()
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	runtimeDependencyDigest, runtimeArgs, err := remoteci.ResolveRuntimeDependencyBuild(tree.Entries, platform)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	policyDigest := remoteBaselinePolicyDigest(registryDigest, runtimeDependencyDigest)
	imageInputs, err := localci.ResolveGateImageInputs(tree, policyDigest, platform)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	sqruffURL, sqruffSHA, err := resolveRemoteSqruffArtifact(runtimeArgs, platform)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	return remoteBaselineRefreshInput{
		Identity:                remoteci.BaselineIdentity{MainCommit: commit, MainTree: treeSHA, Platform: platform, PolicyDigest: policyDigest, ToolchainDigest: imageInputs.ToolchainDigest, RuntimeImage: runtimeImage},
		GateSourceDigest:        imageInputs.GateSourceDigest,
		RuntimeDependencyDigest: runtimeDependencyDigest,
		SqruffURL:               sqruffURL,
		SqruffSHA256:            sqruffSHA,
	}, nil
}

// bindAcceptedRemoteRuntimeDependency 从已验收提交重算运行时依赖摘要，使策略代码变化不会误触发 Anchor。
func bindAcceptedRemoteRuntimeDependency(ctx context.Context, repositoryRoot string, accepted remoteci.BaselineState, input remoteBaselineRefreshInput) (remoteBaselineRefreshInput, error) {
	if accepted.SchemaVersion == 0 {
		return input, nil
	}
	if accepted.MainCommit == input.Identity.MainCommit {
		input.AcceptedRuntimeDependencyDigest = input.RuntimeDependencyDigest
		return input, nil
	}
	repositoryRoot, err := resolveRemoteBaselineRepositoryRoot(repositoryRoot)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	tree, _, err := loadRemoteBaselineGitTree(ctx, repositoryRoot, accepted.MainCommit)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	digest, err := remoteci.ResolveAcceptedRuntimeDependencyDigest(tree.Entries, accepted.Platform)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	input.AcceptedRuntimeDependencyDigest = digest
	return input, nil
}

// loadRemoteBaselineGitTree 只从指定 commit 的 Git object tree 读取基线输入。
func loadRemoteBaselineGitTree(ctx context.Context, repositoryRoot, commit string) (localci.ReadOnlyGitTree, string, error) {
	treeSHA, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return localci.ReadOnlyGitTree{}, "", err
	}
	objectFormat, err := remoteGitOutput(repositoryRoot, "rev-parse", "--show-object-format")
	if err != nil {
		return localci.ReadOnlyGitTree{}, "", err
	}
	spec := gatecontract.SourceSpec{Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormat(objectFormat), SourceTreeSHA: treeSHA, Commit: &gatecontract.CommitSource{SHA: commit}}
	tree, err := localci.LoadReadOnlyGitTree(ctx, repositoryRoot, spec)
	if err != nil {
		return localci.ReadOnlyGitTree{}, "", err
	}
	return tree, treeSHA, nil
}

// acquireRemoteBaselineRefreshLock 在跨 worktree 的状态文件上获取互斥锁。
func acquireRemoteBaselineRefreshLock(ctx context.Context, statePath string) (*remoteBaselineRefreshLock, error) {
	if ctx == nil || strings.TrimSpace(statePath) == "" {
		return nil, errors.New("remote baseline refresh lock identity is incomplete")
	}
	file, err := os.OpenFile(statePath+".refresh.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open remote baseline refresh lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return waitRemoteBaselineRefreshLock(ctx, file)
}

// waitRemoteBaselineRefreshLock 轮询非阻塞 flock 并在上下文结束时关闭文件。
func waitRemoteBaselineRefreshLock(ctx context.Context, file *os.File) (*remoteBaselineRefreshLock, error) {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &remoteBaselineRefreshLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return nil, errors.Join(fmt.Errorf("lock remote baseline refresh: %w", err), file.Close())
		}
		timer := time.NewTimer(remoteBaselineLockPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-timer.C:
		}
	}
}
