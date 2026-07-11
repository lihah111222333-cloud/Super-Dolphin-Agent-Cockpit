package claudecli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

func TestImageHashTrackerMarkIfNew(t *testing.T) {
	tr := newImageHashTracker()

	hash, isNew := tr.markIfNew([]byte("hello"))
	if !isNew {
		t.Errorf("first markIfNew should report new")
	}
	if len(hash) != 64 { // sha256 hex = 64 chars
		t.Errorf("expected 64-char hex hash, got %q", hash)
	}

	hash2, isNew := tr.markIfNew([]byte("hello"))
	if isNew {
		t.Errorf("second markIfNew of same bytes should report duplicate")
	}
	if hash2 != hash {
		t.Errorf("hash mismatch on dupe: %q vs %q", hash2, hash)
	}

	_, isNew = tr.markIfNew([]byte("world"))
	if !isNew {
		t.Errorf("different bytes should report new")
	}
}

func TestImageHashTrackerNilSafe(t *testing.T) {
	var tr *imageHashTracker
	hash, isNew := tr.markIfNew([]byte("x"))
	if !isNew || hash != "" {
		t.Errorf("nil tracker should return (\"\", true), got (%q, %v)", hash, isNew)
	}
}

func TestImageHashTrackerEmptyBytes(t *testing.T) {
	tr := newImageHashTracker()
	hash, isNew := tr.markIfNew(nil)
	if !isNew || hash != "" {
		t.Errorf("empty bytes should return (\"\", true), got (%q, %v)", hash, isNew)
	}
}

func TestImageHashTrackerConcurrent(t *testing.T) {
	tr := newImageHashTracker()
	goroutines := newTestGoroutineGroup(t)
	const writers = 8
	for range writers {
		goroutines.Go(func() {
			tr.markIfNew([]byte("shared-bytes"))
		})
	}
	goroutines.Wait()
	_, isNew := tr.markIfNew([]byte("shared-bytes"))
	if isNew {
		t.Errorf("concurrent tracker did not record bytes; expected duplicate after Wait")
	}
}

func TestImageBlockBytesExtractsBase64(t *testing.T) {
	block := base64ImageBlock("image/png", []byte("rawbytes"))
	got := imageBlockBytes(block)
	if string(got) != "rawbytes" {
		t.Errorf("expected rawbytes, got %q", got)
	}
}

func TestImageBlockBytesIgnoresURLSource(t *testing.T) {
	block := map[string]any{
		"type":   "image",
		"source": map[string]any{"type": "url", "url": "https://example.com/x.png"},
	}
	if got := imageBlockBytes(block); got != nil {
		t.Errorf("url-source block should not yield bytes, got %v", got)
	}
}

func TestImageBlockBytesIgnoresNonImage(t *testing.T) {
	block := map[string]any{"type": "text", "text": "hi"}
	if got := imageBlockBytes(block); got != nil {
		t.Errorf("text block should not yield bytes, got %v", got)
	}
}

func TestComposeTurnContentDedupesRepeatedImages(t *testing.T) {
	tracker := newImageHashTracker()
	path := mustWriteTempPNG(t)

	first := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "localImage", Path: path},
			{Type: "text", Content: "describe"},
		},
	}, tracker)
	if len(first) != 2 || first[0]["type"] != "image" {
		t.Fatalf("first turn: expected image+text blocks, got %+v", first)
	}

	second := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "localImage", Path: path},
			{Type: "text", Content: "what changed"},
		},
	}, tracker)
	if len(second) != 2 {
		t.Fatalf("second turn: expected 2 blocks (placeholder + user text), got %+v", second)
	}
	if second[0]["type"] != "text" {
		t.Errorf("second turn block[0] should be deduped placeholder, got %v", second[0]["type"])
	}
	placeholder := second[0]["text"].(string)
	if !strings.Contains(placeholder, "previously attached") {
		t.Errorf("placeholder text missing dedup hint: %q", placeholder)
	}
	if !strings.Contains(placeholder, "sha256:") {
		t.Errorf("placeholder text missing sha256: %q", placeholder)
	}
}

func TestComposeTurnContentDoesNotDedupeDistinctImages(t *testing.T) {
	tracker := newImageHashTracker()
	pathA := mustWriteTempPNG(t)
	pathB := writeDistinctTempPNG(t, "AB")

	first := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "localImage", Path: pathA}},
	}, tracker)
	if first[0]["type"] != "image" {
		t.Errorf("first turn should send image block, got %v", first[0]["type"])
	}

	second := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "localImage", Path: pathB}},
	}, tracker)
	if second[0]["type"] != "image" {
		t.Errorf("second turn with DIFFERENT image should still send full block, got %v", second[0]["type"])
	}
}

func TestComposeTurnContentNilTrackerDisablesDedup(t *testing.T) {
	path := mustWriteTempPNG(t)
	first := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "localImage", Path: path}},
	}, nil)
	second := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "localImage", Path: path}},
	}, nil)
	if first[0]["type"] != "image" || second[0]["type"] != "image" {
		t.Errorf("nil tracker should preserve image blocks across turns; got %v then %v",
			first[0]["type"], second[0]["type"])
	}
}

// writeDistinctTempPNG 复用最小 PNG fixture 并追加字节，让哈希不同但扩展名仍走图片 MIME 路径。
func writeDistinctTempPNG(t *testing.T, suffix string) string {
	t.Helper()
	path := mustWriteTempPNG(t)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	defer f.Close()
	if _, err := f.Write([]byte(suffix)); err != nil {
		t.Fatalf("append bytes: %v", err)
	}
	return path
}

// TestPrepareTurnLockedDedupesImageAcrossTurns 验证同一 session 内重复图片会从完整 image block 降为占位文本。
// 这里检查的是发送给 provider 的传输载荷，而不只检查图片去重状态。
func TestPrepareTurnLockedDedupesImageAcrossTurns(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	pngPath := mustWriteTempPNG(t)
	s := &session{
		threadID:        "thread-dedup",
		sessionID:       "thread-dedup",
		threadReady:     ready,
		transport:       &transport{stdin: &recordingWriteCloser{}, done: make(chan struct{})},
		suppressedTurns: map[string]struct{}{},
		imageTracker:    newImageHashTracker(),
	}
	req := dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "localImage", Path: pngPath},
			{Type: "text", Content: "describe"},
		},
	}

	payload1 := prepareImageTrackerTurnPayload(t, s, req, "turn1")
	payload2 := prepareImageTrackerTurnPayload(t, s, req, "turn2")
	env1 := decodeImagePayloadEnvelope(t, payload1, "turn1")
	env2 := decodeImagePayloadEnvelope(t, payload2, "turn2")

	assertImagePayloadsDeduped(t, payload1, payload2, env1.Message.Content, env2.Message.Content)
}

type imagePayloadContent struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Source any    `json:"source"`
}

type imagePayloadEnvelope struct {
	Message struct {
		Content []imagePayloadContent `json:"content"`
	} `json:"message"`
}

func prepareImageTrackerTurnPayload(t *testing.T, s *session, req dto.TurnRequest, label string) []byte {
	t.Helper()

	s.mu.Lock()
	payload, _, _, err := s.prepareTurnLocked(context.Background(), req)
	s.activeTurn = nil
	s.mu.Unlock()
	if err != nil {
		t.Fatalf("%s prepareTurnLocked error: %v", label, err)
	}
	return payload
}

func decodeImagePayloadEnvelope(t *testing.T, payload []byte, label string) imagePayloadEnvelope {
	t.Helper()

	var env imagePayloadEnvelope
	if err := json.Unmarshal(payload, &env); err != nil {
		t.Fatalf("unmarshal %s: %v", label, err)
	}
	return env
}

func assertImagePayloadsDeduped(t *testing.T, payload1, payload2 []byte, content1, content2 []imagePayloadContent) {
	t.Helper()

	if !hasImagePayloadContent(content1) {
		t.Errorf("turn1 payload missing image block: %+v", content1)
	}
	assertNoImagePayloadContent(t, content2)
	if !hasDedupPlaceholder(content2) {
		t.Errorf("turn2 missing dedup placeholder text: %+v", content2)
	}
	if len(payload2) >= len(payload1) {
		t.Errorf("turn2 payload should be smaller than turn1; got %d vs %d", len(payload2), len(payload1))
	}
}

func hasImagePayloadContent(content []imagePayloadContent) bool {
	for _, c := range content {
		if c.Type == "image" && c.Source != nil {
			return true
		}
	}
	return false
}

func assertNoImagePayloadContent(t *testing.T, content []imagePayloadContent) {
	t.Helper()

	for _, c := range content {
		if c.Type == "image" {
			t.Errorf("turn2 payload still has image block (dedup failed): %+v", c)
		}
	}
}

func hasDedupPlaceholder(content []imagePayloadContent) bool {
	for _, c := range content {
		if c.Type == "text" && strings.Contains(c.Text, "previously attached") {
			return true
		}
	}
	return false
}
