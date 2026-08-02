package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/sourceexport"
)

// resolveRemoteBaselineRefreshInput 固化远端提交、树和运行时输入。
func resolveRemoteBaselineRefreshInput(ctx context.Context, options remoteBaselineRefreshOptions) (remoteBaselineRefreshInput, error) {
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
	input, err := resolveRemoteBaselineIdentity(ctx, repositoryRoot, commit, options.Platform)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	input.RepositoryRoot = repositoryRoot
	return input, nil
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
func resolveRemoteBaselineIdentity(ctx context.Context, repositoryRoot, commit, platform string) (remoteBaselineRefreshInput, error) {
	tree, treeSHA, err := loadRemoteBaselineGitTree(ctx, repositoryRoot, commit)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	registryDigest, err := gatecontract.GateRegistryDigest()
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	runtimeDependencyDigest, _, err := remoteci.ResolveRuntimeDependencyBuild(tree.Entries, platform)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	goToolchain, err := resolveRemoteBaselineGoToolchain(tree.Entries)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	policyDigest := remoteBaselinePolicyDigest(registryDigest, runtimeDependencyDigest)
	compileInputs, err := remoteci.ResolveBaselineGateCompileInputs(tree, platform)
	if err != nil {
		return remoteBaselineRefreshInput{}, err
	}
	toolchainDigest := remoteBaselineToolchainDigest(compileInputs.ToolchainDigest, goToolchain)
	return remoteBaselineRefreshInput{
		Identity:                       remoteci.BaselineIdentity{MainCommit: commit, MainTree: treeSHA, Platform: platform, PolicyDigest: policyDigest, ToolchainDigest: toolchainDigest},
		GateSourceDigest:               compileInputs.GateSourceDigest,
		RuntimeDependencyDigest:        runtimeDependencyDigest,
		RuntimeDependencySchemaVersion: remoteci.RuntimeDependencySchemaVersion,
		GoToolchain:                    goToolchain,
		SourceEntries:                  append([]sourceexport.TreeEntry(nil), tree.Entries...),
	}, nil
}

func remoteBaselinePolicyDigest(registryDigest, runtimeDependencyDigest string) string {
	payload := "super-dolphin.remote-oci-baseline-policy.v1\x00" + registryDigest + "\x00" + runtimeDependencyDigest
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(payload)))
}

func resolveRemoteRef(repositoryRoot, remote, ref string) (string, error) {
	branch := strings.TrimPrefix(ref, "refs/heads/")
	return remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", remote+"/"+branch+"^{commit}")
}

// remoteBaselineToolchainDigest 将镜像工具链锁与项目 Go 版本绑定为同一基准身份。
func remoteBaselineToolchainDigest(lockDigest, goToolchain string) string {
	payload := "super-dolphin.remote-baseline-toolchain.v1\x00" + lockDigest + "\x00" + goToolchain
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(payload)))
}

// resolveRemoteBaselineGoToolchain 仅以仓库根模块选择生产测试工具链。
func resolveRemoteBaselineGoToolchain(entries []sourceexport.TreeEntry) (string, error) {
	toolchain, err := remoteci.ResolveGoToolchain(entries)
	if err != nil {
		return "", fmt.Errorf("resolve remote baseline Go toolchain: %w", err)
	}
	if err := cicontract.ValidateGoToolchainVersion(toolchain); err != nil {
		return "", err
	}
	return toolchain, nil
}

// loadRemoteBaselineGitTree 只从指定 commit 的 Git object tree 读取基线输入。
func loadRemoteBaselineGitTree(ctx context.Context, repositoryRoot, commit string) (remoteci.ReadOnlyGitTree, string, error) {
	treeSHA, err := remoteGitOutput(repositoryRoot, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}")
	if err != nil {
		return remoteci.ReadOnlyGitTree{}, "", err
	}
	objectFormat, err := remoteGitOutput(repositoryRoot, "rev-parse", "--show-object-format")
	if err != nil {
		return remoteci.ReadOnlyGitTree{}, "", err
	}
	spec := gatecontract.SourceSpec{Kind: gatecontract.SourceKindCommit, ObjectFormat: gatecontract.GitObjectFormat(objectFormat), SourceTreeSHA: treeSHA, Commit: &gatecontract.CommitSource{SHA: commit}}
	tree, err := remoteci.LoadReadOnlyGitTree(ctx, repositoryRoot, spec)
	if err != nil {
		return remoteci.ReadOnlyGitTree{}, "", err
	}
	return tree, treeSHA, nil
}
