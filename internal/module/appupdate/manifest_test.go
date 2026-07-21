package appupdate

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestVerifySignedManifestAcceptsValidManifest(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testManifestPayload()
	raw := signTestManifest(t, privateKey, payload)

	gotPayload, gotArtifact, err := VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      publicKey,
		AppID:          "super-dolphin",
		Channel:        "stable",
		Platform:       "darwin-arm64",
		CurrentVersion: "1.2.2",
	})
	if err != nil {
		t.Fatalf("VerifySignedManifest() error = %v", err)
	}
	if gotPayload.Version != "v1.2.3" {
		t.Fatalf("payload version = %q, want v1.2.3", gotPayload.Version)
	}
	if gotArtifact.URL != "https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg" {
		t.Fatalf("artifact URL = %q, want signed DMG URL", gotArtifact.URL)
	}
}

func TestVerifySignedManifestRejectsTampering(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testManifestPayload()
	raw := signTestManifest(t, privateKey, payload)

	var signed SignedManifest
	if err := json.Unmarshal(raw, &signed); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	signed.Payload.Version = "v9.9.9"
	tampered, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	_, _, err = VerifySignedManifest(tampered, VerifyOptions{
		PublicKey:      publicKey,
		AppID:          "super-dolphin",
		Channel:        "stable",
		Platform:       "darwin-arm64",
		CurrentVersion: "1.2.2",
	})
	if !errors.Is(err, contract.ErrUpdateSignatureInvalid) {
		t.Fatalf("VerifySignedManifest() error = %v, want ErrUpdateSignatureInvalid", err)
	}
	if !errors.Is(err, errInvalidManifestSignature) {
		t.Fatalf("VerifySignedManifest() error = %v, want invalid-signature root cause", err)
	}
}

func TestVerifySignedManifestClassifiesSignatureDecodeFailure(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	var signed SignedManifest
	if err := json.Unmarshal(signTestManifest(t, privateKey, testManifestPayload()), &signed); err != nil {
		t.Fatal(err)
	}
	signed.Signature = "not-base64%%%"
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = VerifySignedManifest(raw, VerifyOptions{
		PublicKey: publicKey, AppID: "super-dolphin", Channel: "stable", Platform: "darwin-arm64", CurrentVersion: "1.2.2",
	})
	if !errors.Is(err, contract.ErrUpdateSignatureInvalid) {
		t.Fatalf("VerifySignedManifest() error = %v, want ErrUpdateSignatureInvalid", err)
	}
	var decodeErr base64.CorruptInputError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("VerifySignedManifest() error = %v, want base64 root cause", err)
	}
}

func TestVerifySignedManifestRejectsWrongPlatform(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	raw := signTestManifest(t, privateKey, testManifestPayload())

	_, _, err := VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      publicKey,
		AppID:          "super-dolphin",
		Channel:        "stable",
		Platform:       "windows-amd64",
		CurrentVersion: "1.2.2",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want platform mismatch")
	}
}

func TestVerifySignedManifestReturnsErrNoUpdateForNonUpgrade(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	raw := signTestManifest(t, privateKey, testManifestPayload())

	_, _, err := VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      publicKey,
		AppID:          "super-dolphin",
		Channel:        "stable",
		Platform:       "darwin-arm64",
		CurrentVersion: "1.2.3",
	})
	if !errors.Is(err, ErrNoUpdate) {
		t.Fatalf("VerifySignedManifest() error = %v, want ErrNoUpdate", err)
	}
}

func TestVerifySignedManifestRejectsNonHTTPSArtifactURL(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testManifestPayload()
	payload.Artifacts[0].URL = "http://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg"
	raw := signTestManifest(t, privateKey, payload)

	_, _, err := VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      publicKey,
		AppID:          "super-dolphin",
		Channel:        "stable",
		Platform:       "darwin-arm64",
		CurrentVersion: "1.2.2",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want non-HTTPS URL rejection")
	}
}

func TestVerifySignedManifestRejectsMalformedVersion(t *testing.T) {
	publicKey, privateKey := testManifestKeypair(t)
	payload := testManifestPayload()
	payload.Version = "v1.+2.3"
	raw := signTestManifest(t, privateKey, payload)

	_, _, err := VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      publicKey,
		AppID:          "super-dolphin",
		Channel:        "stable",
		Platform:       "darwin-arm64",
		CurrentVersion: "1.2.2",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want malformed version rejection")
	}
}

func TestVerifySignedManifestRejectsInvalidPublicKey(t *testing.T) {
	_, privateKey := testManifestKeypair(t)
	raw := signTestManifest(t, privateKey, testManifestPayload())

	_, _, err := VerifySignedManifest(raw, VerifyOptions{
		PublicKey:      []byte("short"),
		AppID:          "super-dolphin",
		Channel:        "stable",
		Platform:       "darwin-arm64",
		CurrentVersion: "1.2.2",
	})
	if err == nil {
		t.Fatal("VerifySignedManifest() error = nil, want public key length rejection")
	}
}

func TestVerifySignedManifestRejectsInvalidArtifactIntegrity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ManifestPayload)
	}{
		{
			name: "sha256",
			mutate: func(payload *ManifestPayload) {
				payload.Artifacts[0].SHA256 = "not-a-sha256"
			},
		},
		{
			name: "size",
			mutate: func(payload *ManifestPayload) {
				payload.Artifacts[0].Size = 0
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publicKey, privateKey := testManifestKeypair(t)
			payload := testManifestPayload()
			tt.mutate(&payload)
			raw := signTestManifest(t, privateKey, payload)

			_, _, err := VerifySignedManifest(raw, VerifyOptions{
				PublicKey:      publicKey,
				AppID:          "super-dolphin",
				Channel:        "stable",
				Platform:       "darwin-arm64",
				CurrentVersion: "1.2.2",
			})
			if err == nil {
				t.Fatal("VerifySignedManifest() error = nil, want artifact integrity rejection")
			}
		})
	}
}

func TestParseManifestVersionSupportsOneTwoAndThreeSegments(t *testing.T) {
	tests := []struct {
		raw  string
		want manifestVersion
	}{
		{raw: "1", want: manifestVersion{1, 0, 0}},
		{raw: "1.2", want: manifestVersion{1, 2, 0}},
		{raw: "v1.2.3", want: manifestVersion{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := parseManifestVersion(tt.raw)
			if err != nil {
				t.Fatalf("parseManifestVersion(%q) error = %v", tt.raw, err)
			}
			if got != tt.want {
				t.Fatalf("parseManifestVersion(%q) = %#v, want %#v", tt.raw, got, tt.want)
			}
		})
	}
}

func testManifestKeypair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return publicKey, privateKey
}

func testManifestPayload() ManifestPayload {
	return ManifestPayload{
		AppID:          "super-dolphin",
		Channel:        "stable",
		Version:        "v1.2.3",
		MinimumVersion: "1.0",
		Artifacts: []UpdateArtifact{{
			Platform: "darwin-arm64",
			URL:      "https://updates.example.com/Super-Dolphin-1.2.3-arm64.dmg",
			SHA256:   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Size:     12345678,
		}},
	}
}

func signTestManifest(t *testing.T, privateKey ed25519.PrivateKey, payload ManifestPayload) []byte {
	t.Helper()
	canonicalPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}
	raw, err := json.Marshal(SignedManifest{
		Payload:   payload,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalPayload)),
	})
	if err != nil {
		t.Fatalf("Marshal(signed manifest) error = %v", err)
	}
	return raw
}
