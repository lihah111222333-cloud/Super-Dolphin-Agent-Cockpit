//go:build windows

package installer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
)

const windowsRuntimeDependencyARM64BCJAlignment = 4

type windowsRuntimeDependencyARM64BCJReader struct {
	rc  io.ReadCloser
	buf bytes.Buffer
	n   int
	ip  uint32
}

func init() {
	sevenzip.RegisterDecompressor([]byte{0x0a}, windowsRuntimeDependencyARM64BCJDecompressor)
}

func windowsRuntimeDependencyARM64BCJDecompressor(_ []byte, _ uint64, readers []io.ReadCloser) (io.ReadCloser, error) {
	if len(readers) != 1 {
		return nil, errors.New("ARM64 BCJ filter requires exactly one reader")
	}
	return &windowsRuntimeDependencyARM64BCJReader{rc: readers[0]}, nil
}

func (reader *windowsRuntimeDependencyARM64BCJReader) Close() error {
	if reader.rc == nil {
		return nil
	}
	err := reader.rc.Close()
	reader.rc = nil
	return err
}

func (reader *windowsRuntimeDependencyARM64BCJReader) Read(p []byte) (int, error) {
	if reader.rc == nil {
		return 0, errors.New("ARM64 BCJ filter read after close")
	}
	want := len(p)
	if want < windowsRuntimeDependencyARM64BCJAlignment {
		want = windowsRuntimeDependencyARM64BCJAlignment
	}
	if missing := want - reader.buf.Len(); missing > 0 {
		if _, err := io.CopyN(&reader.buf, reader.rc, int64(missing)); err != nil {
			if !errors.Is(err, io.EOF) {
				return 0, err
			}
			if reader.buf.Len() < windowsRuntimeDependencyARM64BCJAlignment {
				reader.n = reader.buf.Len()
			}
		}
	}
	reader.n += reader.convert(reader.buf.Bytes()[reader.n:], false)
	read, err := reader.buf.Read(p[:min(reader.n, len(p))])
	reader.n -= read
	return read, err
}

func (reader *windowsRuntimeDependencyARM64BCJReader) convert(data []byte, encoding bool) int {
	if len(data) < windowsRuntimeDependencyARM64BCJAlignment {
		return 0
	}
	var index int
	for index = 0; index < len(data)&^(windowsRuntimeDependencyARM64BCJAlignment-1); index, reader.ip = index+windowsRuntimeDependencyARM64BCJAlignment, reader.ip+windowsRuntimeDependencyARM64BCJAlignment {
		value := binary.LittleEndian.Uint32(data[index:])
		if (value-0x94000000)&0xfc000000 == 0 {
			if encoding {
				value += reader.ip >> 2
			} else {
				value -= reader.ip >> 2
			}
			value &= 0x03ffffff
			value |= 0x94000000
			binary.LittleEndian.PutUint32(data[index:], value)
			continue
		}

		value -= 0x90000000
		if value&0x9f000000 == 0 {
			const (
				flag = uint32(1) << (24 - 4)
				mask = uint32(1)<<24 - flag<<1
			)
			value += flag
			if value&mask > 0 {
				continue
			}
			z, ip := value&0xffffffe0|value>>26, (reader.ip>>(12-3))&^uint32(7)
			if encoding {
				z += ip
			} else {
				z -= ip
			}
			value &= 0x1f
			value |= 0x90000000
			value |= z << 26
			value |= 0x00ffffe0 & ((z & (flag<<1 - 1)) - flag)
			binary.LittleEndian.PutUint32(data[index:], value)
		}
	}
	return index
}

// extractWindowsRuntimeDependencySevenZipAsset 使用固定纯 Go 7z reader 解包资产，
// 在写盘前拒绝路径穿越、重复项、链接、特殊文件、重解析目录和超大展开结果。
func extractWindowsRuntimeDependencySevenZipAsset(payloadPath, outputRoot string, maxArchiveBytes int64) (err error) {
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset 7z payload: %w", err)
	}
	if err := ensureWindowsRuntimeDependencyArchiveDirectory(outputRoot); err != nil {
		return fmt.Errorf("prepare 7z output root: %w", err)
	}
	if err := validateWindowsInstallerExistingFile(payloadPath); err != nil {
		return fmt.Errorf("validate locked asset 7z payload before open: %w", err)
	}
	reader, err := sevenzip.OpenReader(payloadPath)
	if err != nil {
		return fmt.Errorf("open locked asset 7z: %w", err)
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			err = joinWindowsInstallerCleanupError(err, closeErr, "close locked asset 7z reader")
		}
	}()
	seen := make(map[string]struct{}, len(reader.File))
	var total int64
	for _, entry := range reader.File {
		name, err := normalizeArchiveEntryName(entry.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%w: duplicate 7z entry %q", ErrWindowsUnsafeAssetArchive, entry.Name)
		}
		seen[name] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || mode&os.ModeNamedPipe != 0 || mode&os.ModeSocket != 0 || mode&os.ModeDevice != 0 {
			return fmt.Errorf("%w: 7z entry %q is a link or special file", ErrWindowsUnsafeAssetArchive, entry.Name)
		}
		destination, err := safeOutputPath(outputRoot, name)
		if err != nil {
			return fmt.Errorf("7z entry %q: %w", entry.Name, err)
		}
		fileInfo := entry.FileInfo()
		if fileInfo.IsDir() {
			if err := ensureWindowsRuntimeDependencyArchiveDirectory(destination); err != nil {
				return fmt.Errorf("create 7z directory %q: %w", entry.Name, err)
			}
			continue
		}
		size := fileInfo.Size()
		if size < 0 || size > maxArchiveBytes || size > maxArchiveBytes-total {
			return fmt.Errorf("locked asset 7z exceeds archive limit %d bytes", maxArchiveBytes)
		}
		if err := ensureWindowsRuntimeDependencyArchiveDirectory(filepath.Dir(destination)); err != nil {
			return fmt.Errorf("create 7z parent for %q: %w", entry.Name, err)
		}
		if err := validateWindowsInstallerPathWithinRoot(outputRoot, destination, true); err != nil {
			return fmt.Errorf("validate 7z entry destination %q: %w", entry.Name, err)
		}
		output, err := openWindowsInstallerOutput(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return fmt.Errorf("create 7z entry %q: %w", entry.Name, err)
		}
		member, err := entry.Open()
		if err != nil {
			operationErr := fmt.Errorf("open 7z entry %q: %w", entry.Name, err)
			operationErr = joinWindowsInstallerCleanupError(operationErr, output.Close(), fmt.Sprintf("close incomplete 7z entry %q", entry.Name))
			operationErr = joinWindowsInstallerCleanupError(operationErr, removeWindowsInstallerPathChecked(outputRoot, destination), fmt.Sprintf("remove incomplete 7z entry %q", entry.Name))
			return operationErr
		}
		memberLimit := size
		if memberLimit < maxInt64Value {
			memberLimit++
		}
		copied, copyErr := io.Copy(output, io.LimitReader(member, memberLimit))
		memberCloseErr := member.Close()
		closeErr := output.Close()
		var operationErr error
		if copyErr != nil {
			operationErr = fmt.Errorf("extract 7z entry %q: %w", entry.Name, copyErr)
		}
		operationErr = joinWindowsInstallerCleanupError(operationErr, closeErr, fmt.Sprintf("close 7z entry %q", entry.Name))
		operationErr = joinWindowsInstallerCleanupError(operationErr, memberCloseErr, fmt.Sprintf("close 7z input entry %q", entry.Name))
		if operationErr != nil {
			return operationErr
		}
		if copied != size {
			return fmt.Errorf("%w: 7z entry %q size changed while extracting", ErrWindowsUnsafeAssetArchive, entry.Name)
		}
		writtenInfo, statErr := os.Lstat(destination)
		if statErr != nil {
			return fmt.Errorf("inspect extracted 7z entry %q: %w", entry.Name, statErr)
		}
		if isUnsafeAssetFile(writtenInfo) || !writtenInfo.Mode().IsRegular() {
			return fmt.Errorf("%w: extracted 7z entry %q is not a regular file", ErrWindowsUnsafeAssetArchive, entry.Name)
		}
		total += copied
	}
	return nil
}

func ensureWindowsRuntimeDependencyArchiveDirectory(target string) error {
	target, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve archive directory %q: %w", target, err)
	}
	target = filepath.Clean(target)
	if err := ensureDirectoryNoSymlink(target); err != nil {
		return err
	}
	volume := filepath.VolumeName(target)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(target, current)
	if relative == target {
		return fmt.Errorf("archive directory path has an unsupported volume root: %q", target)
	}
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		if segment == "" || segment == "." {
			continue
		}
		current = filepath.Join(current, segment)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return fmt.Errorf("inspect archive directory component %q: %w", current, statErr)
		}
		if isUnsafeAssetFile(info) || !info.IsDir() {
			return fmt.Errorf("%w: archive directory component is unsafe: %q", ErrWindowsUnsafeAssetArchive, current)
		}
	}
	return nil
}
