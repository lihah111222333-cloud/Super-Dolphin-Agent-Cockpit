package claudecli

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// maxImageInlineBytes caps the raw bytes of a single image we'll inline as a
// base64 source. Anthropic Messages API rejects base64 images above ~5 MiB.
const maxImageInlineBytes = 5 * 1024 * 1024

// imageMIMEByExt is the recognised set of inline image MIME types that
// Anthropic Messages API supports as base64 sources.
var imageMIMEByExt = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// isImageInputType reports whether the InputItem.Type marks the input as an
// image. Frontend currently emits "localImage"; rpc may have lowercased it.
// "image" alone is the remote-URL form.
func isImageInputType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "image", "local_image", "localimage":
		return true
	}
	return false
}

// imageBlockFromInput attempts to construct an Anthropic-style image content
// block from an InputItem. It returns (nil, nil) when the input is not an
// image we know how to encode, in which case the caller should fall back to
// the legacy text-hint pathway. It returns (nil, err) when the input *is*
// an image we tried to encode but failed (read error, oversize), so the
// caller can decide whether to surface the error or degrade.
//
// When the InputItem carries both a local Path and a data: URL preview (the
// frontend sends both for clipboard pastes), a Path read failure falls back
// to the data: URL so a missing temp file does not break the turn.
// imageBlockFromInput 从input处理imageblock。
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

// imageBlockFromPath reads a local image file and emits a base64 source block.
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

// imageBlockFromURL handles the URL form. data: URLs are parsed inline; http(s)
// URLs are passed through as a url-source block (Anthropic supports both).
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

// parseDataURLImage parses a data:image/<mime>;base64,<data> URL into a base64
// source block. Returns (nil, nil) if the URL is not a recognised image data URL.
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

// splitImageDataURL parses `data:image/<type>;base64,<data>` into its mediaType
// and base64 payload. Returns ok=false for any URL that is not a base64-encoded
// image data URL (callers fall back to the legacy text-hint pathway).
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

// validateBase64ImagePayload verifies a base64 image string decodes cleanly and
// stays under the inline size cap. Returns nil when the payload is acceptable.
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
