package claudecli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sync"
)

// imageHashTracker 记录当前 Claude CLI 会话已经发送过的图片 sha256。
// 它随 session 创建和释放，内部加锁支持 steer/retry 路径并发触达；进程重启后缓存清空。
type imageHashTracker struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newImageHashTracker() *imageHashTracker {
	return &imageHashTracker{seen: map[string]struct{}{}}
}

// markIfNew 计算图片字节 hash 并记录本会话首次出现的图片。
// 空输入返回新图片语义，让调用方保留原 block；重复图片返回 hash 供占位符引用。
func (t *imageHashTracker) markIfNew(raw []byte) (string, bool) {
	if t == nil || len(raw) == 0 {
		return "", true
	}
	sum := sha256.Sum256(raw)
	full := hex.EncodeToString(sum[:])
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.seen[full]; ok {
		return full, false
	}
	t.seen[full] = struct{}{}
	return full, true
}

// imageBlockBytes 提取 base64 image content block 中的原始字节。
// URL 图片和非图片 block 返回 nil，去重路径不会重新读取外部资源。
func imageBlockBytes(block map[string]any) []byte {
	if block == nil {
		return nil
	}
	if t, _ := block["type"].(string); t != "image" {
		return nil
	}
	source, _ := block["source"].(map[string]any)
	if source == nil {
		return nil
	}
	if st, _ := source["type"].(string); st != "base64" {
		return nil
	}
	data, _ := source["data"].(string)
	if data == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil
	}
	return decoded
}

// dedupedImagePlaceholderBlock 构造重复图片的文本占位 block。
// 文本里带 hash 前缀，模型需要判断图片身份时仍可与前文图片关联。
func dedupedImagePlaceholderBlock(hashHex string) map[string]any {
	short := hashHex
	if len(short) > 12 {
		short = short[:12]
	}
	return map[string]any{
		"type": "text",
		"text": "[image previously attached in this session, sha256:" + short + "…]",
	}
}
