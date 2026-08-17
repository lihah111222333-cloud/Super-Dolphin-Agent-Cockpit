//go:build !windows

package installer

// 本文件仅提供非 Windows 的 tar.xz 纯 Go 实现；Windows 使用同名平台实现调用受控 tar.exe，避免平台行为互相泄漏。

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ulikunitz/xz"
)

func extractTarXzPayload(ctx context.Context, payloadPath, outputRoot string, maxArchiveBytes int64) error {
	payload, err := openWindowsInstallerInput(payloadPath)
	if err != nil {
		return fmt.Errorf("open locked asset tar.xz: %w", err)
	}
	defer payload.Close()
	compressed := &countingArchiveReader{input: payload}
	xzReader, err := xz.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("open locked asset xz stream: %w", err)
	}
	started := time.Now()
	if err := extractTarReader(ctx, xzReader, outputRoot, maxArchiveBytes); err != nil {
		return fmt.Errorf("extract locked asset tar.xz (compressed_bytes=%d elapsed=%s): %w", compressed.bytes, time.Since(started).Round(time.Millisecond), err)
	}
	return nil
}

type countingArchiveReader struct {
	input io.Reader
	bytes int64
}

func (r *countingArchiveReader) Read(p []byte) (int, error) {
	n, err := r.input.Read(p)
	r.bytes += int64(n)
	return n, err
}
