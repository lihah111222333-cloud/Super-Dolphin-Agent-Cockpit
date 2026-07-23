// Package datasource 提供本地文件上传、列举和删除能力，并把文件正文入库供 prompt 动态段消费。
package datasource

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// extractDatasourceText 根据上传文件类型抽取可入库的正文。
// 文本文件只接受可解码内容；无法识别的编码直接报错，避免把乱码写进 datasource_documents。
func extractDatasourceText(ctx context.Context, sourcePath, ext string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var (
		text string
		err  error
	)
	ext = strings.ToLower(strings.TrimSpace(ext))
	switch {
	case ext == ".pdf":
		text, err = extractPDFText(ctx, sourcePath)
	case isTextUploadExtension(ext):
		text, err = extractTextFile(sourcePath)
	default:
		return "", fmt.Errorf("%w: %s", errUnsupportedFileExtension, ext)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", errDatasourceContentEmpty
	}
	return text, nil
}

// extractTextFile 读取普通文本文件并解码成 UTF-8 字符串。
// 支持 UTF-8、UTF-8 BOM、UTF-16LE BOM 和 UTF-16BE BOM；其他编码 fail-fast。
func extractTextFile(sourcePath string) (string, error) {
	content, err := readLimitedTextDatasource(sourcePath)
	if err != nil {
		return "", fmt.Errorf("read text datasource: %w", err)
	}
	text, err := decodeTextDatasourceBytes(content)
	if err != nil {
		return "", err
	}
	return text, nil
}

// readLimitedTextDatasource 读取文本 datasource，并在超过导入上限时返回错误，避免整文件读入失控。
func readLimitedTextDatasource(sourcePath string) (content []byte, err error) {
	file, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	content, err = io.ReadAll(io.LimitReader(file, datasourceMaxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(content) > datasourceMaxImportBytes {
		return nil, errDatasourceTextTooLarge
	}
	return content, nil
}

// isTextUploadExtension 定义 datasource 上传接口可解析为纯文本的后缀集合。
// 这里保持白名单，避免把二进制格式误当正文入库。
func isTextUploadExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".txt",
		".md",
		".markdown",
		".csv",
		".tsv",
		".json",
		".jsonl",
		".yaml",
		".yml",
		".xml",
		".html",
		".htm",
		".css",
		".js",
		".jsx",
		".ts",
		".tsx",
		".go",
		".py",
		".java",
		".c",
		".h",
		".cpp",
		".hpp",
		".cs",
		".rs",
		".rb",
		".php",
		".sh",
		".bash",
		".zsh",
		".ps1",
		".bat",
		".cmd",
		".sql",
		".toml",
		".ini",
		".env",
		".log":
		return true
	default:
		return false
	}
}

// decodeTextDatasourceBytes 把常见文本编码规整为 Go 字符串。
// 无 BOM 时只接受合法 UTF-8，防止二进制文件伪装成文本后进入数据库。
func decodeTextDatasourceBytes(content []byte) (string, error) {
	switch {
	case hasPrefix(content, 0xEF, 0xBB, 0xBF):
		return decodeUTF8Text(content[3:])
	case hasPrefix(content, 0xFF, 0xFE):
		return decodeUTF16Text(content[2:], true)
	case hasPrefix(content, 0xFE, 0xFF):
		return decodeUTF16Text(content[2:], false)
	default:
		return decodeUTF8Text(content)
	}
}

// decodeUTF8Text 验证并返回 UTF-8 文本，无效时 fail-fast。
func decodeUTF8Text(content []byte) (string, error) {
	if !utf8.Valid(content) {
		return "", errUnsupportedTextEncoding
	}
	return string(content), nil
}

// decodeUTF16Text 将 UTF-16LE 或 UTF-16BE 字节解码为 UTF-8 字符串。
func decodeUTF16Text(content []byte, littleEndian bool) (string, error) {
	if len(content)%2 != 0 {
		return "", errUnsupportedTextEncoding
	}
	codeUnits := make([]uint16, 0, len(content)/2)
	for index := 0; index+1 < len(content); index += 2 {
		if littleEndian {
			codeUnits = append(codeUnits, uint16(content[index])|uint16(content[index+1])<<8)
			continue
		}
		codeUnits = append(codeUnits, uint16(content[index])<<8|uint16(content[index+1]))
	}
	return string(utf16.Decode(codeUnits)), nil
}

// hasPrefix 做 BOM 前缀匹配。
// 这里不用 bytes.HasPrefix 的唯一原因是调用点以可变参数写十六进制字节更直观。
func hasPrefix(content []byte, prefix ...byte) bool {
	if len(content) < len(prefix) {
		return false
	}
	for index, want := range prefix {
		if content[index] != want {
			return false
		}
	}
	return true
}
