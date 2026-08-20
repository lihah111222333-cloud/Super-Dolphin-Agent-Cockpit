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

var windowsCSharpProcessRootMu sync.Mutex

// windowsCSharpProcessPathMaxUnits is the legacy Windows MAX_PATH budget
// excluding the terminating NUL. The physical process tree deliberately stays
// within this boundary even when the host supports extended-length paths.
const windowsCSharpProcessPathMaxUnits = 259

// MaterializeWindowsCSharpProcessRoot publishes a digest-isolated physical
// view of a product-owned C# ready cohort under the product root. The source
// cohort remains the identity fact; the published view is only the child
// process boundary and contains no symlink or reparse point.
func MaterializeWindowsCSharpProcessRoot(productRoot, cohortRoot string) (target string, resultErr error) {
	productRoot = filepath.Clean(strings.TrimSpace(productRoot))
	cohortRoot = filepath.Clean(strings.TrimSpace(cohortRoot))
	if productRoot == "." || !filepath.IsAbs(productRoot) || cohortRoot == "." || !filepath.IsAbs(cohortRoot) {
		return "", errors.New("C# process root requires absolute product and cohort paths")
	}
	if err := validateWindowsInstallerPathWithinRoot(productRoot, cohortRoot, false); err != nil {
		return "", fmt.Errorf("C# ready cohort escaped product root: %w", securefs.WrapErrorForPath(err, cohortRoot))
	}
	if info, err := os.Lstat(cohortRoot); err != nil {
		return "", fmt.Errorf("C# ready cohort is not a real directory: %w", securefs.WrapErrorForPath(err, cohortRoot))
	} else if isUnsafeAssetFile(info) || !info.IsDir() {
		return "", fmt.Errorf("C# ready cohort is not a real directory: %w", securefs.WrapErrorForPath(errors.New("not a real directory"), cohortRoot))
	}
	if err := ensureWindowsCSharpOwnerOnlyACL(cohortRoot); err != nil {
		return "", fmt.Errorf("C# ready cohort ACL validation failed: %w", securefs.WrapErrorForPath(err, cohortRoot))
	}
	sourceDigest, err := windowsCSharpProcessTreeDigest(cohortRoot)
	if err != nil {
		return "", fmt.Errorf("hash C# ready cohort: %w", err)
	}
	cohort := "cs-" + strings.ToLower(hex.EncodeToString(sourceDigest[:8]))
	targetRoot := filepath.Join(productRoot, cohort)
	if err := validateWindowsCSharpProcessTargetPath(targetRoot, ""); err != nil {
		return "", err
	}

	windowsCSharpProcessRootMu.Lock()
	defer windowsCSharpProcessRootMu.Unlock()

	if info, statErr := os.Lstat(targetRoot); statErr == nil {
		if isUnsafeAssetFile(info) || !info.IsDir() {
			return "", errors.New("existing C# process root is unsafe")
		}
		targetDigest, digestErr := windowsCSharpProcessTreeDigest(targetRoot)
		if digestErr != nil || targetDigest != sourceDigest {
			return "", errors.New("existing C# process root digest mismatch")
		}
		if aclErr := ensureWindowsCSharpOwnerOnlyACL(targetRoot); aclErr != nil {
			return "", fmt.Errorf("existing C# process root ACL validation failed: %w", securefs.WrapErrorForPath(aclErr, targetRoot))
		}
		return targetRoot, nil
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("inspect existing C# process root: %w", securefs.WrapErrorForPath(statErr, targetRoot))
	}

	stage, err := os.MkdirTemp(productRoot, ".csharp-process-stage-")
	if err != nil {
		return "", fmt.Errorf("create C# process staging root: %w", securefs.WrapErrorForPath(err, productRoot))
	}
	defer func() {
		if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
			target = ""
			resultErr = errors.Join(resultErr, securefs.WrapErrorForPath(cleanupErr, stage))
		}
	}()
	if err := securefs.RestrictOwnerOnly(stage, 0o700); err != nil {
		return "", fmt.Errorf("restrict C# process staging ACL: %w", securefs.WrapErrorForPath(err, stage))
	}
	stageRoot := filepath.Join(stage, "bundle")
	if err := materializeWindowsCSharpProcessTree(productRoot, cohortRoot, stageRoot, targetRoot); err != nil {
		return "", fmt.Errorf("materialize C# process tree: %w", err)
	}
	stagedDigest, err := windowsCSharpProcessTreeDigest(stageRoot)
	if err != nil || stagedDigest != sourceDigest {
		return "", errors.New("staged C# process tree digest mismatch")
	}
	if err := os.MkdirAll(filepath.Dir(targetRoot), 0o700); err != nil {
		return "", securefs.WrapErrorForPath(err, filepath.Dir(targetRoot))
	}
	if err := renameWindowsInstallerPathChecked(productRoot, stageRoot, targetRoot); err != nil {
		if info, statErr := os.Lstat(targetRoot); statErr == nil && !isUnsafeAssetFile(info) && info.IsDir() {
			if got, digestErr := windowsCSharpProcessTreeDigest(targetRoot); digestErr == nil && got == sourceDigest {
				if aclErr := ensureWindowsCSharpOwnerOnlyACL(targetRoot); aclErr == nil {
					return targetRoot, nil
				}
			}
		}
		return "", fmt.Errorf("atomically publish C# process root: %w", securefs.WrapErrorForPath(err, targetRoot))
	}
	if err := ensureWindowsCSharpOwnerOnlyACL(targetRoot); err != nil {
		return "", fmt.Errorf("restrict C# process root ACL: %w", securefs.WrapErrorForPath(err, targetRoot))
	}
	return targetRoot, nil
}

func ensureWindowsCSharpOwnerOnlyACL(path string) error {
	if err := securefs.CheckExistingOwnerOnly(path, nil); err != nil {
		if _, ok := securefs.ClassifyWindowsPermissionError(err); ok {
			return err
		}
		if restrictErr := securefs.RestrictOwnerOnly(path, 0o700); restrictErr != nil {
			return securefs.WrapErrorForPath(restrictErr, path)
		}
	}
	if err := securefs.CheckExistingOwnerOnly(path, nil); err != nil {
		return securefs.WrapErrorForPath(err, path)
	}
	return nil
}

func validateWindowsCSharpProcessTargetPath(targetPath, relative string) error {
	clean := filepath.Clean(targetPath)
	pathUnits := len([]rune(clean))
	if pathUnits <= windowsCSharpProcessPathMaxUnits {
		return nil
	}
	if relative == "" {
		return fmt.Errorf("C# physical process root too long: path_units=%d", pathUnits)
	}
	return fmt.Errorf("C# physical process path too long: path_units=%d relative=%q", pathUnits, filepath.ToSlash(relative))
}

func windowsCSharpProcessTreeDigest(root string) ([32]byte, error) {
	entries := make([]string, 0, 256)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return securefs.WrapErrorForPath(err, path)
		}
		if isUnsafeAssetFile(info) {
			return errors.New("C# process tree contains symlink or reparse point")
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("C# process tree contains unsupported file")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return securefs.WrapErrorForPath(err, path)
		}
		entries = append(entries, relative)
		return nil
	}); err != nil {
		return [32]byte{}, err
	}
	sort.Strings(entries)
	digest := sha256.New()
	for _, relative := range entries {
		fileDigest, err := windowsCSharpProcessFileSHA256(filepath.Join(root, relative))
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

func materializeWindowsCSharpProcessTree(root, source, destination, targetRoot string) error {
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
			return securefs.WrapErrorForPath(err, path)
		}
		if isUnsafeAssetFile(info) {
			return errors.New("C# ready tree contains symlink or reparse point")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return securefs.WrapErrorForPath(err, path)
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(destination, relative)
		publishedTarget := filepath.Join(targetRoot, relative)
		if err := validateWindowsCSharpProcessTargetPath(publishedTarget, relative); err != nil {
			return err
		}
		if entry.IsDir() {
			if err := ensureDirectoryNoSymlink(target); err != nil {
				return securefs.WrapErrorForPath(err, target)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("C# ready tree contains unsupported file")
		}
		if err := ensureDirectoryNoSymlink(filepath.Dir(target)); err != nil {
			return securefs.WrapErrorForPath(err, filepath.Dir(target))
		}
		if err := validateWindowsInstallerPathWithinRoot(root, target, true); err != nil {
			return securefs.WrapErrorForPath(err, target)
		}
		// Always copy the ready cohort. Hardlinks would let a child mutation write
		// through to the canonical receipt and invalidate the verified source.
		// Junctions, subst paths, and other aliases are likewise intentionally not used.
		input, err := os.Open(path)
		if err != nil {
			return securefs.WrapErrorForPath(err, path)
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = input.Close()
			return securefs.WrapErrorForPath(err, target)
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

func windowsCSharpProcessFileSHA256(path string) ([32]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return [32]byte{}, securefs.WrapErrorForPath(err, path)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return [32]byte{}, securefs.WrapErrorForPath(err, path)
	}
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}
