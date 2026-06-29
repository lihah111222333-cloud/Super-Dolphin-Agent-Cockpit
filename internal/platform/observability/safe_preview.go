package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	// ErrorPreviewField 是所有 trace/log 预览错误文本使用的统一字段名。
	ErrorPreviewField = "error_preview"
	// ErrorCodeField 是错误分类使用的统一字段名。
	ErrorCodeField = "error_code"
	// ProviderExitCodeField 是 provider 进程退出码使用的统一字段名。
	ProviderExitCodeField = "provider_exit_code"
	// PeerIDField 是 MCP peer 标识使用的统一字段名。
	PeerIDField = "peer_id"
)

const defaultErrorPreviewBytes = 512

// SafePreviewResult 描述可写入日志或 trace metadata 的安全预览。
// 超过上限的内容不返回局部正文，只保留原始字节数和哈希，避免长 payload 泄密。
type SafePreviewResult struct {
	Preview   string `json:"preview,omitempty"`
	Truncated bool   `json:"truncated"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256,omitempty"`
}

// SafePreview 将任意值转换为单行脱敏预览；超过 maxBytes 时只返回大小和哈希。
func SafePreview(value any, maxBytes int) SafePreviewResult {
	raw := safePreviewBytes(value)
	result := SafePreviewResult{Bytes: int64(len(raw))}
	if maxBytes <= 0 || len(raw) > maxBytes {
		result.Truncated = true
		result.SHA256 = safePreviewHash(raw)
		return result
	}
	result.Preview = safePreviewSanitizer(maxBytes).String(string(raw))
	return result
}

// SafeErrorPreview 返回适合 error_preview 字段的短错误摘要。
func SafeErrorPreview(err error) string {
	if err == nil {
		return ""
	}
	return SafePreview(err.Error(), defaultErrorPreviewBytes).Preview
}

// SafeErrorMetadata 返回统一错误字段；没有错误时返回 nil，便于调用方按需合入 metadata。
func SafeErrorMetadata(err error, code string) map[string]any {
	if err == nil {
		return nil
	}
	out := map[string]any{
		ErrorPreviewField: SafeErrorPreview(err),
	}
	if code != "" {
		out[ErrorCodeField] = code
	}
	return out
}

// SafeMetadataValue 按 metadata 键名和值类型返回可安全写入日志或 trace 的字段值。
func SafeMetadataValue(key string, value any, maxBytes int) (any, bool) {
	return safePreviewSanitizer(maxBytes).metadataValueForKey(key, value)
}

func safePreviewBytes(value any) []byte {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		return append([]byte(nil), typed...)
	case string:
		return []byte(typed)
	case json.RawMessage:
		return append([]byte(nil), typed...)
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return []byte(fmt.Sprint(typed))
		}
		return raw
	}
}

func safePreviewSanitizer(maxBytes int) Sanitizer {
	return Sanitizer{stringMaxBytes: maxBytes, metadataMaxBytes: maxBytes}
}

func safePreviewHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
