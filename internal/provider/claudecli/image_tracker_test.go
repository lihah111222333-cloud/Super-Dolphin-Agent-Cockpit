package claudecli

import (
	"os"
	"strings"
	"sync"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
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
	var wg sync.WaitGroup
	const writers = 8
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func() {
			defer wg.Done()
			tr.markIfNew([]byte("shared-bytes"))
		}()
	}
	wg.Wait()
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

// writeDistinctTempPNG writes a tiny PNG and appends extra bytes so the SHA-256
// differs from mustWriteTempPNG's output, while keeping the .png extension so
// the upstream MIME detection still recognises it.
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
