package claudecli

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// maxImageInlineBytes 限制单张内联图片的原始字节数。
// Anthropic Messages API 会拒绝超过约 5MiB 的 base64 图片，超限要在本地 fail-fast。
const maxImageInlineBytes = 5 * 1024 * 1024

// imageMIMEByExt 定义 Claude vision 支持内联编码的图片后缀和 MIME。
var imageMIMEByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// isImageInputType 判断 InputItem.Type 是否表示图片输入。
// 前端会发送 localImage，RPC 可能转为小写；单独的 image 表示远程 URL 形态。
func isImageInputType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "image", "local_image", "localimage":
		return true
	}
	return false
}

// imageBlockFromInput 将 InputItem 转成 Anthropic image content block。
// 非图片或不支持的 URL 返回 nil 交给文本 hint 路径；已识别图片若读取失败或超限则返回错误。
// 剪贴板图片同时带 Path 和 data: URL 时，本地临时文件丢失可退回 data: URL 预览。
func imageBlockFromInput(input dto.InputItem) (map[string]any, error) {
	if !isImageInputType(input.Type) {
		return nil, nil
	}
	path := strings.TrimSpace(input.Path)
	rawURL := strings.TrimSpace(input.URL)
	if path != "" {
		blk, err := imageBlockFromPath(path)
		if err == nil {
			return blk, nil
		}
		// Path read failed (missing tempfile, perms). If the caller also gave
		// us an inline data: URL preview, use that as a fallback before we
		// surface the error.
		if strings.HasPrefix(rawURL, "data:") {
			if fallback, fallbackErr := parseDataURLImage(rawURL); fallbackErr == nil && fallback != nil {
				return fallback, nil
			}
		}
		return nil, err
	}
	if rawURL != "" {
		return imageBlockFromURL(rawURL)
	}
	return nil, nil
}

// imageBlockFromPath 读取本地图片并生成 base64 source block。
// 不支持的扩展名返回 nil，让调用方走文本 hint；读文件和大小超限错误会向上暴露。
func imageBlockFromPath(path string) (map[string]any, error) {
	ext := strings.ToLower(filepath.Ext(path))
	mediaType, ok := imageMIMEByExt[ext]
	if !ok {
		// Unsupported MIME -> let caller fall back to text hint.
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("claudecli: stat image %q: %w", path, err)
	}
	if info.Size() > maxImageInlineBytes {
		return nil, fmt.Errorf("claudecli: image %q is %d bytes, exceeds limit of %d", path, info.Size(), maxImageInlineBytes)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("claudecli: read image %q: %w", path, err)
	}
	return base64ImageBlock(mediaType, bytes), nil
}

// imageBlockFromURL 处理 data: 和 http(s) 两种图片 URL wire 形态。
// 其他 scheme 不报错，调用方会继续保留原始文本提示。
func imageBlockFromURL(rawURL string) (map[string]any, error) {
	if strings.HasPrefix(rawURL, "data:") {
		return parseDataURLImage(rawURL)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return map[string]any{
			"type": "image",
			"source": map[string]any{
				"type": "url",
				"url":  rawURL,
			},
		}, nil
	}
	return nil, nil
}

// parseDataURLImage 将 data:image/<mime>;base64,<data> 解析成 base64 source block。
// 非图片 data URL 返回 nil；base64 损坏或超限会返回错误阻断图片编码。
func parseDataURLImage(rawURL string) (map[string]any, error) {
	mediaType, data, ok := splitImageDataURL(rawURL)
	if !ok {
		return nil, nil
	}
	if err := validateBase64ImagePayload(data); err != nil {
		return nil, err
	}
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       data,
		},
	}, nil
}

// splitImageDataURL 拆分图片 data URL 的 mediaType 和 base64 payload。
// 只有显式带 base64 token 的 image/* URL 才会被接受。
func splitImageDataURL(rawURL string) (mediaType, data string, ok bool) {
	const prefix = "data:"
	if !strings.HasPrefix(rawURL, prefix) {
		return "", "", false
	}
	body := rawURL[len(prefix):]
	comma := strings.Index(body, ",")
	if comma < 0 {
		return "", "", false
	}
	parts := strings.Split(body[:comma], ";")
	mediaType = strings.TrimSpace(parts[0])
	if !strings.HasPrefix(mediaType, "image/") {
		return "", "", false
	}
	if !hasBase64Token(parts[1:]) {
		return "", "", false
	}
	return mediaType, body[comma+1:], true
}

func hasBase64Token(tokens []string) bool {
	for _, p := range tokens {
		if strings.EqualFold(strings.TrimSpace(p), "base64") {
			return true
		}
	}
	return false
}

// validateBase64ImagePayload 校验图片 base64 可解码且未超过内联大小上限。
// 空 payload 和损坏编码都视为真实错误，不能静默降级成空图片。
func validateBase64ImagePayload(data string) error {
	decoded, decErr := base64.StdEncoding.DecodeString(data)
	if decErr != nil {
		return fmt.Errorf("claudecli: data: image base64 decode failed: %w", decErr)
	}
	if len(decoded) == 0 {
		return fmt.Errorf("claudecli: data: image decoded to zero bytes")
	}
	if len(decoded) > maxImageInlineBytes {
		return fmt.Errorf("claudecli: data: image is %d bytes, exceeds limit of %d", len(decoded), maxImageInlineBytes)
	}
	return nil
}

func base64ImageBlock(mediaType string, raw []byte) map[string]any {
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       base64.StdEncoding.EncodeToString(raw),
		},
	}
}
