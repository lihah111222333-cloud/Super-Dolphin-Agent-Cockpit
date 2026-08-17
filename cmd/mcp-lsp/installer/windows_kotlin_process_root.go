//go:build windows

package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

var windowsKotlinProcessRootMu sync.Mutex

// MaterializeWindowsKotlinProcessRoot 在产品根内发布 Kotlin 的物理扁平副本。
// JetBrains server 会 realpath JNI DLL；8.3、subst 或 junction 仍可能恢复原始长路径，
// 因此这里复制已校验 ready 树到同一产品根的短布局。完整 ready 树继续作为持久事实，
// 扁平副本只用于进程边界，并拒绝所有 symlink/reparse/越界输入。
func MaterializeWindowsKotlinProcessRoot(productRoot, serverBinary string) (target string, resultErr error) {
	productRoot = filepath.Clean(strings.TrimSpace(productRoot))
	serverBinary = filepath.Clean(strings.TrimSpace(serverBinary))
	if productRoot == "." || !filepath.IsAbs(productRoot) || serverBinary == "." || !filepath.IsAbs(serverBinary) {
		return "", errors.New("Kotlin process root requires absolute product and server paths")
	}
	if !strings.EqualFold(filepath.Base(serverBinary), "intellij-server.exe") {
		return "", fmt.Errorf("unexpected Kotlin server basename %q", filepath.Base(serverBinary))
	}
	if err := validateWindowsInstallerPathWithinRoot(productRoot, serverBinary, false); err != nil {
		return "", fmt.Errorf("Kotlin server escaped product root: %w", securefs.WrapErrorForPath(err, serverBinary))
	}
	readyRoot := filepath.Dir(filepath.Dir(serverBinary))
	if err := validateWindowsInstallerPathWithinRoot(productRoot, readyRoot, false); err != nil {
		return "", fmt.Errorf("Kotlin ready root escaped product root: %w", securefs.WrapErrorForPath(err, readyRoot))
	}
	if info, err := os.Lstat(readyRoot); err != nil {
		return "", fmt.Errorf("Kotlin ready root is not a real directory: %w", wrapKotlinWindowsPathError(err, readyRoot))
	} else if isUnsafeAssetFile(info) || !info.IsDir() {
		return "", fmt.Errorf("Kotlin ready root is not a real directory: %w", securefs.WrapErrorForPath(errors.New("not a real directory"), readyRoot))
	}
	if err := securefs.CheckExistingOwnerOnly(readyRoot, nil); err != nil {
		return "", fmt.Errorf("Kotlin ready root ACL validation failed: %w", securefs.WrapErrorForPath(err, readyRoot))
	}
	serverHash, err := windowsKotlinProcessFileSHA256(serverBinary)
	if err != nil {
		return "", fmt.Errorf("hash Kotlin server: %w", securefs.WrapErrorForPath(err, serverBinary))
	}
	sourceTreeDigest, err := windowsKotlinProcessTreeDigest(readyRoot)
	if err != nil {
		return "", fmt.Errorf("hash Kotlin ready tree: %w", err)
	}
	cohort := "kotlin-process-" + strings.ToLower(hex.EncodeToString(serverHash[:8]))
	targetRoot := filepath.Join(productRoot, cohort)
	targetBinary := filepath.Join(targetRoot, "bin", "intellij-server.exe")
	if err := validateKotlinWindowsProcessTargetPath(targetBinary); err != nil {
		return "", err
	}

	windowsKotlinProcessRootMu.Lock()
	defer windowsKotlinProcessRootMu.Unlock()
	if info, statErr := os.Lstat(targetBinary); statErr == nil {
		if isUnsafeAssetFile(info) || !info.Mode().IsRegular() {
			return "", fmt.Errorf("existing Kotlin process binary is unsafe")
		}
		got, hashErr := windowsKotlinProcessFileSHA256(targetBinary)
		if hashErr != nil || got != serverHash {
			return "", fmt.Errorf("existing Kotlin process binary digest mismatch")
		}
		targetTreeDigest, digestErr := windowsKotlinProcessTreeDigest(targetRoot)
		if digestErr != nil || targetTreeDigest != sourceTreeDigest {
			return "", fmt.Errorf("existing Kotlin process tree digest mismatch")
		}
		if aclErr := securefs.CheckExistingOwnerOnly(targetRoot, nil); aclErr != nil {
			return "", fmt.Errorf("existing Kotlin process root ACL validation failed: %w", securefs.WrapErrorForPath(aclErr, targetRoot))
		}
		return targetBinary, nil
	} else if !os.IsNotExist(statErr) {
		return "", wrapKotlinWindowsPathError(statErr, targetBinary)
	}

	stage, err := os.MkdirTemp(productRoot, ".kotlin-process-stage-")
	if err != nil {
		return "", fmt.Errorf("create Kotlin process staging root: %w", wrapKotlinWindowsPathError(err, productRoot))
	}
	defer func() {
		if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
			target = ""
			resultErr = errors.Join(resultErr, wrapKotlinWindowsPathError(cleanupErr, stage))
		}
	}()
	if err := securefs.RestrictOwnerOnly(stage, 0o700); err != nil {
		return "", fmt.Errorf("restrict Kotlin process staging ACL: %w", securefs.WrapErrorForPath(err, stage))
	}
	stageRoot := filepath.Join(stage, "bundle")
	if err := materializeWindowsKotlinProcessTree(productRoot, readyRoot, stageRoot); err != nil {
		return "", fmt.Errorf("materialize Kotlin process tree: %w", err)
	}
	stagedBinary := filepath.Join(stageRoot, "bin", "intellij-server.exe")
	got, err := windowsKotlinProcessFileSHA256(stagedBinary)
	if err != nil || got != serverHash {
		return "", fmt.Errorf("staged Kotlin process digest mismatch")
	}
	stagedTreeDigest, err := windowsKotlinProcessTreeDigest(stageRoot)
	if err != nil || stagedTreeDigest != sourceTreeDigest {
		return "", fmt.Errorf("staged Kotlin process tree digest mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(targetRoot), 0o700); err != nil {
		return "", wrapKotlinWindowsPathError(err, filepath.Dir(targetRoot))
	}
	if err := renameWindowsInstallerPathChecked(productRoot, stageRoot, targetRoot); err != nil {
		if info, statErr := os.Lstat(targetBinary); statErr == nil && !isUnsafeAssetFile(info) {
			if got, hashErr := windowsKotlinProcessFileSHA256(targetBinary); hashErr == nil && got == serverHash {
				if aclErr := securefs.CheckExistingOwnerOnly(targetRoot, nil); aclErr == nil {
					return targetBinary, nil
				}
			}
		}
		return "", fmt.Errorf("atomically publish Kotlin process root: %w", err)
	}
	if err := securefs.RestrictOwnerOnly(targetRoot, 0o700); err != nil {
		return "", fmt.Errorf("restrict Kotlin process root ACL: %w", securefs.WrapErrorForPath(err, targetRoot))
	}
	if len(filepath.Clean(targetBinary)) > 240 {
		return "", fmt.Errorf("Kotlin physical short process path remains too long: path_units=%d", len([]rune(filepath.Clean(targetBinary))))
	}
	return targetBinary, nil
}

func validateKotlinWindowsProcessTargetPath(targetBinary string) error {
	clean := filepath.Clean(targetBinary)
	if len(clean) > 240 {
		return fmt.Errorf("Kotlin physical short process path remains too long: path_units=%d", len([]rune(clean)))
	}
	return nil
}

func wrapKotlinWindowsPathError(err error, path string) error {
	if err == nil {
		return nil
	}
	return securefs.WrapErrorForPath(err, path)
}

func windowsKotlinProcessTreeDigest(root string) ([32]byte, error) {
	entries := make([]string, 0, 256)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return wrapKotlinWindowsPathError(err, path)
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("Kotlin process tree contains symlink or reparse point")
		}
		if !entry.IsDir() {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("Kotlin process tree contains unsupported file")
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return wrapKotlinWindowsPathError(err, path)
			}
			entries = append(entries, relative)
		}
		return nil
	})
	if err != nil {
		return [32]byte{}, err
	}
	sort.Strings(entries)
	digest := sha256.New()
	for _, relative := range entries {
		fileDigest, err := windowsKotlinProcessFileSHA256(filepath.Join(root, relative))
		if err != nil {
			return [32]byte{}, err
		}
		_, _ = io.WriteString(digest, filepath.ToSlash(relative))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(fileDigest[:])
		_, _ = digest.Write([]byte{0})
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func materializeWindowsKotlinProcessTree(root, source, destination string) error {
	if err := validateWindowsInstallerPathWithinRoot(root, source, false); err != nil {
		return securefs.WrapErrorForPath(err, source)
	}
	if err := ensureDirectoryNoSymlink(destination); err != nil {
		return securefs.WrapErrorForPath(err, destination)
	}
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return wrapKotlinWindowsPathError(err, path)
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("Kotlin ready tree contains symlink or reparse point")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return wrapKotlinWindowsPathError(err, path)
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
			return securefs.WrapErrorForPath(err, target)
		}
		if entry.IsDir() {
			if err := ensureDirectoryNoSymlink(target); err != nil {
				return securefs.WrapErrorForPath(err, target)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("Kotlin ready tree contains unsupported file")
		}
		if err := ensureDirectoryNoSymlink(filepath.Dir(target)); err != nil {
			return securefs.WrapErrorForPath(err, filepath.Dir(target))
		}
		input, err := os.Open(path)
		if err != nil {
			return wrapKotlinWindowsPathError(err, path)
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = input.Close()
			return wrapKotlinWindowsPathError(err, target)
		}
		_, copyErr := io.Copy(output, input)
		closeOutErr := output.Close()
		closeInErr := input.Close()
		if closeOutErr == nil {
			if aclErr := securefs.RestrictOwnerOnly(target, 0o600); aclErr != nil {
				return securefs.WrapErrorForPath(aclErr, target)
			}
		}
		return errors.Join(copyErr, closeOutErr, closeInErr)
	})
}

func windowsKotlinProcessFileSHA256(path string) ([32]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, wrapKotlinWindowsPathError(err, path)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return [32]byte{}, wrapKotlinWindowsPathError(err, path)
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}
