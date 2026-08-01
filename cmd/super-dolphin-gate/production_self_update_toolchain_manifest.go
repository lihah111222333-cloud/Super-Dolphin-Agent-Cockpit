package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// productionGoToolManifestDigest 绑定 Go 实际会调用的 compile、link、asm 等完整工具目录。
func productionGoToolManifestDigest(directory string) (string, error) {
	resolved, err := resolveProductionGoDirectory("GOTOOLDIR", directory)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", errors.New("Go tool manifest is empty")
	}
	var manifest strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || strings.ContainsAny(entry.Name(), "\r\n\x00") {
			return "", errors.New("Go tool manifest contains an invalid entry")
		}
		path := filepath.Join(resolved, entry.Name())
		canonical, err := canonicalProductionToolPath("Go tool", path)
		if err != nil {
			return "", err
		}
		digest, err := productionBinaryDigest(canonical)
		if err != nil {
			return "", err
		}
		manifest.WriteString(entry.Name())
		manifest.WriteByte(0)
		manifest.WriteString(digest)
		manifest.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(manifest.String()))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// productionGoDistributionDigest 绑定空构建缓存会读取的标准库源码、汇编头和运行时数据。
func productionGoDistributionDigest(goRoot string) (string, error) {
	resolved, err := resolveProductionGoDirectory("GOROOT", goRoot)
	if err != nil {
		return "", err
	}
	paths := []string{filepath.Join(resolved, "VERSION"), filepath.Join(resolved, "go.env")}
	for _, directory := range []string{
		filepath.Join(resolved, "src"),
		filepath.Join(resolved, "lib"),
		filepath.Join(resolved, "pkg", "include"),
	} {
		walked, err := productionGoDistributionFiles(resolved, directory)
		if err != nil {
			return "", err
		}
		paths = append(paths, walked...)
	}
	slices.Sort(paths)
	var manifest strings.Builder
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.Join(fmt.Errorf("Go distribution file is invalid: %q", path), err)
		}
		digest, err := productionBinaryDigest(path)
		if err != nil {
			return "", err
		}
		relative, err := filepath.Rel(resolved, path)
		if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
			return "", errors.Join(errors.New("Go distribution path is invalid"), err)
		}
		manifest.WriteString(filepath.ToSlash(relative))
		manifest.WriteByte(0)
		manifest.WriteString(digest)
		manifest.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(manifest.String()))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func productionGoDistributionFiles(root string, directory string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Go distribution contains a symbolic link: %q", path)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Go distribution contains a non-regular file: %q", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(relative, "..") {
			return errors.Join(errors.New("Go distribution entry escapes GOROOT"), err)
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}
