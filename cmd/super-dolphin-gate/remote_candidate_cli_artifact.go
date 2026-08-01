package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

const remoteCandidateCLIArtifactManifestMaxBytes int64 = 64 << 10

// materializeRemoteCandidateCLIArtifact 在清单绑定候选源码和工具链后，将预构建 CLI 原子移交到持久运行路径。
func materializeRemoteCandidateCLIArtifact(
	ctx context.Context,
	workRoot string,
	manifestKey string,
	manifestDigest string,
	candidateTree string,
	sourceSHA256 string,
	toolchainSHA256 string,
	download remoteObjectDownload,
) (string, error) {
	if download == nil || manifestKey == "" || manifestDigest == "" {
		return "", errors.New("candidate CLI artifact download binding is required")
	}
	stage, err := os.MkdirTemp(workRoot, ".candidate-cli-")
	if err != nil {
		return "", fmt.Errorf("create candidate CLI artifact staging root: %w", err)
	}
	defer func() { _ = os.RemoveAll(stage) }()
	manifest, err := downloadRemoteCandidateCLIManifest(ctx, stage, manifestKey, manifestDigest, download)
	if err != nil {
		return "", err
	}
	if err := validateRemoteCandidateCLIManifestIdentity(manifest, candidateTree, sourceSHA256, toolchainSHA256); err != nil {
		return "", err
	}
	stagedBinaryPath, err := downloadRemoteCandidateCLIBinary(ctx, stage, manifest, download)
	if err != nil {
		return "", err
	}
	finalPath, err := installRemoteCandidateCLIBinary(workRoot, stagedBinaryPath)
	if err != nil {
		return "", err
	}
	if err := verifyRemoteCandidateCLIIdentity(ctx, finalPath, manifest.CLIIdentity); err != nil {
		_ = os.Remove(finalPath)
		return "", err
	}
	return finalPath, nil
}

// downloadRemoteCandidateCLIManifest 下载并严格解码候选 CLI 清单。
func downloadRemoteCandidateCLIManifest(ctx context.Context, stage, manifestKey, manifestDigest string, download remoteObjectDownload) (remoteci.CandidateCLIArtifactManifest, error) {
	manifestPath := filepath.Join(stage, "manifest.json")
	if err := downloadVerifiedFile(ctx, download, manifestKey, manifestDigest, remoteCandidateCLIArtifactManifestMaxBytes, manifestPath); err != nil {
		return remoteci.CandidateCLIArtifactManifest{}, fmt.Errorf("download candidate CLI artifact manifest: %w", err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return remoteci.CandidateCLIArtifactManifest{}, fmt.Errorf("read candidate CLI artifact manifest: %w", err)
	}
	manifest, err := remoteci.DecodeCandidateCLIArtifactManifest(data)
	if err != nil {
		return remoteci.CandidateCLIArtifactManifest{}, err
	}
	if err := manifest.ValidateForManifestKey(manifestKey); err != nil {
		return remoteci.CandidateCLIArtifactManifest{}, err
	}
	return manifest, nil
}

// validateRemoteCandidateCLIManifestIdentity 确认清单属于当前候选树和可信工具链。
func validateRemoteCandidateCLIManifestIdentity(manifest remoteci.CandidateCLIArtifactManifest, candidateTree, sourceSHA256, toolchainSHA256 string) error {
	if manifest.CandidateTree != candidateTree || manifest.SourceSHA256 != sourceSHA256 || manifest.ToolchainSHA256 != toolchainSHA256 {
		return errors.New("candidate CLI artifact manifest does not match shard candidate identity")
	}
	return nil
}

// downloadRemoteCandidateCLIBinary 下载、检查并赋予候选二进制可执行权限。
func downloadRemoteCandidateCLIBinary(ctx context.Context, stage string, manifest remoteci.CandidateCLIArtifactManifest, download remoteObjectDownload) (string, error) {
	stagedBinaryPath := filepath.Join(stage, "super-dolphin-gate")
	if err := downloadVerifiedFile(ctx, download, manifest.BinaryKey, manifest.BinarySHA256, manifest.BinarySize, stagedBinaryPath); err != nil {
		return "", fmt.Errorf("download candidate CLI artifact binary: %w", err)
	}
	info, err := os.Stat(stagedBinaryPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() != manifest.BinarySize {
		return "", errors.New("candidate CLI artifact binary is invalid after download")
	}
	if err := os.Chmod(stagedBinaryPath, 0o755); err != nil {
		return "", fmt.Errorf("make candidate CLI artifact executable: %w", err)
	}
	return stagedBinaryPath, nil
}

// installRemoteCandidateCLIBinary 将已验证二进制原子安装到固定 worker 路径。
func installRemoteCandidateCLIBinary(workRoot, stagedBinaryPath string) (string, error) {
	finalPath := filepath.Join(workRoot, "bin", "super-dolphin-gate")
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return "", fmt.Errorf("create candidate CLI artifact destination: %w", err)
	}
	if info, err := os.Lstat(finalPath); err == nil && !info.Mode().IsRegular() {
		return "", errors.New("candidate CLI artifact destination is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect candidate CLI artifact destination: %w", err)
	}
	if err := os.Rename(stagedBinaryPath, finalPath); err != nil {
		return "", fmt.Errorf("atomically install candidate CLI artifact: %w", err)
	}
	return finalPath, nil
}

// verifyRemoteCandidateCLIIdentity 验证安装后二进制自报身份仍与清单一致。
func verifyRemoteCandidateCLIIdentity(ctx context.Context, finalPath, expectedIdentity string) error {
	identity, err := exec.CommandContext(ctx, finalPath, "worker", "cli-identity").Output()
	if err != nil {
		return fmt.Errorf("execute candidate CLI identity: %w", err)
	}
	if string(identity) != expectedIdentity+"\n" {
		return errors.New("candidate CLI artifact binary identity does not match manifest")
	}
	return nil
}
