package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteci"
)

// buildRemoteCandidateCLI materializes the exact candidate closure and cross-compiles it once.
// A Darwin coordinator cannot execute the Linux binary, so the consumer performs cli-identity.
func buildRemoteCandidateCLI(ctx context.Context, input remoteci.RunInput, destination string) (string, error) {
	deps := liveProductionSelfUpdateDeps()
	sourceDigest, toolchainDigest, entries, err := deps.loadClosure(ctx, input.RepositoryRoot, input.Tree)
	if err != nil {
		return "", fmt.Errorf("load candidate CLI closure: %w", err)
	}
	if sourceDigest != input.CandidateGateSourceSHA256 || toolchainDigest != input.CandidateGateToolchainSHA256 {
		return "", errors.New("candidate CLI closure identity drift")
	}
	requirement, err := productionGoRequirementFromEntries(entries)
	if err != nil {
		return "", err
	}
	toolchain, err := deps.resolveToolchain(requirement)
	if err != nil {
		return "", fmt.Errorf("resolve trusted candidate Go toolchain: %w", err)
	}
	toolchain.GOOS, toolchain.GOARCH = "linux", "amd64"
	candidate, cleanup, err := buildProductionCLICandidate(ctx, destination, sourceDigest, toolchainDigest, entries, toolchain, deps)
	if err != nil {
		return "", err
	}
	defer func() { _ = cleanup() }()
	output := filepath.Join(destination, "super-dolphin-gate-linux-amd64")
	source, err := os.Open(candidate)
	if err != nil {
		return "", err
	}
	defer source.Close()
	target, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return "", err
	}
	return output, nil
}
