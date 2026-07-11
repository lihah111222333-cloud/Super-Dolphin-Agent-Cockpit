package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/appupdate"
)

func TestRunWritesSignedLatestManifest(t *testing.T) {
	publicKey, privateKey := testKeypair(t)
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "Super Dolphin.dmg")
	artifactBytes := []byte("fake dmg bytes")
	writeFile(t, artifactPath, artifactBytes, 0o644)
	signingKeyPath := filepath.Join(dir, "ed25519.key")
	writeFile(t, signingKeyPath, privateKey, 0o600)
	outPath := filepath.Join(dir, "latest.json")

	err := run([]string{
		"-artifact", artifactPath,
		"-artifact-url", "https://updates.example.com/Super%20Dolphin.dmg",
		"-app-id", "com.superdolphin.app",
		"-channel", "gray-macos",
		"-version", "1.2.3",
		"-minimum-version", "1.0.0",
		"-platform", "darwin-arm64",
		"-signing-key", signingKeyPath,
		"-out", outPath,
		"-notes", "gray rollout",
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}

	raw := readFile(t, outPath)
	payload, artifact, err := appupdate.VerifySignedManifest(raw, appupdate.VerifyOptions{
		PublicKey:      publicKey,
		AppID:          "com.superdolphin.app",
		Channel:        "gray-macos",
		Platform:       "darwin-arm64",
		CurrentVersion: "1.2.2",
	})
	if err != nil {
		t.Fatalf("VerifySignedManifest() error = %v", err)
	}
	if payload.Version != "1.2.3" {
		t.Fatalf("payload version = %q, want 1.2.3", payload.Version)
	}
	if payload.MinimumVersion != "1.0.0" {
		t.Fatalf("payload minimum_version = %q, want 1.0.0", payload.MinimumVersion)
	}
	if artifact.URL != "https://updates.example.com/Super%20Dolphin.dmg" {
		t.Fatalf("artifact URL = %q, want artifact URL flag", artifact.URL)
	}
	wantSHA := sha256.Sum256(artifactBytes)
	if artifact.SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("artifact sha256 = %q, want %q", artifact.SHA256, hex.EncodeToString(wantSHA[:]))
	}
	if artifact.Size != int64(len(artifactBytes)) {
		t.Fatalf("artifact size = %d, want %d", artifact.Size, len(artifactBytes))
	}

	assertNotesExcludedFromPayload(t, raw)
}

func TestRunCheckKeyAcceptsMatchingPublicKey(t *testing.T) {
	publicKey, privateKey := testKeypair(t)
	dir := t.TempDir()
	signingKeyPath := filepath.Join(dir, "ed25519.key")
	writeFile(t, signingKeyPath, privateKey, 0o600)

	err := run([]string{
		"-check-key",
		"-signing-key", signingKeyPath,
		"-public-key", base64PublicKey(publicKey),
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
}

func TestRunRejectsMismatchedPublicKey(t *testing.T) {
	_, privateKey := testKeypair(t)
	otherPublicKey, _ := testKeypair(t)
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "Super Dolphin.dmg")
	writeFile(t, artifactPath, []byte("fake dmg bytes"), 0o644)
	signingKeyPath := filepath.Join(dir, "ed25519.key")
	writeFile(t, signingKeyPath, privateKey, 0o600)

	err := run([]string{
		"-artifact", artifactPath,
		"-artifact-url", "https://updates.example.com/Super%20Dolphin.dmg",
		"-app-id", "com.superdolphin.app",
		"-channel", "gray-macos",
		"-version", "1.2.3",
		"-minimum-version", "1.0.0",
		"-platform", "darwin-arm64",
		"-signing-key", signingKeyPath,
		"-public-key", base64PublicKey(otherPublicKey),
		"-out", filepath.Join(dir, "latest.json"),
	})
	if err == nil {
		t.Fatal("run() error = nil, want public key mismatch failure")
	}
	if !strings.Contains(err.Error(), "public key does not match") {
		t.Fatalf("run() error = %v, want public key mismatch failure", err)
	}
}

func TestRunVerifiesExistingManifestAgainstArtifact(t *testing.T) {
	publicKey, privateKey := testKeypair(t)
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "Super Dolphin.dmg")
	writeFile(t, artifactPath, []byte("fake dmg bytes"), 0o644)
	signingKeyPath := filepath.Join(dir, "ed25519.key")
	writeFile(t, signingKeyPath, privateKey, 0o600)
	outPath := filepath.Join(dir, "latest.json")

	if err := run([]string{
		"-artifact", artifactPath,
		"-artifact-url", "https://updates.example.com/Super%20Dolphin.dmg",
		"-app-id", "super-dolphin",
		"-channel", "gray",
		"-version", "1.2.3",
		"-minimum-version", "1.0.0",
		"-platform", "darwin-arm64",
		"-signing-key", signingKeyPath,
		"-public-key", base64PublicKey(publicKey),
		"-out", outPath,
	}); err != nil {
		t.Fatalf("generate manifest error = %v", err)
	}

	err := run([]string{
		"-verify-manifest", outPath,
		"-artifact", artifactPath,
		"-artifact-url", "https://updates.example.com/Super%20Dolphin.dmg",
		"-app-id", "super-dolphin",
		"-channel", "gray",
		"-version", "1.2.3",
		"-minimum-version", "1.0.0",
		"-platform", "darwin-arm64",
		"-public-key", base64PublicKey(publicKey),
	})
	if err != nil {
		t.Fatalf("verify manifest error = %v", err)
	}
}

func TestRunRejectsInvalidSigningKeyLength(t *testing.T) {
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "Super Dolphin.dmg")
	writeFile(t, artifactPath, []byte("fake dmg bytes"), 0o644)
	signingKeyPath := filepath.Join(dir, "ed25519.key")
	writeFile(t, signingKeyPath, []byte("short"), 0o600)

	err := run([]string{
		"-artifact", artifactPath,
		"-artifact-url", "https://updates.example.com/Super%20Dolphin.dmg",
		"-app-id", "com.superdolphin.app",
		"-channel", "gray-macos",
		"-version", "1.2.3",
		"-minimum-version", "1.0.0",
		"-platform", "darwin-arm64",
		"-signing-key", signingKeyPath,
		"-out", filepath.Join(dir, "latest.json"),
		"-notes", "gray rollout",
	})
	if err == nil {
		t.Fatal("run() error = nil, want signing key length failure")
	}
	if !strings.Contains(err.Error(), "signing key length") {
		t.Fatalf("run() error = %v, want signing key length failure", err)
	}
}

func testKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return publicKey, privateKey
}

func base64PublicKey(publicKey ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(publicKey)
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return data
}

func assertNotesExcludedFromPayload(t *testing.T, raw []byte) {
	t.Helper()
	var signed appupdate.SignedManifest
	if err := json.Unmarshal(raw, &signed); err != nil {
		t.Fatalf("Unmarshal(latest.json) error = %v", err)
	}
	canonicalPayload, err := json.Marshal(signed.Payload)
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}
	if strings.Contains(string(canonicalPayload), "gray rollout") {
		t.Fatal("payload unexpectedly includes notes unsupported by appupdate manifest schema")
	}
}
