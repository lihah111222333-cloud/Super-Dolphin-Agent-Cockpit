package codexapp

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/codexmanifest"
)

const codexManagedManifestName = codexmanifest.Name

type codexManifestVerifier struct{}

// IsExecutable 判断路径是否指向可执行文件。
func (codexManifestVerifier) IsExecutable(path string) bool { return isExecutable(path) }

// ValidCLI 判断 Codex CLI 是否可用。
func (codexManifestVerifier) ValidCLI(ctx context.Context, path string) bool {
	return validCodexCLI(ctx, path)
}

func writeManagedCodexManifest(target, version, assetName, sourceSHA256, codexPath string) error {
	return codexmanifest.WriteManaged(target, version, assetName, sourceSHA256, codexPath, codexExecutableFileName())
}

func verifyManagedCodexInstall(ctx context.Context, target, expectedSourceSHA256 string) (string, error) {
	return codexmanifest.VerifyManaged(ctx, target, expectedSourceSHA256, codexExecutableFileName(), codexManifestVerifier{})
}

func verifyBundledCodexInstall(ctx context.Context, binaryPath string) error {
	return codexmanifest.VerifyBundled(ctx, binaryPath, codexManifestVerifier{})
}
