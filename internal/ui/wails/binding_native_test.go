package wails

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestSaveClipboardImageDecodesBase64Payload(t *testing.T) {
	t.Parallel()

	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 'S', 'u', 'p', 'e', 'r'}
	payload := base64.StdEncoding.EncodeToString(png)

	app := &App{}
	path, err := app.SaveClipboardImage(payload)
	if err != nil {
		t.Fatalf("SaveClipboardImage() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	if path == "" {
		t.Fatal("SaveClipboardImage() path is empty")
	}
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("SaveClipboardImage() path = %q, want *.png", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(png) {
		t.Fatalf("SaveClipboardImage() decoded bytes = %v, want %v", got, png)
	}
}

func TestSaveClipboardImageStripsDataURLPrefix(t *testing.T) {
	t.Parallel()

	raw := []byte("hello-clipboard")
	payload := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)

	app := &App{}
	path, err := app.SaveClipboardImage(payload)
	if err != nil {
		t.Fatalf("SaveClipboardImage() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("SaveClipboardImage() decoded bytes = %q, want %q", got, raw)
	}
}

func TestSaveClipboardImageRejectsEmptyPayload(t *testing.T) {
	t.Parallel()

	app := &App{}
	_, err := app.SaveClipboardImage("   \n\t  ")
	if err == nil {
		t.Fatal("SaveClipboardImage() error = nil, want non-empty payload error")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("SaveClipboardImage() error = %v, want contain 'empty'", err)
	}
}

func TestSaveClipboardImageRejectsInvalidBase64(t *testing.T) {
	t.Parallel()

	app := &App{}
	_, err := app.SaveClipboardImage("!!!not-base64!!!")
	if err == nil {
		t.Fatal("SaveClipboardImage() error = nil, want decode error")
	}
	if !strings.Contains(err.Error(), "decode base64") {
		t.Fatalf("SaveClipboardImage() error = %v, want contain 'decode base64'", err)
	}
}

func TestSaveClipboardImageTolerantToWhitespace(t *testing.T) {
	t.Parallel()

	raw := []byte("wrapped-base64-payload-needs-to-be-long-enough")
	encoded := base64.StdEncoding.EncodeToString(raw)
	// simulate line-wrapped base64 (e.g. MIME/RFC 2045)
	wrapped := encoded[:8] + "\n" + encoded[8:16] + " \t " + encoded[16:]

	app := &App{}
	path, err := app.SaveClipboardImage(wrapped)
	if err != nil {
		t.Fatalf("SaveClipboardImage() error = %v, want nil", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("SaveClipboardImage() decoded bytes = %q, want %q", got, raw)
	}
}
