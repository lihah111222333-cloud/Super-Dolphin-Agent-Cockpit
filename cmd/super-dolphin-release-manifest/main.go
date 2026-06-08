package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/module/appupdate"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type manifestFlags struct {
	artifact       string
	artifactURL    string
	appID          string
	channel        string
	version        string
	minimumVersion string
	platform       string
	signingKey     string
	publicKey      string
	checkKey       bool
	verifyManifest string
	currentVersion string
	out            string
	notes          string
}

func main() {
	pkglogger.InitWithConsoleWriter(os.Stderr)
	if err := run(os.Args[1:]); err != nil {
		pkglogger.Get().Error("super-dolphin-release-manifest failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := parseFlags(args)
	if err != nil {
		return err
	}
	if cfg.checkKey {
		return verifySigningKeyMatchesPublicKeyPath(cfg.signingKey, cfg.publicKey)
	}
	if cfg.verifyManifest != "" {
		return verifyExistingManifest(cfg)
	}
	artifact, err := inspectArtifact(cfg.artifact)
	if err != nil {
		return err
	}
	privateKey, err := readSigningKey(cfg.signingKey)
	if err != nil {
		return err
	}
	if cfg.publicKey != "" {
		if err := verifySigningKeyMatchesPublicKey(privateKey, cfg.publicKey); err != nil {
			return err
		}
	}
	payload := appupdate.ManifestPayload{
		AppID:          cfg.appID,
		Channel:        cfg.channel,
		Version:        cfg.version,
		MinimumVersion: cfg.minimumVersion,
		Artifacts: []appupdate.UpdateArtifact{{
			Platform: cfg.platform,
			URL:      cfg.artifactURL,
			SHA256:   artifact.sha256,
			Size:     artifact.size,
		}},
	}
	return writeSignedManifest(cfg.out, privateKey, payload)
}

func parseFlags(args []string) (manifestFlags, error) {
	var cfg manifestFlags
	flags := flag.NewFlagSet("super-dolphin-release-manifest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.artifact, "artifact", "", "path to DMG artifact")
	flags.StringVar(&cfg.artifactURL, "artifact-url", "", "published artifact URL")
	flags.StringVar(&cfg.appID, "app-id", "", "application identifier")
	flags.StringVar(&cfg.channel, "channel", "", "release channel")
	flags.StringVar(&cfg.version, "version", "", "release version")
	flags.StringVar(&cfg.minimumVersion, "minimum-version", "", "minimum supported app version")
	flags.StringVar(&cfg.platform, "platform", "", "artifact platform")
	flags.StringVar(&cfg.signingKey, "signing-key", "", "64-byte Ed25519 private key path")
	flags.StringVar(&cfg.publicKey, "public-key", "", "base64-encoded 32-byte Ed25519 public key")
	flags.BoolVar(&cfg.checkKey, "check-key", false, "verify signing key derives the provided public key")
	flags.StringVar(&cfg.verifyManifest, "verify-manifest", "", "verify an existing signed update manifest")
	flags.StringVar(&cfg.currentVersion, "current-version", "", "current version used for update-window verification")
	flags.StringVar(&cfg.out, "out", "", "output latest.json path")
	flags.StringVar(&cfg.notes, "notes", "", "release notes accepted for release tooling compatibility")
	if err := flags.Parse(args); err != nil {
		return manifestFlags{}, err
	}
	if flags.NArg() != 0 {
		return manifestFlags{}, fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if err := requireFlagValues(cfg); err != nil {
		return manifestFlags{}, err
	}
	return cfg, nil
}

func requireFlagValues(cfg manifestFlags) error {
	if cfg.checkKey {
		return requireNamedValues(map[string]string{
			"signing-key": cfg.signingKey,
			"public-key":  cfg.publicKey,
		})
	}
	if cfg.verifyManifest != "" {
		return requireNamedValues(map[string]string{
			"artifact":        cfg.artifact,
			"artifact-url":    cfg.artifactURL,
			"app-id":          cfg.appID,
			"channel":         cfg.channel,
			"version":         cfg.version,
			"minimum-version": cfg.minimumVersion,
			"platform":        cfg.platform,
			"public-key":      cfg.publicKey,
		})
	}
	required := map[string]string{
		"artifact":        cfg.artifact,
		"artifact-url":    cfg.artifactURL,
		"app-id":          cfg.appID,
		"channel":         cfg.channel,
		"version":         cfg.version,
		"minimum-version": cfg.minimumVersion,
		"platform":        cfg.platform,
		"signing-key":     cfg.signingKey,
		"out":             cfg.out,
	}
	return requireNamedValues(required)
}

func requireNamedValues(required map[string]string) error {
	for name, value := range required {
		if value == "" {
			return fmt.Errorf("-%s is required", name)
		}
	}
	return nil
}

type artifactIntegrity struct {
	sha256 string
	size   int64
}

func inspectArtifact(path string) (artifactIntegrity, error) {
	file, err := os.Open(path)
	if err != nil {
		return artifactIntegrity{}, fmt.Errorf("open artifact: %w", err)
	}
	defer file.Close()
	hasher := sha256.New()
	size, err := io.Copy(hasher, file)
	if err != nil {
		return artifactIntegrity{}, fmt.Errorf("hash artifact: %w", err)
	}
	if size <= 0 {
		return artifactIntegrity{}, fmt.Errorf("artifact size = %d, want > 0", size)
	}
	return artifactIntegrity{
		sha256: hex.EncodeToString(hasher.Sum(nil)),
		size:   size,
	}, nil
}

func readSigningKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing key length = %d, want %d", len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

func verifySigningKeyMatchesPublicKeyPath(signingKeyPath, publicKeyValue string) error {
	privateKey, err := readSigningKey(signingKeyPath)
	if err != nil {
		return err
	}
	return verifySigningKeyMatchesPublicKey(privateKey, publicKeyValue)
}

func verifySigningKeyMatchesPublicKey(privateKey ed25519.PrivateKey, publicKeyValue string) error {
	publicKey, err := decodePublicKey(publicKeyValue)
	if err != nil {
		return err
	}
	derived, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("derive signing public key: unexpected key type")
	}
	if !bytes.Equal(derived, publicKey) {
		return fmt.Errorf("signing key public key does not match SUPER_DOLPHIN_UPDATE_PUBLIC_KEY")
	}
	return nil
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key length = %d, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

func verifyExistingManifest(cfg manifestFlags) error {
	artifact, err := inspectArtifact(cfg.artifact)
	if err != nil {
		return err
	}
	publicKey, err := decodePublicKey(cfg.publicKey)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(cfg.verifyManifest)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	currentVersion := cfg.currentVersion
	if currentVersion == "" {
		currentVersion = cfg.minimumVersion
	}
	_, selected, err := appupdate.VerifySignedManifest(raw, appupdate.VerifyOptions{
		PublicKey:      publicKey,
		AppID:          cfg.appID,
		Channel:        cfg.channel,
		Platform:       cfg.platform,
		CurrentVersion: currentVersion,
	})
	if err != nil {
		return err
	}
	return verifyManifestArtifact(selected, artifact, cfg)
}

func verifyManifestArtifact(selected appupdate.UpdateArtifact, artifact artifactIntegrity, cfg manifestFlags) error {
	if selected.Platform != cfg.platform {
		return fmt.Errorf("manifest artifact platform = %q, want %q", selected.Platform, cfg.platform)
	}
	if selected.URL != cfg.artifactURL {
		return fmt.Errorf("manifest artifact URL = %q, want %q", selected.URL, cfg.artifactURL)
	}
	if selected.SHA256 != artifact.sha256 {
		return fmt.Errorf("manifest artifact sha256 = %s, want %s", selected.SHA256, artifact.sha256)
	}
	if selected.Size != artifact.size {
		return fmt.Errorf("manifest artifact size = %d, want %d", selected.Size, artifact.size)
	}
	return nil
}

func writeSignedManifest(path string, privateKey ed25519.PrivateKey, payload appupdate.ManifestPayload) error {
	canonicalPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("canonicalize manifest payload: %w", err)
	}
	signed := appupdate.SignedManifest{
		Payload:   payload,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, canonicalPayload)),
	}
	raw, err := json.MarshalIndent(signed, "", "  ")
	if err != nil {
		return fmt.Errorf("encode signed manifest: %w", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return fmt.Errorf("write signed manifest: %w", err)
	}
	return nil
}
