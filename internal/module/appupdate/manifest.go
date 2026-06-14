package appupdate

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

var ErrNoUpdate = errors.New("no update available")

type ManifestPayload struct {
	AppID          string           `json:"app_id"`
	Channel        string           `json:"channel"`
	Version        string           `json:"version"`
	MinimumVersion string           `json:"minimum_version,omitempty"`
	Artifacts      []UpdateArtifact `json:"artifacts"`
}

type SignedManifest struct {
	Payload   ManifestPayload `json:"payload"`
	Signature string          `json:"signature"`
}

type UpdateArtifact struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type VerifyOptions struct {
	PublicKey      []byte
	AppID          string
	Channel        string
	Platform       string
	CurrentVersion string
}

// VerifySignedManifest 验证signedmanifest。
func VerifySignedManifest(raw []byte, opts VerifyOptions) (ManifestPayload, UpdateArtifact, error) {
	if len(opts.PublicKey) != ed25519.PublicKeySize {
		return ManifestPayload{}, UpdateArtifact{}, fmt.Errorf("app update public key length = %d, want %d", len(opts.PublicKey), ed25519.PublicKeySize)
	}
	signed, err := decodeSignedManifest(raw)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := verifyManifestSignature(signed, opts.PublicKey); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := validatePayloadTarget(signed.Payload, opts); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := validateVersionWindow(signed.Payload, opts.CurrentVersion); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	artifact, err := artifactForPlatform(signed.Payload.Artifacts, opts.Platform)
	if err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	if err := validateArtifact(artifact); err != nil {
		return ManifestPayload{}, UpdateArtifact{}, err
	}
	return signed.Payload, artifact, nil
}

func decodeSignedManifest(raw []byte) (SignedManifest, error) {
	var signed SignedManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&signed); err != nil {
		return SignedManifest{}, fmt.Errorf("decode app update manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return SignedManifest{}, errors.New("decode app update manifest: trailing JSON data")
	}
	return signed, nil
}

func verifyManifestSignature(signed SignedManifest, publicKey []byte) error {
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil {
		return fmt.Errorf("decode app update signature: %w", err)
	}
	canonicalPayload, err := json.Marshal(signed.Payload)
	if err != nil {
		return fmt.Errorf("canonicalize app update payload: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKey), canonicalPayload, signature) {
		return errors.New("verify app update signature: invalid signature")
	}
	return nil
}

func validatePayloadTarget(payload ManifestPayload, opts VerifyOptions) error {
	if payload.AppID != opts.AppID {
		return fmt.Errorf("app update app_id = %q, want %q", payload.AppID, opts.AppID)
	}
	if payload.Channel != opts.Channel {
		return fmt.Errorf("app update channel = %q, want %q", payload.Channel, opts.Channel)
	}
	if strings.TrimSpace(opts.Platform) == "" {
		return errors.New("app update platform option is required")
	}
	return nil
}

// validateVersionWindow 校验版本window。
func validateVersionWindow(payload ManifestPayload, current string) error {
	updateVersion, err := parseManifestVersion(payload.Version)
	if err != nil {
		return fmt.Errorf("app update version: %w", err)
	}
	currentVersion, err := parseManifestVersion(current)
	if err != nil {
		return fmt.Errorf("current app version: %w", err)
	}
	if payload.MinimumVersion != "" {
		if err := validateMinimumVersion(payload.MinimumVersion, currentVersion); err != nil {
			return err
		}
	}
	if compareManifestVersions(updateVersion, currentVersion) <= 0 {
		return ErrNoUpdate
	}
	return nil
}

func validateMinimumVersion(minimum string, current manifestVersion) error {
	minimumVersion, err := parseManifestVersion(minimum)
	if err != nil {
		return fmt.Errorf("minimum app update version: %w", err)
	}
	if compareManifestVersions(current, minimumVersion) < 0 {
		return fmt.Errorf("current app version is below minimum update version %q", minimum)
	}
	return nil
}

func artifactForPlatform(artifacts []UpdateArtifact, platform string) (UpdateArtifact, error) {
	for _, artifact := range artifacts {
		if artifact.Platform == platform {
			return artifact, nil
		}
	}
	return UpdateArtifact{}, fmt.Errorf("app update artifact for platform %q not found", platform)
}

// validateArtifact 校验产物。
func validateArtifact(artifact UpdateArtifact) error {
	artifactURL, err := url.Parse(artifact.URL)
	if err != nil {
		return fmt.Errorf("parse app update artifact URL: %w", err)
	}
	if artifactURL.Scheme != "https" || artifactURL.Host == "" {
		return fmt.Errorf("app update artifact URL must be HTTPS with host: %q", artifact.URL)
	}
	if err := validateSHA256Hex(artifact.SHA256); err != nil {
		return err
	}
	if artifact.Size <= 0 {
		return fmt.Errorf("app update artifact size = %d, want > 0", artifact.Size)
	}
	return nil
}

func validateSHA256Hex(value string) error {
	if len(value) != 64 {
		return fmt.Errorf("app update artifact sha256 length = %d, want 64", len(value))
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return fmt.Errorf("decode app update artifact sha256: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("app update artifact sha256 decoded length = %d, want 32", len(decoded))
	}
	return nil
}

type manifestVersion [3]int

func parseManifestVersion(raw string) (manifestVersion, error) {
	value := strings.TrimPrefix(strings.TrimPrefix(raw, "v"), "V")
	if value == "" {
		return manifestVersion{}, errors.New("empty version")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 3 {
		return manifestVersion{}, fmt.Errorf("version %q has %d segments, want 1 to 3", raw, len(parts))
	}
	var version manifestVersion
	for i, part := range parts {
		number, err := parseVersionSegment(raw, part)
		if err != nil {
			return manifestVersion{}, err
		}
		version[i] = number
	}
	return version, nil
}

func parseVersionSegment(raw, part string) (int, error) {
	if part == "" {
		return 0, fmt.Errorf("version %q contains an empty segment", raw)
	}
	if !isDigitString(part) {
		return 0, fmt.Errorf("version %q segment %q is not numeric", raw, part)
	}
	number, err := strconv.Atoi(part)
	if err != nil {
		return 0, fmt.Errorf("parse version %q segment %q: %w", raw, part, err)
	}
	return number, nil
}

func isDigitString(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func compareManifestVersions(left, right manifestVersion) int {
	for i := range left {
		if left[i] > right[i] {
			return 1
		}
		if left[i] < right[i] {
			return -1
		}
	}
	return 0
}
