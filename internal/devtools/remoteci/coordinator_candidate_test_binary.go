package remoteci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// CandidateTestBinaryBuild is trusted builder output. The coordinator verifies every claimed binding before upload.
type CandidateTestBinaryBuild struct {
	BinaryPath           string
	Package              string
	Mode                 string
	GoToolchain          string
	ToolchainSHA256      string
	BuildFlags           []string
	CompileClosureSHA256 string
}

// CandidateTestBinaryBuilder compiles all candidate-bound go test binaries required before shard fan-out.
type CandidateTestBinaryBuilder func(context.Context, RunInput, []gate.ContainerShard, string) ([]CandidateTestBinaryBuild, error)

type candidateTestBinaryAsset struct {
	ref          CandidateTestBinaryArtifactRef
	binaryPath   string
	manifestPath string
}

func buildRemoteCandidateTestBinaryArtifacts(ctx context.Context, builder CandidateTestBinaryBuilder, input RunInput, shards []gate.ContainerShard, jobID, tempRoot, sourcePrefix string) ([]candidateTestBinaryAsset, error) {
	if builder == nil {
		return nil, errors.New("remote CI candidate test binary builder is required")
	}
	builds, err := builder(ctx, input, shards, tempRoot)
	if err != nil {
		return nil, fmt.Errorf("build candidate test binaries: %w", err)
	}
	if len(builds) == 0 || len(builds) > 64 {
		return nil, errors.New("candidate test binary builder produced an invalid count")
	}
	prefix := sourcePrefix + jobID + "/"
	assets := make([]candidateTestBinaryAsset, 0, len(builds))
	seen := make(map[string]struct{}, len(builds))
	for index, build := range builds {
		info, statErr := os.Stat(build.BinaryPath)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil, fmt.Errorf("candidate test binary %d is not a regular binary", index)
		}
		binaryDigest, digestErr := fileDigest(build.BinaryPath)
		if digestErr != nil {
			return nil, digestErr
		}
		identity := build.Package + "\x00" + build.Mode
		if _, exists := seen[identity]; exists {
			return nil, errors.New("candidate test binary builder produced duplicate package and mode")
		}
		seen[identity] = struct{}{}
		manifest := CandidateTestBinaryArtifactManifest{SchemaVersion: CandidateTestBinaryArtifactSchemaVersion, CandidateTree: input.Tree, Package: build.Package, Mode: build.Mode, Platform: "linux/amd64", GoToolchain: build.GoToolchain, CGOEnabled: true, ToolchainSHA256: build.ToolchainSHA256, BuildFlags: append([]string(nil), build.BuildFlags...), CompileClosureSHA256: build.CompileClosureSHA256, BinaryKey: prefix + binaryDigest + ".test-bin", BinarySHA256: "sha256:" + binaryDigest, BinarySize: info.Size()}
		data, manifestDigest, encodeErr := EncodeCandidateTestBinaryArtifactManifest(manifest)
		if encodeErr != nil {
			return nil, encodeErr
		}
		manifestDigest = strings.TrimPrefix(manifestDigest, "sha256:")
		manifestPath := filepath.Join(tempRoot, fmt.Sprintf("candidate-test-binary-%02d.manifest.json", index))
		if writeErr := os.WriteFile(manifestPath, data, 0o600); writeErr != nil {
			return nil, fmt.Errorf("write candidate test binary manifest: %w", writeErr)
		}
		ref := CandidateTestBinaryArtifactRef{CandidateTree: input.Tree, Package: manifest.Package, Mode: manifest.Mode, Platform: manifest.Platform, GoToolchain: manifest.GoToolchain, CGOEnabled: manifest.CGOEnabled, ToolchainSHA256: manifest.ToolchainSHA256, BuildFlags: append([]string(nil), manifest.BuildFlags...), CompileClosureSHA256: manifest.CompileClosureSHA256, ManifestKey: prefix + manifestDigest + ".manifest.json", ManifestSHA256: manifestDigest, BinaryKey: manifest.BinaryKey, BinarySHA256: strings.TrimPrefix(manifest.BinarySHA256, "sha256:"), BinarySize: manifest.BinarySize}
		if validateErr := ref.Validate(prefix, input.Tree); validateErr != nil {
			return nil, validateErr
		}
		assets = append(assets, candidateTestBinaryAsset{ref: ref, binaryPath: build.BinaryPath, manifestPath: manifestPath})
	}
	return assets, nil
}
