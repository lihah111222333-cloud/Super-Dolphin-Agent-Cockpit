package wails

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsValidClipboardAssetName(t *testing.T) {
	cases := map[string]bool{
		"clipboard-12345.png":      true,
		"clipboard-abc-123.PNG":    true,
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

func mustCreateTempClipboardPNG(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "clipboard-*.png")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f.Close()
	full := f.Name()
	name := filepath.Base(full)
	if !strings.HasPrefix(name, "clipboard-") {
		t.Fatalf("temp name unexpected: %s", name)
	}
	return name
}
