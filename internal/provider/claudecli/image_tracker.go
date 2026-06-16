package claudecli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"sync"
)

// imageHashTracker keeps the sha256 of image content blocks already emitted
// in the current claude CLI session so subsequent identical images can be
// replaced with a small text placeholder instead of re-sending the raw bytes.
//
// Lifecycle: one tracker is attached to each *session at construction time
// and lives as long as the session does. After a process restart the tracker
// is empty; the first re-paste of an image already in the resumed history
// will resend the bytes once before subsequent dupes get deduplicated.
type imageHashTracker struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newImageHashTracker() *imageHashTracker {
	return &imageHashTracker{seen: map[string]struct{}{}}
}

// markIfNew hashes raw and either records it (returns hashHex, true) when the
// bytes are new to this session, or returns (hashHex, false) when we have
// already sent them. Empty input returns ("", true) so the caller can keep
// the original block.
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

// imageBlockBytes returns the raw bytes referenced by a base64-source image
// content block, or nil for url-source / non-image blocks. Used by the dedup
// path to compute a stable hash without re-reading the source file.
// imageBlockBytes 处理imageblockbytes。
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
		return []byte{}
	}
	return decoded
}

// dedupedImagePlaceholderBlock builds a tiny text block that stands in for an
// image we have already attached earlier in this session. The hash prefix is
// included so the model can correlate the placeholder with the prior bytes
// if it needs to reason about identity (rare, but harmless when present).
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
