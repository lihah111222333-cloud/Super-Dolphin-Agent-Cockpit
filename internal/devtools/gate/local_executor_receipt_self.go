package gate

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const trustedSelfBinaryLogicalName = ExecutorSelfCommandName

// TrustedSelfBinary is the sealed proof for the gate process that owns a
// ProjectMap invocation. Its absolute path is runtime capability only and is
// deliberately omitted from receipt identity.
type TrustedSelfBinary struct {
	path    string
	digest  string
	version string
}

// VerifiedPath 在 receipt consumer 执行前复核 self 可执行文件、canonical 路径和内容摘要。
func (binary TrustedSelfBinary) VerifiedPath() (string, error) {
	if binary.path == "" || !isPrefixedSHA256Digest(binary.digest) || strings.TrimSpace(binary.version) == "" {
		return "", errors.New("trusted self binary proof is incomplete")
	}
	path, err := canonicalTrustedSelfBinaryPath(binary.path)
	if err != nil {
		return "", fmt.Errorf("reverify trusted self binary path: %w", err)
	}
	if path != binary.path {
		return "", errors.New("trusted self binary path drifted")
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return "", fmt.Errorf("digest trusted self binary: %w", err)
	}
	if digest != binary.digest {
		return "", errors.New("trusted self binary content drifted")
	}
	return path, nil
}

func resolveTrustedSelfBinary() (TrustedSelfBinary, error) {
	path, err := resolveCurrentExecutorExecutable()
	if err != nil {
		return TrustedSelfBinary{}, err
	}
	return newTrustedSelfBinary(path, "")
}

// newTrustedSelfBinary 是显式测试注入 seam；生产空 version 从二进制读取 build identity。
func newTrustedSelfBinary(path, injectedVersion string) (TrustedSelfBinary, error) {
	canonical, err := canonicalTrustedSelfBinaryPath(path)
	if err != nil {
		return TrustedSelfBinary{}, err
	}
	digest, err := fileSHA256(canonical)
	if err != nil {
		return TrustedSelfBinary{}, err
	}
	version := injectedVersion
	if version == "" {
		version, err = trustedSelfBinaryBuildIdentity(canonical)
		if err != nil {
			return TrustedSelfBinary{}, err
		}
	}
	if strings.TrimSpace(version) == "" {
		return TrustedSelfBinary{}, errors.New("trusted self binary version is empty")
	}
	return TrustedSelfBinary{path: canonical, digest: digest, version: version}, nil
}

func canonicalTrustedSelfBinaryPath(path string) (string, error) {
	canonical, err := canonicalReceiptToolPath(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(canonical) {
		return "", errors.New("trusted self binary path must be absolute")
	}
	if err := verifyReceiptToolDirectories(canonical); err != nil {
		return "", err
	}
	return canonical, nil
}

func trustedSelfBinaryBuildIdentity(path string) (string, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read trusted self binary build identity: %w", err)
	}
	version := strings.TrimSpace(info.Main.Version)
	if version == "" {
		return "", errors.New("trusted self binary build version is empty")
	}
	return version, nil
}
