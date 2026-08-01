package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
)

type productionSelfUpdateHead struct {
	commit string
	tree   string
}

// resolveProductionGitExecutable 选择不受调用者 PATH 顺序影响的 root-owned 系统 Git。
func resolveProductionGitExecutable() (string, error) {
	candidates := make([]string, 0, 4)
	if configured := os.Getenv("SUPER_DOLPHIN_GATE_GIT"); configured != "" {
		candidates = append(candidates, configured)
	}
	candidates = append(candidates, "/usr/bin/git", "/bin/git")
	if discovered, err := exec.LookPath("git"); err == nil {
		candidates = append(candidates, discovered)
	}
	seen := make(map[string]struct{}, len(candidates))
	candidateErrors := make([]error, 0, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if canonical, err := canonicalProductionGitExecutable(candidate); err == nil {
			return canonical, nil
		} else {
			candidateErrors = append(candidateErrors, fmt.Errorf("Git candidate %q: %w", candidate, err))
		}
	}
	return "", errors.Join(
		errors.New("no root-owned system Git executable is available"),
		errors.Join(candidateErrors...),
	)
}

// canonicalProductionGitExecutable 拒绝用户可替换的 Git 文件与路径祖先。
func canonicalProductionGitExecutable(path string) (string, error) {
	return gateprivate.CanonicalRootExecutable("Git executable", path)
}

// fetchProductionSelfUpdateCommit 把可信 ref 拉到进程唯一临时 ref，完全绕开共享 FETCH_HEAD。
func fetchProductionSelfUpdateCommit(
	ctx context.Context,
	session productionSelfUpdateSession,
	run productionSelfUpdateDepsRun,
) (string, func() error, error) {
	temporaryRef, err := newProductionSelfUpdateRef()
	if err != nil {
		return "", nil, err
	}
	refspec := "+" + session.config.TrustedRef + ":" + temporaryRef
	if _, err := runProductionSelfUpdateGit(
		ctx, session.repository, session.git, run,
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", refspec,
	); err != nil {
		return "", nil, fmt.Errorf("refresh trusted main ref: %w", err)
	}
	release := func() error {
		cleanupContext, cancel := gateprivate.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := runProductionSelfUpdateGit(
			cleanupContext, session.repository, session.git, run,
			"update-ref", "-d", temporaryRef,
		)
		return err
	}
	commit, err := productionSelfUpdateGitLine(
		ctx, session, run, "rev-parse", "--verify", "--end-of-options", temporaryRef+"^{commit}",
	)
	if err != nil {
		return "", nil, errors.Join(fmt.Errorf("resolve trusted main ref: %w", err), release())
	}
	if err := requireProductionSelfUpdateAncestor(ctx, session, run, session.root.BaselineCommit, commit); err != nil {
		return "", nil, errors.Join(err, release())
	}
	return commit, release, nil
}

// fetchProductionSelfUpdateHead 只解析可信 ref 的提交与树，供已安装版本快速命中。
func fetchProductionSelfUpdateHead(
	ctx context.Context,
	session productionSelfUpdateSession,
	run productionSelfUpdateDepsRun,
) (head productionSelfUpdateHead, release func() error, resultErr error) {
	commit, release, err := fetchProductionSelfUpdateCommit(ctx, session, run)
	if err != nil {
		return productionSelfUpdateHead{}, nil, err
	}
	tree, err := productionSelfUpdateGitLine(
		ctx, session, run, "rev-parse", "--verify", "--end-of-options", commit+"^{tree}",
	)
	if err != nil {
		return productionSelfUpdateHead{}, nil, errors.Join(
			fmt.Errorf("resolve trusted main tree: %w", err),
			release(),
		)
	}
	return productionSelfUpdateHead{commit: commit, tree: tree}, release, nil
}

func newProductionSelfUpdateRef() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate production update ref: %w", err)
	}
	return "refs/super-dolphin/self-update/" + hex.EncodeToString(token[:]), nil
}

func runProductionSelfUpdateGit(
	ctx context.Context,
	repository string,
	gitExecutable string,
	run productionSelfUpdateDepsRun,
	args ...string,
) ([]byte, error) {
	commandArgs := append([]string{"-C", repository}, args...)
	return run(ctx, gitExecutable, commandArgs, "", controlledProductionGitEnvironment(gitExecutable))
}

func productionSelfUpdateGitLine(
	ctx context.Context,
	session productionSelfUpdateSession,
	run productionSelfUpdateDepsRun,
	args ...string,
) (string, error) {
	output, err := runProductionSelfUpdateGit(ctx, session.repository, session.git, run, args...)
	if err != nil {
		return "", err
	}
	return strictProductionGitLine(output)
}

func strictProductionGitLine(data []byte) (string, error) {
	if len(data) == 0 || bytes.IndexByte(data, 0) >= 0 || bytes.IndexByte(data, '\r') >= 0 {
		return "", errors.New("Git output is empty or non-canonical")
	}
	line := strings.TrimSuffix(string(data), "\n")
	if line == "" || strings.Contains(line, "\n") {
		return "", errors.New("Git output must contain exactly one line")
	}
	return line, nil
}

func requireProductionSelfUpdateAncestor(
	ctx context.Context,
	session productionSelfUpdateSession,
	run productionSelfUpdateDepsRun,
	ancestor string,
	candidate string,
) error {
	if ancestor == candidate {
		return nil
	}
	if _, err := runProductionSelfUpdateGit(
		ctx, session.repository, session.git, run,
		"merge-base", "--is-ancestor", ancestor, candidate,
	); err != nil {
		return fmt.Errorf("production update candidate %s does not descend from trusted commit %s: %w", candidate, ancestor, err)
	}
	return nil
}

// validateProductionSelfUpdateProgress 拒绝相对当前已安装状态的回退或兄弟提交。
func validateProductionSelfUpdateProgress(
	ctx context.Context,
	session productionSelfUpdateSession,
	candidate string,
	previous *productionSelfUpdateState,
	run productionSelfUpdateDepsRun,
) error {
	if previous == nil {
		return nil
	}
	return requireProductionSelfUpdateAncestor(ctx, session, run, previous.Commit, candidate)
}

// verifyProductionSelfUpdateTrust 以固定 Git 和隔离配置复核 bare remote 与签名基线对象。
func verifyProductionSelfUpdateTrust(
	ctx context.Context,
	repository string,
	gitExecutable string,
	config productionCoordinatorConfig,
	root productionBootstrapRoot,
	run productionSelfUpdateDepsRun,
) error {
	if root.RepoID != config.RepoID || root.TrustedRef != config.TrustedRef {
		return errors.New("production update signed repository identity drifted from installed config")
	}
	session := productionSelfUpdateSession{repository: repository, git: gitExecutable}
	bare, err := productionSelfUpdateGitLine(ctx, session, run, "rev-parse", "--is-bare-repository")
	if err != nil || bare != "true" {
		return errors.Join(errors.New("production update repository is not bare"), err)
	}
	remote, err := productionSelfUpdateGitLine(ctx, session, run, "config", "--local", "--get", "remote.origin.url")
	if err != nil || remote != root.RemoteURL {
		return errors.Join(errors.New("production update repository remote drifted from signed root"), err)
	}
	objectType, err := productionSelfUpdateGitLine(ctx, session, run, "cat-file", "-t", root.BaselineCommit)
	if err != nil || objectType != "commit" {
		return errors.Join(errors.New("production update signed baseline commit is unavailable"), err)
	}
	tree, err := productionSelfUpdateGitLine(ctx, session, run, "rev-parse", "--verify", "--end-of-options", root.BaselineCommit+"^{tree}")
	if err != nil || tree != root.BaselineTree {
		return errors.Join(errors.New("production update signed baseline tree drifted"), err)
	}
	return nil
}
