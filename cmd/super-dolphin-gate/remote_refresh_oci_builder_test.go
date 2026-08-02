package main

import (
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func TestValidateRemoteOCIBuildReceiptRejectsEveryIdentityDrift(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	request := remoteOCIBuildRequest{
		SchemaVersion:   remoteOCIBuildReceiptSchemaVersion,
		MainTree:        strings.Repeat("b", 40),
		ToolchainDigest: digest,
		Platform:        "linux/amd64",
		Build:           localci.BuildKitBuildRequest{InputDigest: digest},
	}
	receipt := remoteOCIBuildReceipt{
		SchemaVersion: remoteOCIBuildReceiptSchemaVersion,
		MainTree:      request.MainTree, ToolchainDigest: request.ToolchainDigest,
		Platform: request.Platform, InputDigest: request.Build.InputDigest,
		ImageDigest: digest, ConfigDigest: digest,
	}
	if err := validateRemoteOCIBuildReceipt(request, receipt); err != nil {
		t.Fatalf("validate matching receipt: %v", err)
	}
	for _, mutate := range []struct {
		name  string
		apply func(*remoteOCIBuildReceipt)
	}{
		{"schema", func(got *remoteOCIBuildReceipt) { got.SchemaVersion++ }},
		{"main tree", func(got *remoteOCIBuildReceipt) { got.MainTree = strings.Repeat("c", 40) }},
		{"toolchain", func(got *remoteOCIBuildReceipt) { got.ToolchainDigest = "sha256:" + strings.Repeat("c", 64) }},
		{"platform", func(got *remoteOCIBuildReceipt) { got.Platform = "linux/arm64" }},
		{"input", func(got *remoteOCIBuildReceipt) { got.InputDigest = "sha256:" + strings.Repeat("c", 64) }},
		{"image digest", func(got *remoteOCIBuildReceipt) { got.ImageDigest = "invalid" }},
		{"config digest", func(got *remoteOCIBuildReceipt) { got.ConfigDigest = "invalid" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			got := receipt
			mutate.apply(&got)
			if err := validateRemoteOCIBuildReceipt(request, got); err == nil {
				t.Fatal("validate drifted receipt succeeded")
			}
		})
	}
}
