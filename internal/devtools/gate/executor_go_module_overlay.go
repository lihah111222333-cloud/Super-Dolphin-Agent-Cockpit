package gate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// mirrorGoModuleMetadataTree 创建私有目录并把既有元数据文件链接到只读 seed。
func mirrorGoModuleMetadataTree(sharedRoot string, privateRoot string) error {
	return filepath.WalkDir(sharedRoot, func(sharedPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sharedRoot, sharedPath)
		if err != nil {
			return err
		}
		privatePath := filepath.Join(privateRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(privatePath, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("shared Go module metadata entry %q is not regular", relative)
		}
		if goModuleMetadataNeedsPrivateCopy(relative) {
			return copyPrivateGoModuleMetadataFile(sharedPath, privatePath)
		}
		return os.Symlink(sharedPath, privatePath)
	})
}

// goModuleMetadataNeedsPrivateCopy 识别 Go 会在只读解析期间更新的小型下载状态文件。
func goModuleMetadataNeedsPrivateCopy(relative string) bool {
	name := filepath.Base(relative)
	return name == "list" || strings.HasSuffix(name, ".lock")
}

// copyPrivateGoModuleMetadataFile 流式复制可变元数据，避免私有写入穿透到共享种子。
func copyPrivateGoModuleMetadataFile(sourcePath string, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.Join(err, source.Close())
	}
	_, copyErr := io.Copy(target, source)
	closeErr := errors.Join(target.Close(), source.Close())
	if err := errors.Join(copyErr, closeErr); err != nil {
		return errors.Join(err, os.Remove(targetPath))
	}
	return nil
}
