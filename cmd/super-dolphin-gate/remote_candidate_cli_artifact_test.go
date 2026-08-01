package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestMaterializeRemoteCandidateCLIArtifactRejectsCandidateIdentityDrift(t *testing.T) {
	manifest := remoteci.CandidateCLIArtifactManifest{SchemaVersion: remoteci.CandidateCLIArtifactSchemaVersion, CandidateTree: strings.Repeat("a", 40), SourceSHA256: "sha256:" + strings.Repeat("b", 64), ToolchainSHA256: "sha256:" + strings.Repeat("c", 64), Platform: "linux/amd64", BinaryKey: "candidate-artifacts/job-012/candidate-linux-amd64.candidate-cli", BinarySHA256: "sha256:" + strings.Repeat("d", 64), BinarySize: 1}
	manifest.CLIIdentity = remoteci.CandidateCLIIdentity(manifest.SourceSHA256, manifest.ToolchainSHA256)
	data, digest, err := remoteci.EncodeCandidateCLIArtifactManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	remoteDownload := func(_ context.Context, key string, _ int64, destination io.Writer) (int64, error) {
		if key != "candidate-artifacts/job-012/manifest.json" {
			t.Fatalf("unexpected key %q", key)
		}
		n, err := destination.Write(data)
		return int64(n), err
	}
	_, err = materializeRemoteCandidateCLIArtifact(context.Background(), t.TempDir(), "candidate-artifacts/job-012/manifest.json", digest, strings.Repeat("e", 40), manifest.SourceSHA256, manifest.ToolchainSHA256, remoteDownload)
	if err == nil || !strings.Contains(err.Error(), "does not match shard candidate identity") {
		t.Fatalf("error = %v", err)
	}
}

func TestMaterializeRemoteCandidateCLIArtifactRejectsTamperedManifest(t *testing.T) {
	sourceDigest := "sha256:" + strings.Repeat("b", 64)
	toolchainDigest := "sha256:" + strings.Repeat("c", 64)
	manifest := remoteci.CandidateCLIArtifactManifest{SchemaVersion: remoteci.CandidateCLIArtifactSchemaVersion, CandidateTree: strings.Repeat("a", 40), SourceSHA256: sourceDigest, ToolchainSHA256: toolchainDigest, Platform: "linux/amd64", BinaryKey: "candidate-artifacts/job-012/candidate-linux-amd64.candidate-cli", BinarySHA256: "sha256:" + strings.Repeat("d", 64), BinarySize: 1}
	manifest.CLIIdentity = remoteci.CandidateCLIIdentity(sourceDigest, toolchainDigest)
	data, digest, err := remoteci.EncodeCandidateCLIArtifactManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append(append([]byte{}, data...), '\n')
	download := func(_ context.Context, key string, _ int64, destination io.Writer) (int64, error) {
		if key != "candidate-artifacts/job-012/manifest.json" {
			t.Fatalf("unexpected key %q", key)
		}
		n, err := destination.Write(tampered)
		return int64(n), err
	}
	if _, err := materializeRemoteCandidateCLIArtifact(context.Background(), t.TempDir(), "candidate-artifacts/job-012/manifest.json", digest, manifest.CandidateTree, sourceDigest, toolchainDigest, download); err == nil {
		t.Fatal("materializeRemoteCandidateCLIArtifact() accepted tampered manifest")
	}
}

func TestMaterializeRemoteCandidateCLIArtifactPersistsVerifiedBinary(t *testing.T) {
	workRoot := t.TempDir()
	sourceDigest := "sha256:" + strings.Repeat("b", 64)
	toolchainDigest := "sha256:" + strings.Repeat("c", 64)
	identity := remoteci.CandidateCLIIdentity(sourceDigest, toolchainDigest)
	binary := []byte("#!/bin/sh\nif test \"$1 $2\" = 'worker cli-identity'; then\n  printf '%s\\n' '" + identity + "'\n  exit 0\nfi\nexit 1\n")
	binaryDigest := "sha256:" + digestBytes(binary)
	manifest := remoteci.CandidateCLIArtifactManifest{
		SchemaVersion: remoteci.CandidateCLIArtifactSchemaVersion,
		CandidateTree: strings.Repeat("a", 40), SourceSHA256: sourceDigest, ToolchainSHA256: toolchainDigest,
		Platform: "linux/amd64", BinaryKey: "candidate-artifacts/" + strings.Repeat("a", 40) + "/candidate-linux-amd64.candidate-cli",
		BinarySHA256: binaryDigest, BinarySize: int64(len(binary)), CLIIdentity: identity,
	}
	manifestData, manifestDigest, err := remoteci.EncodeCandidateCLIArtifactManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	download := func(_ context.Context, key string, _ int64, destination io.Writer) (int64, error) {
		var data []byte
		switch key {
		case "candidate-artifacts/" + strings.Repeat("a", 40) + "/manifest.json":
			data = manifestData
		case manifest.BinaryKey:
			data = binary
		default:
			return 0, fmt.Errorf("unexpected object key %q", key)
		}
		n, err := destination.Write(data)
		return int64(n), err
	}
	path, err := materializeRemoteCandidateCLIArtifact(context.Background(), workRoot, "candidate-artifacts/"+strings.Repeat("a", 40)+"/manifest.json", manifestDigest, manifest.CandidateTree, sourceDigest, toolchainDigest, download)
	if err != nil {
		t.Fatalf("materializeRemoteCandidateCLIArtifact() error = %v", err)
	}
	if path != filepath.Join(workRoot, "bin", "super-dolphin-gate") {
		t.Fatalf("binary path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(binary) {
		t.Fatalf("persistent binary = %q, %v", data, err)
	}
}
