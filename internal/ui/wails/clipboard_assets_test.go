package wails

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsValidClipboardAssetName(t *testing.T) {
	cases := map[string]bool{
		"clipboard-12345.png":      true,
		"clipboard-abc-123.PNG":    true,
		"codex-clipboard-f05.png":  true,
		"":                         false,
		"../etc/passwd":            false,
		"clipboard-bad/../foo.png": false,
		"foo-clipboard.png":        false, // wrong prefix
		"clipboard-x.jpg":          false, // wrong ext
		"clipboard-":               false, // missing ext
		".":                        false,
		"..":                       false,
	}
	for input, want := range cases {
		if got := isValidClipboardAssetName(input); got != want {
			t.Errorf("isValidClipboardAssetName(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestServeClipboardAssetServesValidPNG(t *testing.T) {
	tempName := mustCreateTempClipboardPNG(t)
	defer os.Remove(filepath.Join(os.TempDir(), tempName))

	mux := http.NewServeMux()
	mux.Handle("/", withClipboardAssets(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// inner: fall-through default for non-clipboard URLs
		http.NotFound(w, r)
	})))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/clipboard/" + tempName)
	if err != nil {
		t.Fatalf("GET clipboard asset: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
}

func TestServeClipboardAssetServesCodexClipboardPNG(t *testing.T) {
	tempName := mustCreateTempClipboardPNGWithPattern(t, "codex-clipboard-*.png")
	defer os.Remove(filepath.Join(os.TempDir(), tempName))

	mux := http.NewServeMux()
	mux.Handle("/", withClipboardAssets(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/clipboard/" + tempName)
	if err != nil {
		t.Fatalf("GET codex clipboard asset: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

func TestServeClipboardAssetRejectsTraversal(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", withClipboardAssets(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	bad := []string{
		"/clipboard/../etc/hosts",
		"/clipboard/clipboard-..%2F..%2Fetc%2Fhosts.png",
		"/clipboard/notclipboard-x.png",
		"/clipboard/clipboard-x.txt",
		"/clipboard/",
	}
	for _, p := range bad {
		res, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("path %s status = %d, want 404", p, res.StatusCode)
		}
	}
}

func TestWithClipboardAssetsFallsThroughForNonClipboardURLs(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})
	srv := httptest.NewServer(withClipboardAssets(inner))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/index.html")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if !called {
		t.Errorf("inner handler not invoked for /index.html")
	}
	if res.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want 418", res.StatusCode)
	}
}

func TestServeLocalImageAssetServesAbsolutePNG(t *testing.T) {
	full := mustCreateLocalPNG(t, t.TempDir())

	srv := httptest.NewServer(withClipboardAssets(http.NotFoundHandler()))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/local-image?path=" + url.QueryEscape(full))
	if err != nil {
		t.Fatalf("GET local image: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
}

func TestServeLocalImageAssetRejectsUnsafePaths(t *testing.T) {
	dir := t.TempDir()
	relativePNG := "screen.png"
	fakePNG := filepath.Join(dir, "fake.png")
	if err := os.WriteFile(fakePNG, []byte("not a png"), 0o600); err != nil {
		t.Fatalf("write fake png: %v", err)
	}
	textFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(textFile, []byte("notes"), 0o600); err != nil {
		t.Fatalf("write text file: %v", err)
	}

	srv := httptest.NewServer(withClipboardAssets(http.NotFoundHandler()))
	defer srv.Close()

	bad := []string{"", relativePNG, fakePNG, textFile, dir}
	for _, p := range bad {
		res, err := http.Get(srv.URL + "/local-image?path=" + url.QueryEscape(p))
		if err != nil {
			t.Fatalf("GET local image %q: %v", p, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("path %q status = %d, want 404", p, res.StatusCode)
		}
	}
}

func mustCreateTempClipboardPNG(t *testing.T) string {
	return mustCreateTempClipboardPNGWithPattern(t, "clipboard-*.png")
}

func mustCreateTempClipboardPNGWithPattern(t *testing.T, pattern string) string {
	t.Helper()
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f.Close()
	full := f.Name()
	name := filepath.Base(full)
	if !strings.HasPrefix(name, strings.TrimSuffix(pattern, "*.png")) {
		t.Fatalf("temp name unexpected: %s", name)
	}
	return name
}

func mustCreateLocalPNG(t *testing.T, dir string) string {
	t.Helper()
	full := filepath.Join(dir, "screen shot.png")
	if err := os.WriteFile(full, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o600); err != nil {
		t.Fatalf("write local png: %v", err)
	}
	return full
}
