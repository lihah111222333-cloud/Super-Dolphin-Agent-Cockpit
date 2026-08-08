package remoteci

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func buildSourceManifest(bundlePath string, spec gate.SourceSpec, plan sourcePlan, baseline SourceBaseline) (SourceMaterializationManifest, error) {
	digest, err := digestSourceFile(bundlePath)
	if err != nil {
		return SourceMaterializationManifest{}, err
	}
	manifest := SourceMaterializationManifest{
		SchemaVersion:          sourceManifestVersion,
		TransportKind:          sourceTransportKind,
		Source:                 spec,
		SourceTreeSHA:          plan.tree,
		TransportCommitSHA:     plan.transportCommit,
		TrustedBaseCommitSHA:   plan.baseCommit,
		BaselineCommitSHA:      baseline.CommitSHA,
		BaselineTreeSHA:        baseline.TreeSHA,
		SyntheticBaseTreeSHA:   plan.baseTree,
		SyntheticBaseCommitSHA: plan.syntheticBaseCommit,
		BundleDigest:           digest,
		ObjectFormat:           spec.ObjectFormat,
	}
	if err := manifest.Validate(); err != nil {
		return SourceMaterializationManifest{}, err
	}
	return manifest, nil
}

// Validate 校验 source manifest 与原 SourceSpec 的逐字段身份关系。
func (manifest SourceMaterializationManifest) Validate() error {
	if err := manifest.Source.Validate(); err != nil {
		return fmt.Errorf("validate manifest source: %w", err)
	}
	if err := validateManifestIdentityFields(manifest); err != nil {
		return err
	}
	if manifest.TrustedBaseCommitSHA != "" && !validOID(manifest.TrustedBaseCommitSHA, manifest.ObjectFormat) {
		return errors.New("source manifest trusted base commit is invalid")
	}
	if err := validateManifestCommitIdentity(manifest); err != nil {
		return err
	}
	return validateManifestSyntheticBase(manifest)
}

// validateManifestIdentityFields 检查 manifest 的 schema、transport、tree、
// object ID、digest 与 candidate identity 字段。
func validateManifestIdentityFields(manifest SourceMaterializationManifest) error {
	if err := validateManifestIdentityMetadata(manifest); err != nil {
		return err
	}
	return validateManifestIdentityObjects(manifest)
}

// validateManifestIdentityMetadata 检查 manifest 的协议、格式与 source tree 身份。
func validateManifestIdentityMetadata(manifest SourceMaterializationManifest) error {
	if manifest.SchemaVersion != sourceManifestVersion || manifest.TransportKind != sourceTransportKind ||
		manifest.ObjectFormat != manifest.Source.ObjectFormat || manifest.SourceTreeSHA != manifest.Source.SourceTreeSHA {
		return errors.New("source manifest identity or digest is invalid")
	}
	return nil
}

// validateManifestIdentityObjects 检查 manifest 的 Git object ID 与 bundle digest。
func validateManifestIdentityObjects(manifest SourceMaterializationManifest) error {
	if !validOID(manifest.TransportCommitSHA, manifest.ObjectFormat) ||
		!validOID(manifest.BaselineCommitSHA, manifest.ObjectFormat) ||
		!validOID(manifest.BaselineTreeSHA, manifest.ObjectFormat) ||
		!validOID(manifest.SyntheticBaseTreeSHA, manifest.ObjectFormat) ||
		!validOID(manifest.SyntheticBaseCommitSHA, manifest.ObjectFormat) ||
		!validDigest(manifest.BundleDigest) {
		return errors.New("source manifest identity or digest is invalid")
	}
	return nil
}

// validateManifestCommitIdentity 约束 SourceSpec 的原始身份；transport
// commit 是独立的、确定性的 Git 载体，不得被误当作原始 commit/head。
func validateManifestCommitIdentity(manifest SourceMaterializationManifest) error {
	switch manifest.Source.Kind {
	case gate.SourceKindRange:
		if manifest.Source.Range.HeadSHA == "" {
			return errors.New("range manifest source head is missing")
		}
	case gate.SourceKindCommit:
		if manifest.Source.Commit.SHA == "" {
			return errors.New("commit manifest source identity is missing")
		}
	case gate.SourceKindTree:
		if manifest.Source.Tree.SHA != manifest.SourceTreeSHA {
			return errors.New("tree manifest source identity does not match source tree")
		}
	default:
		return fmt.Errorf("unsupported manifest source kind %q", manifest.Source.Kind)
	}
	return validateManifestTrustedBase(manifest)
}

// validateManifestTrustedBase 约束显式 base 只来自 SourceSpec 或 commit 的真实单 parent。
func validateManifestTrustedBase(manifest SourceMaterializationManifest) error {
	switch manifest.Source.Kind {
	case gate.SourceKindRange:
		if manifest.Source.Range.BaseKind == gate.BaseKindCommit &&
			manifest.TrustedBaseCommitSHA != manifest.Source.Range.BaseSHA {
			return errors.New("range manifest trusted base does not match SourceSpec")
		}
		if manifest.Source.Range.BaseKind != gate.BaseKindCommit && manifest.TrustedBaseCommitSHA != "" {
			return errors.New("empty-tree range manifest must not contain a trusted base")
		}
	case gate.SourceKindTree:
		if manifest.TrustedBaseCommitSHA != manifest.Source.Tree.ParentCommitSHA {
			return errors.New("tree manifest trusted base does not match SourceSpec")
		}
	}
	return nil
}

// validateManifestSyntheticBase 约束候选 parent synthetic commit 的 tree、
// parent 与 transport commit 身份全部由 manifest 与 accepted baseline 重算。
func validateManifestSyntheticBase(manifest SourceMaterializationManifest) error {
	expectedSynthetic, err := DeterministicSourceSyntheticBaseCommitSHA(manifest.SyntheticBaseTreeSHA, manifest.BaselineCommitSHA, manifest.ObjectFormat)
	if err != nil || manifest.SyntheticBaseCommitSHA != expectedSynthetic {
		return errors.New("source manifest synthetic base commit is not deterministic")
	}
	expectedTransport, err := DeterministicSourceTransportCommitSHA(manifest.SourceTreeSHA, manifest.SyntheticBaseCommitSHA, manifest.ObjectFormat)
	if err != nil || manifest.TransportCommitSHA != expectedTransport {
		return errors.New("source manifest transport commit is not deterministic")
	}
	return nil
}

// writeSourceManifest 以独占创建和只读权限发布严格 JSON manifest。
func writeSourceManifest(path string, manifest SourceMaterializationManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode source manifest: %w", err)
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create source manifest: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return closeSourceFile(file, err)
	}
	if err := file.Sync(); err != nil {
		return closeSourceFile(file, err)
	}
	if err := file.Chmod(privateSourceFileMode); err != nil {
		return closeSourceFile(file, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close source manifest: %w", err)
	}
	return nil
}

func readSourceManifest(path string) (SourceMaterializationManifest, error) {
	file, err := os.Open(path)
	if err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("open source manifest: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSourceManifestLength+1))
	if err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("read source manifest: %w", err)
	}
	if len(data) > maxSourceManifestLength {
		return SourceMaterializationManifest{}, errors.New("source manifest is too large")
	}
	var manifest SourceMaterializationManifest
	if err := gate.DecodeStrictJSON(data, &manifest); err != nil {
		return SourceMaterializationManifest{}, fmt.Errorf("decode source manifest: %w", err)
	}
	return manifest, nil
}

// publishSourceArtifacts 原子移动两个只读文件，并在失败时清理局部发布。
