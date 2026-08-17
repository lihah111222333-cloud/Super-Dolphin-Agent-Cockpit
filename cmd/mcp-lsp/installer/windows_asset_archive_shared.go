package installer

// 本文件故意不加 windows build tag：Windows 资产归档的路径穿越与大小门禁
// 是跨平台可复核的纯格式逻辑；平台专用文件打开/重解析点检查由带标签实现提供。

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractZipAsset 将 ZIP 归档解包到临时输出目录，并拒绝危险条目。
func extractZipAsset(payloadPath, outputRoot, binaryPath string, maxArchiveBytes int64) (err error) {
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset ZIP payload: %w", err)
	}
	if err := ensureDirectoryNoSymlink(outputRoot); err != nil {
		return fmt.Errorf("validate locked asset ZIP output root: %w", err)
	}
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset ZIP payload before open: %w", err)
	}
	reader, err := zip.OpenReader(payloadPath)
	if err != nil {
		return fmt.Errorf("open locked asset ZIP: %w", err)
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, "close locked asset ZIP reader")
		}
	}()
	seen := make(map[string]struct{}, len(reader.File))
	var total int64
	for _, entry := range reader.File {
		name, err := normalizeArchiveEntryName(entry.Name)
		if err != nil {
			return err
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: duplicate ZIP entry %q", ErrWindowsUnsafeAssetArchive, entry.Name)
		}
		seen[name] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 || mode&os.ModeDevice != 0 {
			return fmt.Errorf("%w: ZIP entry %q is a link or special file", ErrWindowsUnsafeAssetArchive, entry.Name)
		}
		destination, err := safeOutputPath(outputRoot, name)
		if err != nil {
			return fmt.Errorf("ZIP entry %q: %w", entry.Name, err)
		}
		if entry.FileInfo().IsDir() {
			if err := ensureDirectoryNoSymlink(destination); err != nil {
				return fmt.Errorf("create ZIP directory %q: %w", entry.Name, err)
			}
			if err := validateWindowsInstallerPathWithinRoot(outputRoot, destination, false); err != nil {
				return fmt.Errorf("validate ZIP directory %q: %w", entry.Name, err)
			}
			continue
		}
		if entry.UncompressedSize64 > uint64(maxArchiveBytes) || int64(entry.UncompressedSize64) > maxArchiveBytes-total {
			return fmt.Errorf("locked asset ZIP exceeds archive limit %d bytes", maxArchiveBytes)
		}
		if err := ensureDirectoryNoSymlink(filepath.Dir(destination)); err != nil {
			return fmt.Errorf("create ZIP parent for %q: %w", entry.Name, err)
		}
		if err := validateWindowsInstallerPathWithinRoot(outputRoot, destination, true); err != nil {
			return fmt.Errorf("validate ZIP entry destination %q: %w", entry.Name, err)
		}
		output, err := openWindowsInstallerOutput(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return fmt.Errorf("create ZIP entry %q: %w", entry.Name, err)
		}
		member, openErr := entry.Open()
		if openErr != nil {
			operationErr := fmt.Errorf("open ZIP entry %q: %w", entry.Name, openErr)
			operationErr = joinWindowsInstallerCleanupError(operationErr, output.Close(), fmt.Sprintf("close incomplete ZIP entry %q", entry.Name))
			operationErr = joinWindowsInstallerCleanupError(operationErr, removeWindowsInstallerPathChecked(outputRoot, destination), fmt.Sprintf("remove incomplete ZIP entry %q", entry.Name))
			return operationErr
		}
		memberLimit := int64(entry.UncompressedSize64)
		if memberLimit < maxInt64Value {
			memberLimit++
		}
		copied, copyErr := io.Copy(output, io.LimitReader(member, memberLimit))
		memberCloseErr := member.Close()
		closeErr := output.Close()
		var operationErr error
		if copyErr != nil {
			operationErr = fmt.Errorf("extract ZIP entry %q: %w", entry.Name, copyErr)
		}
		operationErr = joinWindowsInstallerCleanupError(operationErr, closeErr, fmt.Sprintf("close ZIP entry %q", entry.Name))
		operationErr = joinWindowsInstallerCleanupError(operationErr, memberCloseErr, fmt.Sprintf("close ZIP input entry %q", entry.Name))
		if operationErr != nil {
			return operationErr
		}
		if copied != int64(entry.UncompressedSize64) {
			return fmt.Errorf("%w: ZIP entry %q size changed while extracting", ErrWindowsUnsafeAssetArchive, entry.Name)
		}
		total += copied
	}
	return nil
}

// extractTarGzAsset 将 tar.gz 归档解包到临时输出目录，并拒绝链接和特殊文件。
func extractTarGzAsset(ctx context.Context, payloadPath, outputRoot, binaryPath string, maxArchiveBytes int64) (err error) {
	if ctx == nil {
		return fmt.Errorf("extract locked asset tar.gz: nil context")
	}
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset tar.gz payload: %w", err)
	}
	if err := ensureDirectoryNoSymlink(outputRoot); err != nil {
		return fmt.Errorf("validate locked asset tar.gz output root: %w", err)
	}
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset tar.gz payload before open: %w", err)
	}
	payload, err := openWindowsInstallerInput(payloadPath)
	if err != nil {
		return fmt.Errorf("open locked asset tar.gz: %w", err)
	}
	defer func() {
		if closeErr := payload.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, "close locked asset tar.gz payload")
		}
	}()
	gzipReader, err := gzip.NewReader(payload)
	if err != nil {
		return fmt.Errorf("open locked asset gzip stream: %w", err)
	}
	defer func() {
		if closeErr := gzipReader.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, "close locked asset gzip stream")
		}
	}()
	return extractTarReader(ctx, gzipReader, outputRoot, maxArchiveBytes)
}

// extractTarXzAsset 先做统一路径/目录门禁，再交给平台实现；Windows 使用可取消的受控 tar，非 Windows 保持纯 Go 解压。
func extractTarXzAsset(ctx context.Context, payloadPath, outputRoot, binaryPath string, maxArchiveBytes int64) (err error) {
	if ctx == nil {
		return fmt.Errorf("extract locked asset tar.xz: nil context")
	}
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset tar.xz payload: %w", err)
	}
	if err := ensureDirectoryNoSymlink(outputRoot); err != nil {
		return fmt.Errorf("validate locked asset tar.xz output root: %w", err)
	}
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset tar.xz payload before open: %w", err)
	}
	return extractTarXzPayload(ctx, payloadPath, outputRoot, maxArchiveBytes)
}

// contextArchiveReader 是跨平台的取消边界：不改变归档格式与路径校验，只让长解压在 deadline/cancel 后尽快失败。
type contextArchiveReader struct {
	ctx   context.Context
	input io.Reader
}

func (r contextArchiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.input.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return n, ctxErr
	}
	return n, err
}

func extractTarReader(ctx context.Context, input io.Reader, outputRoot string, maxArchiveBytes int64) error {
	if ctx == nil {
		return fmt.Errorf("extract locked asset tar: nil context")
	}
	tarReader := tar.NewReader(contextArchiveReader{ctx: ctx, input: input})
	seen := make(map[string]struct{})
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("extract locked asset tar after %d decompressed bytes: %w", total, err)
		}
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read locked asset tar header: %w", nextErr)
		}
		name, err := normalizeArchiveEntryName(header.Name)
		if err != nil {
			return err
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: duplicate tar entry %q", ErrWindowsUnsafeAssetArchive, header.Name)
		}
		seen[name] = struct{}{}
		switch header.Typeflag {
		case tar.TypeDir:
			destination, pathErr := safeOutputPath(outputRoot, name)
			if pathErr != nil {
				return fmt.Errorf("tar directory %q: %w", header.Name, pathErr)
			}
			if err := ensureDirectoryNoSymlink(destination); err != nil {
				return fmt.Errorf("create tar directory %q: %w", header.Name, err)
			}
			if err := validateWindowsInstallerPathWithinRoot(outputRoot, destination, false); err != nil {
				return fmt.Errorf("validate tar directory %q: %w", header.Name, err)
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxArchiveBytes || header.Size > maxArchiveBytes-total {
				return fmt.Errorf("locked asset tar exceeds archive limit %d bytes", maxArchiveBytes)
			}
			destination, pathErr := safeOutputPath(outputRoot, name)
			if pathErr != nil {
				return fmt.Errorf("tar entry %q: %w", header.Name, pathErr)
			}
			if err := ensureDirectoryNoSymlink(filepath.Dir(destination)); err != nil {
				return fmt.Errorf("create tar parent for %q: %w", header.Name, err)
			}
			if err := validateWindowsInstallerPathWithinRoot(outputRoot, destination, true); err != nil {
				return fmt.Errorf("validate tar entry destination %q: %w", header.Name, err)
			}
			output, createErr := openWindowsInstallerOutput(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
			if createErr != nil {
				return fmt.Errorf("create tar entry %q: %w", header.Name, createErr)
			}
			copied, copyErr := io.CopyN(output, contextArchiveReader{ctx: ctx, input: tarReader}, header.Size)
			closeErr := output.Close()
			var operationErr error
			if copyErr != nil {
				operationErr = fmt.Errorf("extract tar entry %q: %w", header.Name, copyErr)
			}
			operationErr = joinWindowsInstallerCleanupError(operationErr, closeErr, fmt.Sprintf("close tar entry %q", header.Name))
			if operationErr != nil {
				return operationErr
			}
			if copied != header.Size {
				return fmt.Errorf("%w: tar entry %q size changed while extracting", ErrWindowsUnsafeAssetArchive, header.Name)
			}
			total += copied
		default:
			return fmt.Errorf("%w: tar entry %q has unsupported type %d", ErrWindowsUnsafeAssetArchive, header.Name, header.Typeflag)
		}
	}
	return nil
}

func normalizeArchiveEntryName(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("%w: archive entry has an empty name", ErrWindowsUnsafeAssetArchive)
	}
	clean, err := normalizeAssetRelativePath(raw)
	if err != nil {
		return "", fmt.Errorf("%w: archive entry %q: %w", ErrWindowsUnsafeAssetArchive, raw, err)
	}
	return clean, nil
}
