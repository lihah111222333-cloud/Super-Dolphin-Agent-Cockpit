package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const remoteCandidateTestBinaryManifestMaxBytes int64 = 64 << 10

// materializeRemoteCandidateTestBinaries atomically installs every request-bound Go test binary.
// It deliberately accepts no source-build fallback: a missing or mismatched bundle is a worker failure.
func materializeRemoteCandidateTestBinaries(
	ctx context.Context,
	workRoot string,
	candidateTree string,
	refs []remoteci.CandidateTestBinaryArtifactRef,
	download remoteObjectDownload,
) (string, error) {
	if download == nil || candidateTree == "" {
		return "", errors.New("candidate test binary download binding is required")
	}
	if len(refs) == 0 {
		return "", nil
	}
	stage, err := os.MkdirTemp(workRoot, ".candidate-test-binaries-")
	if err != nil {
		return "", fmt.Errorf("create candidate test binary staging root: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	entries := make([]gatecontract.CandidateTestBinaryBundle, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for index, ref := range refs {
		if err := ref.Validate(filepath.ToSlash(filepath.Dir(ref.ManifestKey))+"/", candidateTree); err != nil {
			return "", fmt.Errorf("validate candidate test binary reference %d: %w", index, err)
		}
		identity := ref.Package + "\x00" + ref.Mode
		if _, duplicate := seen[identity]; duplicate {
			return "", errors.New("candidate test binary references contain duplicate package and mode")
		}
		seen[identity] = struct{}{}
		manifestPath := filepath.Join(stage, fmt.Sprintf("%02d.manifest.json", index))
		if err := downloadVerifiedFile(ctx, download, ref.ManifestKey, ref.ManifestSHA256, remoteCandidateTestBinaryManifestMaxBytes, manifestPath); err != nil {
			return "", fmt.Errorf("download candidate test binary manifest %q: %w", identity, err)
		}
		manifestData, err := os.ReadFile(manifestPath)
		if err != nil {
			return "", fmt.Errorf("read candidate test binary manifest %q: %w", identity, err)
		}
		manifest, err := remoteci.DecodeCandidateTestBinaryArtifactManifest(manifestData)
		if err != nil {
			return "", fmt.Errorf("decode candidate test binary manifest %q: %w", identity, err)
		}
		if err := validateRemoteCandidateTestBinaryManifest(ref, manifest, candidateTree); err != nil {
			return "", err
		}
		stagedBinary := filepath.Join(stage, fmt.Sprintf("%02d.test-bin", index))
		if err := downloadVerifiedFile(ctx, download, manifest.BinaryKey, manifest.BinarySHA256, manifest.BinarySize, stagedBinary); err != nil {
			return "", fmt.Errorf("download candidate test binary %q: %w", identity, err)
		}
		info, err := os.Stat(stagedBinary)
		if err != nil || !info.Mode().IsRegular() || info.Size() != manifest.BinarySize {
			return "", fmt.Errorf("candidate test binary %q is invalid after download", identity)
		}
		if err := os.Chmod(stagedBinary, 0o555); err != nil {
			return "", fmt.Errorf("make candidate test binary %q executable: %w", identity, err)
		}
		entries = append(entries, gatecontract.CandidateTestBinaryBundle{
			Package: manifest.Package, Mode: manifest.Mode, BinaryPath: stagedBinary,
			BinarySHA256: manifest.BinarySHA256, BinarySize: manifest.BinarySize,
		})
	}
	return gatecontract.InstallCandidateTestBinaryBundleIndex(workRoot, entries)
}

func validateRemoteCandidateTestBinaryManifest(ref remoteci.CandidateTestBinaryArtifactRef, manifest remoteci.CandidateTestBinaryArtifactManifest, candidateTree string) error {
	if manifest.CandidateTree != candidateTree || manifest.CandidateTree != ref.CandidateTree ||
		manifest.Package != ref.Package || manifest.Mode != ref.Mode || manifest.Platform != ref.Platform ||
		manifest.GoToolchain != ref.GoToolchain || manifest.CGOEnabled != ref.CGOEnabled || manifest.ToolchainSHA256 != ref.ToolchainSHA256 ||
		!slices.Equal(manifest.BuildFlags, ref.BuildFlags) || manifest.CompileClosureSHA256 != ref.CompileClosureSHA256 ||
		manifest.BinaryKey != ref.BinaryKey || manifest.BinarySHA256 != "sha256:"+ref.BinarySHA256 || manifest.BinarySize != ref.BinarySize {
		return errors.New("candidate test binary manifest does not match shard candidate identity")
	}
	return nil
}
