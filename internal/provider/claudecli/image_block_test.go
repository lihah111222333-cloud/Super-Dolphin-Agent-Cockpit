package claudecli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// 1x1 transparent PNG (68 bytes).
const tinyPNGBase64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkAAIAAAoAAv/lxKUAAAAASUVORK5CYII="

func mustWriteTempPNG(t *testing.T) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(tinyPNGBase64)
	if err != nil {
		t.Fatalf("decode tiny png: %v", err)
	}
	path := filepath.Join(t.TempDir(), "tiny.png")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write tiny png: %v", err)
	}
	return path
}

func TestIsImageInputType(t *testing.T) {
	yes := []string{"image", "Image", "local_image", "localImage", "LOCALIMAGE", " image "}
	for _, in := range yes {
		if !isImageInputType(in) {
			t.Errorf("isImageInputType(%q) = false, want true", in)
		}
	}
	no := []string{"", "text", "mention", "file", "filecontent"}
	for _, in := range no {
		if isImageInputType(in) {
			t.Errorf("isImageInputType(%q) = true, want false", in)
		}
	}
}

func TestImageBlockFromPathSuccess(t *testing.T) {
	path := mustWriteTempPNG(t)
	blk, err := imageBlockFromInput(dto.InputItem{Type: "localImage", Path: path})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if blk == nil {
		t.Fatalf("expected image block, got nil")
	}
	if got := blk["type"]; got != "image" {
		t.Errorf("block type = %v, want image", got)
	}
	source, _ := blk["source"].(map[string]any)
	if source["type"] != "base64" {
		t.Errorf("source type = %v, want base64", source["type"])
	}
	if source["media_type"] != "image/png" {
		t.Errorf("media_type = %v, want image/png", source["media_type"])
	}
	if data, _ := source["data"].(string); data == "" || strings.ContainsAny(data, " \n") {
		t.Errorf("data field empty or whitespace: %q", data)
	}
}

func TestImageBlockFromPathUnsupportedExt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weird.heic")
	if err := os.WriteFile(path, []byte("not a real heic"), 0o644); err != nil {
		t.Fatal(err)
	}
	blk, err := imageBlockFromInput(dto.InputItem{Type: "image", Path: path})
	if err != nil {
		t.Fatalf("unsupported ext should not error, got %v", err)
	}
	if blk != nil {
		t.Errorf("unsupported ext should fall back (nil block), got %v", blk)
	}
}

func TestImageBlockFromPathMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.png")
	_, err := imageBlockFromInput(dto.InputItem{Type: "localImage", Path: missing})
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestImageBlockFromPathOversize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.png")
	huge := make([]byte, maxImageInlineBytes+1)
	if err := os.WriteFile(path, huge, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := imageBlockFromInput(dto.InputItem{Type: "localImage", Path: path})
	if err == nil {
		t.Fatalf("expected oversize error")
	}
	if !strings.Contains(err.Error(), "exceeds limit") {
		t.Errorf("error should mention limit, got %q", err.Error())
	}
}

func TestImageBlockFromDataURL(t *testing.T) {
	dataURL := "data:image/png;base64," + tinyPNGBase64
	blk, err := imageBlockFromInput(dto.InputItem{Type: "image", URL: dataURL})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if blk == nil {
		t.Fatalf("expected block from data URL")
	}
	source, _ := blk["source"].(map[string]any)
	if source["type"] != "base64" {
		t.Errorf("source type = %v, want base64", source["type"])
	}
	if source["data"] != tinyPNGBase64 {
		t.Errorf("data not preserved: got %v", source["data"])
	}
}

func TestImageBlockFromHTTPURL(t *testing.T) {
	blk, err := imageBlockFromInput(dto.InputItem{Type: "image", URL: "https://example.com/x.png"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if blk == nil {
		t.Fatalf("expected block from http url")
	}
	source, _ := blk["source"].(map[string]any)
	if source["type"] != "url" {
		t.Errorf("source type = %v, want url", source["type"])
	}
	if source["url"] != "https://example.com/x.png" {
		t.Errorf("url not preserved: %v", source["url"])
	}
}

func TestImageBlockNonImageInputReturnsNil(t *testing.T) {
	blk, err := imageBlockFromInput(dto.InputItem{Type: "text", Content: "hello"})
	if err != nil || blk != nil {
		t.Errorf("non-image input should return (nil, nil), got (%v, %v)", blk, err)
	}
}

func TestComposeTurnContentTextOnly(t *testing.T) {
	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "text", Content: "hello"}},
	}, nil)
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0]["type"] != "text" {
		t.Errorf("block[0] type = %v, want text", blocks[0]["type"])
	}
	if blocks[0]["text"] != "hello" {
		t.Errorf("block[0] text = %v, want hello", blocks[0]["text"])
	}
}

func TestComposeTurnContentImageOnly(t *testing.T) {
	path := mustWriteTempPNG(t)
	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "localImage", Path: path}},
	}, nil)
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0]["type"] != "image" {
		t.Errorf("block[0] type = %v, want image", blocks[0]["type"])
	}
}

func TestComposeTurnContentImageAndText(t *testing.T) {
	path := mustWriteTempPNG(t)
	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "localImage", Path: path},
			{Type: "text", Content: "describe this"},
		},
	}, nil)
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	// Image block should come before text block.
	if blocks[0]["type"] != "image" {
		t.Errorf("block[0] type = %v, want image (images go first)", blocks[0]["type"])
	}
	if blocks[1]["type"] != "text" {
		t.Errorf("block[1] type = %v, want text", blocks[1]["type"])
	}
	if !strings.Contains(blocks[1]["text"].(string), "describe this") {
		t.Errorf("text block missing user content: %v", blocks[1]["text"])
	}
}

func TestComposeTurnContentUnsupportedMIMEFallsBackToTextHint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weird.heic")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "image", Path: path}},
	}, nil)
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1 (text hint fallback)", len(blocks))
	}
	if blocks[0]["type"] != "text" {
		t.Errorf("block[0] type = %v, want text (fallback)", blocks[0]["type"])
	}
	if !strings.Contains(blocks[0]["text"].(string), "Use the Read tool") {
		t.Errorf("expected fallback hint text, got %q", blocks[0]["text"])
	}
}

func TestComposeTurnContentMissingFileFallsBackToTextHint(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ghost.png")
	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{{Type: "localImage", Path: missing}},
	}, nil)
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1 (degrade to text)", len(blocks))
	}
	if blocks[0]["type"] != "text" {
		t.Errorf("block[0] type = %v, want text", blocks[0]["type"])
	}
}

func TestMarshalTurnContentPayloadEnvelope(t *testing.T) {
	path := mustWriteTempPNG(t)
	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "localImage", Path: path},
			{Type: "text", Content: "hi"},
		},
	}, nil)
	raw, err := marshalTurnContentPayload(blocks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var env struct {
		Type    string `json:"type"`
		Message struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Type != "user" || env.Message.Role != "user" {
		t.Errorf("envelope type/role unexpected: %+v", env)
	}
	if len(env.Message.Content) != 2 {
		t.Fatalf("content len = %d, want 2", len(env.Message.Content))
	}
	if env.Message.Content[0]["type"] != "image" {
		t.Errorf("first block not image: %v", env.Message.Content[0])
	}
}

func TestMarshalTurnPayloadStillWorks(t *testing.T) {
	// Backwards-compat: silent turn keepalive uses marshalTurnPayload(string).
	raw, err := marshalTurnPayload("PING")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"text":"PING"`) {
		t.Errorf("expected text payload, got %s", raw)
	}
}

func TestImageBlockFromInputFallsBackToDataURLWhenPathMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ghost.png")
	dataURL := "data:image/png;base64," + tinyPNGBase64
	blk, err := imageBlockFromInput(dto.InputItem{
		Type: "localImage",
		Path: missing,
		URL:  dataURL,
	})
	if err != nil {
		t.Fatalf("expected fallback to data URL, got err: %v", err)
	}
	if blk == nil {
		t.Fatalf("expected block from data URL fallback, got nil")
	}
	source, _ := blk["source"].(map[string]any)
	if source["type"] != "base64" || source["media_type"] != "image/png" {
		t.Errorf("data URL fallback malformed: %+v", source)
	}
}

func TestImageBlockFromInputPathFailsAndNoFallback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "ghost.png")
	// URL is non-data (http) — should NOT be used as fallback for a path failure.
	_, err := imageBlockFromInput(dto.InputItem{
		Type: "localImage",
		Path: missing,
		URL:  "https://example.com/x.png",
	})
	if err == nil {
		t.Fatalf("expected error when path fails and URL is not data:")
	}
}

func TestParseDataURLImageRejectsBrokenBase64(t *testing.T) {
	bad := "data:image/png;base64,!!!not-base64!!!"
	_, err := imageBlockFromInput(dto.InputItem{Type: "image", URL: bad})
	if err == nil {
		t.Fatalf("expected base64 decode error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("expected decode error, got %q", err.Error())
	}
}

func TestParseDataURLImageRejectsZeroBytes(t *testing.T) {
	// data URL with empty base64 payload.
	emptyDataURL := "data:image/png;base64,"
	_, err := imageBlockFromInput(dto.InputItem{Type: "image", URL: emptyDataURL})
	if err == nil {
		t.Fatalf("expected zero-bytes error for empty data URL")
	}
}

func TestComposeTurnContentPreservesImageInputCaption(t *testing.T) {
	// When an image input also carries a Content caption, the caption text
	// must survive into the trailing text block — it must NOT be silently
	// dropped just because the image bytes were extracted into their own
	// content block.
	path := mustWriteTempPNG(t)
	blocks := composeTurnContent(dto.TurnRequest{
		Inputs: []dto.InputItem{
			{Type: "localImage", Path: path, Content: "caption attached to image"},
		},
	}, nil)
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2 (image + caption text)", len(blocks))
	}
	if blocks[0]["type"] != "image" {
		t.Errorf("block[0] = %v, want image", blocks[0]["type"])
	}
	if blocks[1]["type"] != "text" {
		t.Errorf("block[1] = %v, want text", blocks[1]["type"])
	}
	if got := blocks[1]["text"].(string); !strings.Contains(got, "caption attached to image") {
		t.Errorf("caption lost: block[1].text = %q", got)
	}
}
