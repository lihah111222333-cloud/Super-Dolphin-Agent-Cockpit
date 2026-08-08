package gate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// installFrontendRuntimeOverlay 把依赖与 Vite cache 直接链接到 accepted 镜像层。
func installFrontendRuntimeOverlay(seedRoot string, viteSeedRoot string, targetRoot string) error {
	if _, err := os.Lstat(targetRoot); !errors.Is(err, os.ErrNotExist) {
		return errors.New("runtime seed target already exists")
	}
	if err := os.Mkdir(targetRoot, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(seedRoot)
	if err != nil {
		return err
	}
	reserved := map[string]bool{".vite": true, ".vite-temp": true}
	for _, entry := range entries {
		if reserved[entry.Name()] {
			return fmt.Errorf("runtime seed reserves frontend overlay entry %q", entry.Name())
		}
		if err := os.Symlink(filepath.Join(seedRoot, entry.Name()), filepath.Join(targetRoot, entry.Name())); err != nil {
			return err
		}
	}
	if _, err := trustedDirectory(viteSeedRoot, false, -1); err != nil {
		return err
	}
	if err := os.Symlink(viteSeedRoot, filepath.Join(targetRoot, ".vite")); err != nil {
		return err
	}
	return nil
}

// installFrontendViteCacheOverlay 在分片私有 .vite-temp 中只链接镜像 deps，允许 Vite 失效时替换该链接。
func installFrontendViteCacheOverlay(viteSeedRoot string, privateCacheRoot string) error {
	seedRoot, err := validateFrontendViteCacheRoots(viteSeedRoot, privateCacheRoot)
	if err != nil {
		return err
	}
	depsSeedRoot, err := trustedDirectory(filepath.Join(seedRoot, "deps"), false, -1)
	if err != nil {
		return fmt.Errorf("Vite cache seed deps directory: %w", err)
	}
	if err := os.Mkdir(privateCacheRoot, 0o700); err != nil {
		return err
	}
	if err := os.Symlink(depsSeedRoot, filepath.Join(privateCacheRoot, "deps")); err != nil {
		return err
	}
	return nil
}

// validateFrontendViteCacheRoots 校验镜像种子与分片私有缓存根目录的边界和所有权。
func validateFrontendViteCacheRoots(viteSeedRoot string, privateCacheRoot string) (string, error) {
	if !filepath.IsAbs(privateCacheRoot) || filepath.Clean(privateCacheRoot) != privateCacheRoot ||
		filepath.Base(privateCacheRoot) != ".vite-temp" {
		return "", errors.New("private Vite cache path must be a canonical absolute .vite-temp directory")
	}
	seedRoot, err := trustedDirectory(viteSeedRoot, false, -1)
	if err != nil {
		return "", fmt.Errorf("Vite cache seed directory: %w", err)
	}
	privateParent, err := trustedDirectory(filepath.Dir(privateCacheRoot), false, os.Geteuid())
	if err != nil {
		return "", fmt.Errorf("private Vite cache parent: %w", err)
	}
	if rootsOverlap(seedRoot, privateParent) || rootsOverlap(seedRoot, privateCacheRoot) {
		return "", errors.New("Vite cache seed and private cache must be disjoint")
	}
	return seedRoot, nil
}

// installFrontendRuntimeOverlays 安装前端依赖的镜像只读链接和分片私有 Vite 缓存。
func installFrontendRuntimeOverlays(seedPath string, viteSeedRoot string, targetRoot string, privateCacheRoot string) error {
	if err := installFrontendRuntimeOverlay(seedPath, viteSeedRoot, targetRoot); err != nil {
		return fmt.Errorf("install frontend runtime overlay: %w", err)
	}
	if err := installFrontendViteCacheOverlay(viteSeedRoot, privateCacheRoot); err != nil {
		return fmt.Errorf("install frontend Vite cache overlay: %w", err)
	}
	return nil
}
