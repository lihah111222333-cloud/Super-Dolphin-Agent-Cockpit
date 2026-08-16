package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// extractNativeDeb 在进程内解析 DEB ar 和 data.tar.zst，不调用 PATH 中的系统工具。
func extractNativeDeb(archivePath, payloadDir string, maxBytes int64, allowSymlinks bool) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open native DEB artifact: %w", err)
	}
	defer archive.Close()
	header := make([]byte, 8)
	if _, err := io.ReadFull(archive, header); err != nil {
		return fmt.Errorf("read native DEB header: %w", err)
	}
	if string(header) != "!<arch>\n" {
		return errors.New("native DEB artifact has invalid ar signature")
	}
	dataArchive, err := readNativeDebDataArchive(archive, maxBytes)
	if err != nil {
		return err
	}
	decoder, err := zstd.NewReader(bytes.NewReader(dataArchive))
	if err != nil {
		return fmt.Errorf("open native DEB zstd data archive: %w", err)
	}
	defer decoder.Close()
	return extractNativeTarReader(decoder, payloadDir, maxBytes, allowSymlinks)
}

// readNativeDebDataArchive 从 ar envelope 中提取唯一 data.tar.zst 内容。
func readNativeDebDataArchive(archive io.Reader, maxBytes int64) ([]byte, error) {
	var dataArchive []byte
	for {
		name, size, eof, err := readNativeDebEntryHeader(archive, maxBytes)
		if err != nil {
			return nil, err
		}
		if eof {
			break
		}
		content, err := consumeNativeDebEntry(archive, name, size)
		if err != nil {
			return nil, err
		}
		if content != nil {
			dataArchive = content
		}
	}
	if len(dataArchive) == 0 {
		return nil, errors.New("native DEB artifact has no data.tar.zst entry")
	}
	return dataArchive, nil
}

// readNativeDebEntryHeader 解析一个固定宽度 ar header，并报告干净 EOF。
func readNativeDebEntryHeader(archive io.Reader, maxBytes int64) (string, int64, bool, error) {
	header := make([]byte, 60)
	_, err := io.ReadFull(archive, header)
	if errors.Is(err, io.EOF) {
		return "", 0, true, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("read native DEB ar entry header: %w", err)
	}
	if string(header[58:60]) != "`\n" {
		return "", 0, false, errors.New("native DEB artifact has invalid ar entry header")
	}
	name := strings.TrimSuffix(strings.TrimSpace(string(header[:16])), "/")
	sizeText := strings.TrimSpace(string(header[48:58]))
	size, err := strconv.ParseInt(sizeText, 10, 64)
	if err != nil || size < 0 || size > maxBytes {
		return "", 0, false, fmt.Errorf("native DEB ar entry %q has invalid size %q", name, sizeText)
	}
	return name, size, false, nil
}

// consumeNativeDebEntry 读取目标数据项或跳过普通项，并消费 ar 奇数字节填充。
func consumeNativeDebEntry(archive io.Reader, name string, size int64) ([]byte, error) {
	var content []byte
	var err error
	if name == "data.tar.zst" {
		content, err = io.ReadAll(io.LimitReader(archive, size))
		if err == nil && int64(len(content)) != size {
			err = errors.New("native DEB data archive is truncated")
		}
	} else {
		_, err = io.CopyN(io.Discard, archive, size)
	}
	if err != nil {
		return nil, fmt.Errorf("consume native DEB ar entry %q: %w", name, err)
	}
	if size%2 != 0 {
		if _, err := io.CopyN(io.Discard, archive, 1); err != nil {
			return nil, fmt.Errorf("skip native DEB ar padding: %w", err)
		}
	}
	return content, nil
}
