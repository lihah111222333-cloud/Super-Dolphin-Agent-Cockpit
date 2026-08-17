//go:build windows

package installer

// Windows tar.xz 解压必须通过受控、可取消的系统 tar；失败或版本不支持时直接阻断，不退回不可取消的纯 Go 路径。

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

const windowsTarListingLimit = 16 << 20

func extractTarXzPayload(ctx context.Context, payloadPath, outputRoot string, maxArchiveBytes int64) error {
	if ctx == nil {
		return errors.New("tar.xz extraction context is nil")
	}
	tarPath, err := windowsSystemTarPath()
	if err != nil {
		return err
	}
	started := time.Now()
	entries, err := windowsTarList(ctx, tarPath, payloadPath)
	if err != nil {
		return fmt.Errorf("list locked asset tar.xz (compressed_bytes=%d elapsed=%s output_root_units=%d): %w", fileSize(payloadPath), time.Since(started).Round(time.Millisecond), len([]rune(outputRoot)), err)
	}
	for _, entry := range entries {
		if err := validateTarEntryName(entry); err != nil {
			return err
		}
	}
	if err := windowsTarRejectSpecialEntries(ctx, tarPath, payloadPath); err != nil {
		return err
	}
	if err := windowsTarExtract(ctx, tarPath, payloadPath, outputRoot); err != nil {
		return fmt.Errorf("extract locked asset tar.xz (compressed_bytes=%d entries=%d elapsed=%s output_root_units=%d): %w", fileSize(payloadPath), len(entries), time.Since(started).Round(time.Millisecond), len([]rune(outputRoot)), err)
	}
	if err := validateExtractedTarTree(outputRoot, maxArchiveBytes); err != nil {
		return err
	}
	return nil
}

func windowsTarRejectSpecialEntries(ctx context.Context, tarPath, payloadPath string) error {
	cmd := hiddenexec.CommandContext(ctx, tarPath, "-tvf", payloadPath)
	var stdout bytes.Buffer
	cmd.Stdout = &limitedBuffer{buffer: &stdout, limit: windowsTarListingLimit}
	var stderr bytes.Buffer
	cmd.Stderr = &limitedBuffer{buffer: &stderr, limit: 1 << 20}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tar type listing failed (%s): %w", windowsTarStderrSummary(stderr.Bytes()), err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line[0] {
		case 'l', 'p', 'c', 'b', 's':
			return fmt.Errorf("%w: tar archive contains link or special entry", ErrWindowsUnsafeAssetArchive)
		}
	}
	return scanner.Err()
}

func windowsSystemTarPath() (string, error) {
	root := strings.TrimSpace(os.Getenv("SystemRoot"))
	if root == "" {
		return "", errors.New("SystemRoot is empty; refusing tar.xz extraction")
	}
	path := filepath.Join(root, "System32", "tar.exe")
	if err := validateWindowsInstallerExistingFile(path); err != nil {
		return "", fmt.Errorf("Windows tar.exe is unavailable: %w", err)
	}
	return path, nil
}

func windowsTarList(ctx context.Context, tarPath, payloadPath string) ([]string, error) {
	cmd := hiddenexec.CommandContext(ctx, tarPath, "-tf", payloadPath)
	var stdout bytes.Buffer
	cmd.Stdout = &limitedBuffer{buffer: &stdout, limit: windowsTarListingLimit}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("tar list failed (%s): %w", windowsTarStderrSummary(stderr.Bytes()), err)
	}
	var entries []string
	scanner := bufio.NewScanner(bytes.NewReader(stdout.Bytes()))
	for scanner.Scan() {
		entry := strings.TrimSuffix(scanner.Text(), "\r")
		if strings.TrimSpace(entry) == "" {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read tar listing: %w", err)
	}
	return entries, nil
}

func windowsTarExtract(ctx context.Context, tarPath, payloadPath, outputRoot string) error {
	cmd := hiddenexec.CommandContext(ctx, tarPath, "-xf", payloadPath, "-C", outputRoot, "--no-same-owner", "--no-same-permissions")
	var stderr limitedBuffer
	stderr.limit = 1 << 20
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var diagnostic []byte
		if stderr.buffer != nil {
			diagnostic = stderr.buffer.Bytes()
		}
		return fmt.Errorf("tar extract failed (%s): %w", windowsTarStderrSummary(diagnostic), err)
	}
	return nil
}

// windowsTarStderrSummary 只保留解压阶段可裁决的低敏 stderr 事实；不输出完整命令、环境或路径。
// 这是 Windows tar 边界的诊断合同，非 Windows 继续使用共享的纯 Go 解压实现。
func windowsTarStderrSummary(stderr []byte) string {
	digest := sha256.Sum256(stderr)
	class := "empty"
	lower := strings.ToLower(string(stderr))
	switch {
	case strings.Contains(lower, "no space") || strings.Contains(lower, "disk full"):
		class = "disk_space_exhausted"
	case strings.Contains(lower, "access is denied") || strings.Contains(lower, "permission denied"):
		class = "authorization_required"
	case strings.Contains(lower, "cannot open") || strings.Contains(lower, "failed to open"):
		class = "open_failed"
	case len(bytes.TrimSpace(stderr)) > 0:
		class = "tar_error"
	}
	words := strings.Fields(string(stderr))
	kept := make([]string, 0, 12)
	for i := len(words) - 1; i >= 0 && len(kept) < 12; i-- {
		word := words[i]
		if strings.ContainsAny(word, `\\/:`) {
			continue
		}
		kept = append(kept, word)
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	tail := strings.Join(kept, " ")
	if len(tail) > 160 {
		tail = tail[len(tail)-160:]
	}
	return fmt.Sprintf("stderr_bytes=%d stderr_sha256=%x stderr_class=%s stderr_tail=%q", len(stderr), digest, class, tail)
}

func validateTarEntryName(entry string) error {
	if strings.Contains(entry, ":") || strings.ContainsRune(entry, 0) {
		return fmt.Errorf("%w: tar entry contains drive/ADS syntax", ErrWindowsUnsafeAssetArchive)
	}
	_, err := normalizeArchiveEntryName(entry)
	return err
}

func validateExtractedTarTree(root string, maxArchiveBytes int64) error {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if isUnsafeAssetFile(info) {
			return fmt.Errorf("%w: extracted tar tree contains link or reparse point", ErrWindowsUnsafeAssetArchive)
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			if total > maxArchiveBytes {
				return fmt.Errorf("locked asset tar exceeds archive limit %d bytes", maxArchiveBytes)
			}
		}
		return nil
	})
	return err
}

type limitedBuffer struct {
	buffer *bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.buffer == nil {
		b.buffer = &bytes.Buffer{}
	}
	if len(p) > b.limit-b.buffer.Len() {
		return 0, fmt.Errorf("tar diagnostic output exceeds limit")
	}
	return b.buffer.Write(p)
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

var _ io.Writer = (*limitedBuffer)(nil)
