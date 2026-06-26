package codexapp

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/codexmanifest"
)

const codexManagedManifestName = codexmanifest.Name

type codexManifestVerifier struct{}

// IsExecutable 复用 provider 的平台可执行文件判定。
// manifest 校验通过这个接口隔离文件权限和 Windows 头部检查细节。
func (codexManifestVerifier) IsExecutable(path string) bool { return isExecutable(path) }

// ValidCLI 验证指定 Codex CLI 能否启动 app-server。
// 只有二进制可实际服务当前 provider 时 manifest 才算可信。
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
