package remoteci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// MaterializeVerifiedSourceBundle 将已经通过 strict manifest、bundle digest 和 Git
// object-closure 复核的 prerequisite bundle 安装为 detached sourceRoot。baseline
// object store 必须来自 accepted image，sourceRoot 仅保留候选 Git 元数据。
func MaterializeVerifiedSourceBundle(ctx context.Context, artifactRoot string, sourceRoot string, baseline SourceBaseline) (result SourceMaterializationManifest, err error) {
	if err := validateContext(ctx); err != nil {
		return SourceMaterializationManifest{}, err
	}
	if err := validateCanonicalDirectory(sourceRoot, false); err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("validate source worktree root: %w", err)
	}
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("read source worktree root: %w", err)
	}
	if len(entries) != 0 {
		return SourceMaterializationManifest{}, errors.New("source worktree root must be empty")
	}
	manifest, err := ImportAndVerifySourceBundle(ctx, artifactRoot, baseline)
	if err != nil {
		return SourceMaterializationManifest{}, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, clearSourceWorktree(sourceRoot))
		}
	}()
	bundlePath := filepath.Join(artifactRoot, sourceBundleName)
	if err := materializeSourceWorktree(ctx, bundlePath, sourceRoot, manifest, baseline); err != nil {
		return SourceMaterializationManifest{}, err
	}
	return manifest, nil
}

// materializeSourceWorktree 在目标目录中导入已验证 bundle、固定 refs 并 detached checkout。
func materializeSourceWorktree(ctx context.Context, bundlePath string, sourceRoot string, manifest SourceMaterializationManifest, baseline SourceBaseline) error {
	output, err := runGitOutput(ctx, sourceRoot, nil, "init", "-q", "--object-format="+string(manifest.ObjectFormat))
	if err := rejectGitOutput(output, err, "initialize source worktree repository"); err != nil {
		return err
	}
	if err := configureObjectStoreAlternate(ctx, sourceRoot, baseline); err != nil {
		return err
	}
	if err := importSourceWorktreeBundle(ctx, bundlePath, sourceRoot, manifest, baseline); err != nil {
		return err
	}
	return checkoutMaterializedSource(ctx, sourceRoot, manifest)
}

func importSourceWorktreeBundle(ctx context.Context, bundlePath string, sourceRoot string, manifest SourceMaterializationManifest, baseline SourceBaseline) error {
	if _, err := runGitOutput(ctx, sourceRoot, nil, "bundle", "verify", bundlePath); err != nil {
		return fmt.Errorf("verify source worktree bundle: %w", err)
	}
	output, err := runGitOutput(ctx, sourceRoot, nil, "bundle", "unbundle", bundlePath)
	if err != nil {
		return fmt.Errorf("import source worktree bundle: %w", err)
	}
	if string(output) != expectedBundleRefs(manifest) {
		return errors.New("source worktree bundle advertised unexpected or trailing refs")
	}
	if err := verifyBundlePrerequisites(bundlePath, manifest, baseline); err != nil {
		return err
	}
	refInput := fmt.Sprintf(
		"create %s %s\ncreate %s %s\n",
		sourceBundleRef,
		manifest.TransportCommitSHA,
		cicontract.SourceBaseRef,
		manifest.SyntheticBaseCommitSHA,
	)
	output, err = runGitOutput(ctx, sourceRoot, strings.NewReader(refInput), "update-ref", "--stdin")
	if err := rejectGitOutput(output, err, "create source worktree refs"); err != nil {
		return err
	}
	return verifyImportedSource(ctx, sourceRoot, manifest, baseline)
}

func checkoutMaterializedSource(ctx context.Context, sourceRoot string, manifest SourceMaterializationManifest) error {
	output, err := runGitOutput(ctx, sourceRoot, nil, "checkout", "--quiet", "--detach", manifest.TransportCommitSHA)
	if err := rejectGitOutput(output, err, "checkout materialized source"); err != nil {
		return err
	}
	base, err := runGitOutput(ctx, sourceRoot, nil, "rev-parse", "--verify", cicontract.SourceBaseRef+"^{commit}")
	if err != nil || strings.TrimSpace(string(base)) != manifest.SyntheticBaseCommitSHA {
		return errors.New("materialized source base ref does not match synthetic base commit")
	}
	status, err := runGitOutput(ctx, sourceRoot, nil, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("verify source worktree status: %w", err)
	}
	if len(status) != 0 {
		return errors.New("materialized source worktree is not clean")
	}
	return nil
}

// clearSourceWorktree 仅清理本次尚未成功发布的空 sourceRoot 内容。
func clearSourceWorktree(sourceRoot string) error {
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		failures = append(failures, os.RemoveAll(filepath.Join(sourceRoot, entry.Name())))
	}
	return errors.Join(failures...)
}
