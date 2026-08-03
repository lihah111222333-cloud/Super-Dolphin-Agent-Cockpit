package remoteci

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	sourceBundleName            = "source.bundle"
	sourceManifestName          = "source-manifest.json"
	sourceBundleRef             = "refs/source/materialized"
	sourceManifestVersion       = 2
	sourceTransportKind         = "git-bundle-thin"
	privateSourceFileMode       = 0o400
	privateSourceDirMode        = 0o700
	maxSourceManifestLength     = 1 << 20
	maxSourceBundleHeaderLength = 1 << 20
)

// SourceMaterializationManifest 记录 bundle 的 Git object truth 与完整性摘要。
type SourceMaterializationManifest struct {
	SchemaVersion        uint32          `json:"schema_version"`
	TransportKind        string          `json:"transport_kind"`
	Source               gate.SourceSpec `json:"source"`
	SourceTreeSHA        string          `json:"source_tree_sha"`
	TransportCommitSHA   string          `json:"transport_commit_sha"`
	TrustedBaseCommitSHA string          `json:"trusted_base_commit_sha,omitempty"`
	// BaselineCommitSHA and BaselineTreeSHA identify the immutable Git
	// prerequisite supplied by the accepted image. They are deliberately
	// separate from TrustedBaseCommitSHA, which describes the candidate
	// SourceSpec parent relationship.
	BaselineCommitSHA string               `json:"baseline_commit_sha"`
	BaselineTreeSHA   string               `json:"baseline_tree_sha"`
	BundleDigest      string               `json:"bundle_digest"`
	ObjectFormat      gate.GitObjectFormat `json:"object_format"`
}

// SourceBaseline 标识 accepted image 挂载的只读 Git 对象存储。
// thin source bundle 可以引用这些对象，但不得通过网络携带它们。
type SourceBaseline struct {
	RepositoryRoot string
	CommitSHA      string
	TreeSHA        string
	ObjectFormat   gate.GitObjectFormat
}

const (
	deterministicSourceBaselineName     = "Super Dolphin Source Baseline"
	deterministicSourceBaselineEmail    = "source-baseline.invalid"
	deterministicSourceBaselineDate     = "2000-01-01T00:00:00Z"
	deterministicSourceBaselineMessage  = "super-dolphin accepted source baseline"
	deterministicSourceTransportName    = "Super Dolphin Source Transport"
	deterministicSourceTransportEmail   = "source-transport.invalid"
	deterministicSourceTransportMessage = "super-dolphin source transport"
)

// DeterministicSourceBaselineCommitSHA 计算镜像 provisioning 必须写入只读
// source-git 对象存储的 accepted tree 基线 commit 身份。该 commit 无 parent，
// author、committer 和 message 均固定，不携带仓库历史但可作为 Git prerequisite。
func DeterministicSourceBaselineCommitSHA(tree string, format gate.GitObjectFormat) (string, error) {
	if !validOID(tree, format) {
		return "", errors.New("source baseline tree is invalid")
	}
	data := deterministicSourceBaselinePayload(tree)
	object, err := hashGitObject(format, "commit", data)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(object), nil
}

// BuildSourceBaseline 构造 thin source transport 所需的最小只读 image baseline
// ODB：accepted tree/blob 闭包和一个确定性的 parentless commit；不会复制候选历史。
func BuildSourceBaseline(ctx context.Context, repoRoot string, tree string, outputRoot string, format gate.GitObjectFormat) (SourceBaseline, error) {
	if err := validateSourceBaselineBuildInput(ctx, repoRoot, tree, outputRoot, format); err != nil {
		return SourceBaseline{}, err
	}
	if err := initBareRepository(ctx, filepath.Dir(outputRoot), outputRoot, format); err != nil {
		return SourceBaseline{}, err
	}
	if err := transferTreeClosure(ctx, repoRoot, outputRoot, tree); err != nil {
		return SourceBaseline{}, err
	}
	commitSHA, err := finalizeSourceBaseline(ctx, outputRoot, tree, format)
	if err != nil {
		return SourceBaseline{}, err
	}
	return SourceBaseline{RepositoryRoot: outputRoot, CommitSHA: commitSHA, TreeSHA: tree, ObjectFormat: format}, nil
}

// finalizeSourceBaseline 写入确定性的 parentless commit、ref 与只读闭包，
// 使 accepted image baseline 在发布前完成全部身份复核。
func finalizeSourceBaseline(ctx context.Context, outputRoot string, tree string, format gate.GitObjectFormat) (string, error) {
	commitSHA, err := DeterministicSourceBaselineCommitSHA(tree, format)
	if err != nil {
		return "", err
	}
	payload := deterministicSourceBaselinePayload(tree)
	encoded, err := runGitOutput(ctx, outputRoot, bytes.NewReader(payload), "hash-object", "-t", "commit", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("write deterministic source baseline commit: %w", err)
	}
	actual, err := strictGitLine(encoded)
	if err != nil || actual != commitSHA {
		return "", errors.New("deterministic source baseline commit identity drifted")
	}
	refInput := fmt.Sprintf("create %s %s\n", sourceBundleRef, commitSHA)
	refOutput, refErr := runGitOutput(ctx, outputRoot, strings.NewReader(refInput), "update-ref", "--stdin")
	if err := rejectGitOutput(refOutput, refErr, "create source baseline ref"); err != nil {
		return "", err
	}
	if err := verifyObjectClosure(ctx, outputRoot, commitSHA); err != nil {
		return "", err
	}
	if err := makeReadOnlyGitTree(outputRoot); err != nil {
		return "", err
	}
	return commitSHA, nil
}

// validateSourceBaselineBuildInput 在创建 image baseline 前完成所有输入与
// accepted tree 校验，确保后续只执行确定性的 Git 物化步骤。
func validateSourceBaselineBuildInput(ctx context.Context, repoRoot string, tree string, outputRoot string, format gate.GitObjectFormat) error {
	if err := validateContext(ctx); err != nil {
		return err
	}
	if err := errors.Join(validateCanonicalDirectory(repoRoot, false), validateCanonicalDirectory(outputRoot, true)); err != nil {
		return fmt.Errorf("validate source baseline build roots: %w", err)
	}
	if err := verifyRepositoryIdentity(ctx, repoRoot, format); err != nil {
		return err
	}
	if !validOID(tree, format) {
		return errors.New("source baseline tree is invalid")
	}
	object, err := readSourceObject(ctx, repoRoot, tree)
	if err != nil || object.kind != "tree" {
		return errors.Join(errors.New("source baseline tree object is missing or not a tree"), err)
	}
	entries, err := os.ReadDir(outputRoot)
	if err != nil {
		return fmt.Errorf("read source baseline output root: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("source baseline output root must be empty")
	}
	return nil
}

func deterministicSourceBaselinePayload(tree string) []byte {
	return fmt.Appendf(nil,
		"tree %s\nauthor %s <%s> 946684800 +0000\ncommitter %s <%s> 946684800 +0000\n\n%s\n",
		tree, deterministicSourceBaselineName, deterministicSourceBaselineEmail,
		deterministicSourceBaselineName, deterministicSourceBaselineEmail,
		deterministicSourceBaselineMessage,
	)
}

// DeterministicSourceTransportCommitSHA 计算候选 tree 相对 accepted baseline
// 的固定 transport commit 身份。该 commit 与候选原始 commit/range 历史无关，
// 唯一 parent 是 image baseline，tree 是 SourceTreeSHA。
func DeterministicSourceTransportCommitSHA(tree string, baseline string, format gate.GitObjectFormat) (string, error) {
	if !validOID(tree, format) || !validOID(baseline, format) {
		return "", errors.New("source transport tree or baseline commit is invalid")
	}
	object, err := hashGitObject(format, "commit", deterministicSourceTransportPayload(tree, baseline))
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(object), nil
}

func deterministicSourceTransportPayload(tree string, baseline string) []byte {
	return fmt.Appendf(nil,
		"tree %s\nparent %s\nauthor %s <%s> 946684800 +0000\ncommitter %s <%s> 946684800 +0000\n\n%s\n",
		tree, baseline, deterministicSourceTransportName, deterministicSourceTransportEmail,
		deterministicSourceTransportName, deterministicSourceTransportEmail,
		deterministicSourceTransportMessage,
	)
}

// transferTreeClosure 将 accepted tree 的完整 Git object 闭包写入 baseline ODB。
func transferTreeClosure(ctx context.Context, repoRoot string, bareRoot string, tree string) error {
	packPath := filepath.Join(filepath.Dir(bareRoot), ".source-baseline.pack")
	defer func() { _ = os.Remove(packPath) }()
	pack, err := os.OpenFile(packPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source baseline pack: %w", err)
	}
	packErr := runGit(ctx, repoRoot, strings.NewReader(tree+"\n"), pack, "pack-objects", "--stdout", "--revs")
	closeErr := pack.Close()
	if packErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("pack source baseline tree: %w", packErr), closeErr)
	}
	pack, err = os.Open(packPath)
	if err != nil {
		return fmt.Errorf("open source baseline pack: %w", err)
	}
	output, indexErr := runGitOutput(ctx, bareRoot, pack, "index-pack", "--stdin")
	closeErr = pack.Close()
	if indexErr != nil || closeErr != nil {
		return errors.Join(fmt.Errorf("index source baseline pack: %w", indexErr), closeErr)
	}
	line, err := strictGitLine(output)
	if err != nil || !strings.HasPrefix(line, "pack\t") {
		return errors.New("source baseline index-pack returned unexpected output")
	}
	return nil
}

func makeReadOnlyGitTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if entry.IsDir() {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	})
}
