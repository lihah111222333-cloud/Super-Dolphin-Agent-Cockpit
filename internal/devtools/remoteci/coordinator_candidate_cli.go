package remoteci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CandidateCLIBuilder compiles one candidate-bound Linux CLI before shard fan-out.
// The builder must use a trusted toolchain and must never use the coordinator host Go implicitly.
type CandidateCLIBuilder func(context.Context, RunInput, string) (string, error)

func buildRemoteCandidateCLIArtifact(ctx context.Context, builder CandidateCLIBuilder, input RunInput, jobID, tempRoot, sourcePrefix string) (CandidateCLIArtifactRef, string, string, string, error) {
	if builder == nil {
		return CandidateCLIArtifactRef{}, "", "", "", errors.New("remote CI candidate CLI builder is required")
	}
	binaryPath, err := builder(ctx, input, tempRoot)
	if err != nil {
		return CandidateCLIArtifactRef{}, "", "", "", fmt.Errorf("build candidate CLI: %w", err)
	}
	info, err := os.Stat(binaryPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return CandidateCLIArtifactRef{}, "", "", "", errors.New("candidate CLI builder did not produce a regular binary")
	}
	binaryDigest, err := fileDigest(binaryPath)
	if err != nil {
		return CandidateCLIArtifactRef{}, "", "", "", err
	}
	prefix := sourcePrefix + jobID + "/"
	manifest := CandidateCLIArtifactManifest{SchemaVersion: CandidateCLIArtifactSchemaVersion, CandidateTree: input.Tree, SourceSHA256: input.CandidateGateSourceSHA256, ToolchainSHA256: input.CandidateGateToolchainSHA256, Platform: "linux/amd64", BinaryKey: prefix + binaryDigest + ".candidate-cli", BinarySHA256: "sha256:" + binaryDigest, BinarySize: info.Size(), CLIIdentity: CandidateCLIIdentity(input.CandidateGateSourceSHA256, input.CandidateGateToolchainSHA256)}
	data, manifestDigest, err := EncodeCandidateCLIArtifactManifest(manifest)
	if err != nil {
		return CandidateCLIArtifactRef{}, "", "", "", err
	}
	manifestDigest = strings.TrimPrefix(manifestDigest, "sha256:")
	manifestPath := filepath.Join(tempRoot, "candidate-cli.manifest.json")
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return CandidateCLIArtifactRef{}, "", "", "", fmt.Errorf("write candidate CLI manifest: %w", err)
	}
	ref := CandidateCLIArtifactRef{CandidateTree: input.Tree, SourceSHA256: input.CandidateGateSourceSHA256, ToolchainSHA256: input.CandidateGateToolchainSHA256, Platform: "linux/amd64", ManifestKey: prefix + manifestDigest + ".manifest.json", ManifestSHA256: manifestDigest, BinaryKey: manifest.BinaryKey, BinarySHA256: strings.TrimPrefix(manifest.BinarySHA256, "sha256:"), BinarySize: manifest.BinarySize, CLIIdentity: manifest.CLIIdentity}
	if err := ref.Validate(prefix, input.Tree); err != nil {
		return CandidateCLIArtifactRef{}, "", "", "", err
	}
	return ref, binaryPath, manifestPath, manifest.BinaryKey, nil
}
