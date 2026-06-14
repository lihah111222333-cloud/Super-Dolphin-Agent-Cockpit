package wails

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// clipboardPathPrefix is the URL prefix the webview uses to load files
// previously saved by SaveClipboardImage. The basename of the URL must match
// the pattern produced by os.CreateTemp("", "clipboard-*.png").
const clipboardPathPrefix = "/clipboard/"

// withClipboardAssets wraps inner with a route that serves the temporary
// clipboard PNGs written by SaveClipboardImage. Wails webviews block file://
// loads, so the frontend cannot use the raw temp path as an <img src>.
func withClipboardAssets(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, clipboardPathPrefix) {
			serveClipboardAsset(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})
}

// serveClipboardAsset resolves and serves one clipboard temp file. It rejects
// anything that is not a single basename matching `clipboard-*.png`, and
// double-checks the resolved file is still inside os.TempDir() (defending
// against symlink escapes).
func serveClipboardAsset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, clipboardPathPrefix)
	if !isValidClipboardAssetName(name) {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(os.TempDir(), name)
	if !isUnderTempDir(full) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, full)
}

// isValidClipboardAssetName 判断validclipboardasset名称是否可用。
func isValidClipboardAssetName(name string) bool {
	if name == "" {
		return false
	}
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	if name == "." || name == ".." {
		return false
	}
	if !strings.HasPrefix(name, "clipboard-") {
		return false
	}
	if !strings.EqualFold(filepath.Ext(name), ".png") {
		return false
	}
	return true
}

// isUnderTempDir resolves both the candidate file and os.TempDir() to their
// real paths and confirms the candidate is still nested inside the temp dir.
// Falls back to a lexical containment check when the file does not yet exist
// (so unit tests can stage missing files without false positives).
// isUnderTempDir 判断undertemp目录是否可用。
func isUnderTempDir(full string) bool {
	cleanFull := filepath.Clean(full)
	tempDir := filepath.Clean(os.TempDir())

	resolvedFull, err := filepath.EvalSymlinks(cleanFull)
	if err == nil {
		cleanFull = resolvedFull
	}
	resolvedTemp, err := filepath.EvalSymlinks(tempDir)
	if err == nil {
		tempDir = resolvedTemp
	}

	rel, err := filepath.Rel(tempDir, cleanFull)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	if strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}
