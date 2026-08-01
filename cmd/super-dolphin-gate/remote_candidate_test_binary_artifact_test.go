package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

func TestBuilderRequestShardPreservesDirectCacheReference(t *testing.T) {
	reference := &remoteci.DirectCacheRef{DataCacheID: "edc-direct"}
	shard := builderRequestShard(remoteci.CandidateTestBinaryBuilderRequest{DirectCacheRef: reference})
	if shard.DirectCacheRef != reference {
		t.Fatalf("builder shard direct cache = %#v, want %#v", shard.DirectCacheRef, reference)
	}
}

func TestMaterializeRemoteCandidateTestBinariesInstallsOnlyVerifiedBundle(t *testing.T) {
	tree := strings.Repeat("a", 40)
	binary := []byte("#!/bin/sh\nexit 0\n")
	binaryDigest := digestBytes(binary)
	manifest := remoteci.CandidateTestBinaryArtifactManifest{
		SchemaVersion: remoteci.CandidateTestBinaryArtifactSchemaVersion, CandidateTree: tree,
		Package: "example.test/internal/gate", Mode: "test", Platform: "linux/amd64", GoToolchain: "go1.26.5", CGOEnabled: true,
		BuildFlags:      []string{"-mod=readonly", "-buildvcs=false", "-trimpath"},
		ToolchainSHA256: "sha256:" + strings.Repeat("b", 64), CompileClosureSHA256: "sha256:" + strings.Repeat("c", 64),
		BinaryKey: "candidate/job/" + binaryDigest + ".test-bin", BinarySHA256: "sha256:" + binaryDigest, BinarySize: int64(len(binary)),
	}
	data, manifestDigest, err := remoteci.EncodeCandidateTestBinaryArtifactManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	ref := remoteci.CandidateTestBinaryArtifactRef{CandidateTree: tree, Package: manifest.Package, Mode: manifest.Mode, Platform: manifest.Platform, GoToolchain: manifest.GoToolchain, CGOEnabled: manifest.CGOEnabled, ToolchainSHA256: manifest.ToolchainSHA256, BuildFlags: manifest.BuildFlags, CompileClosureSHA256: manifest.CompileClosureSHA256, ManifestKey: "candidate/job/manifest.manifest.json", ManifestSHA256: strings.TrimPrefix(manifestDigest, "sha256:"), BinaryKey: manifest.BinaryKey, BinarySHA256: binaryDigest, BinarySize: int64(len(binary))}
	download := func(_ context.Context, key string, _ int64, destination io.Writer) (int64, error) {
		switch key {
		case ref.ManifestKey:
			n, writeErr := destination.Write(data)
			return int64(n), writeErr
		case ref.BinaryKey:
			n, writeErr := destination.Write(binary)
			return int64(n), writeErr
		default:
			return 0, fmt.Errorf("unexpected object key %q", key)
		}
	}
	index, err := materializeRemoteCandidateTestBinaries(context.Background(), t.TempDir(), tree, []remoteci.CandidateTestBinaryArtifactRef{ref}, download)
	if err != nil {
		t.Fatalf("materializeRemoteCandidateTestBinaries() error = %v", err)
	}
	info, err := os.Stat(index)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("index mode = %v, error = %v", info.Mode(), err)
	}
}

func TestMaterializeRemoteCandidateTestBinariesSkipsGuardOnlyShard(t *testing.T) {
	called := false
	index, err := materializeRemoteCandidateTestBinaries(context.Background(), t.TempDir(), strings.Repeat("a", 40), nil, func(context.Context, string, int64, io.Writer) (int64, error) {
		called = true
		return 0, nil
	})
	if err != nil || index != "" || called {
		t.Fatalf("guard-only materialization = index %q, err %v, called %v", index, err, called)
	}
}
