package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func buildProductionBootstrapFixtureImage(t *testing.T, contextRoot string, tag string) string {
	t.Helper()
	metadataPath := filepath.Join(contextRoot, "metadata.json")
	runBootstrapDocker(
		t,
		"buildx", "build",
		"--load",
		"--provenance=false",
		"--network=none",
		"--tag="+tag,
		"--metadata-file="+metadataPath,
		contextRoot,
	)
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	var digest string
	if err := json.Unmarshal(metadata["containerimage.digest"], &digest); err != nil {
		t.Fatalf("read fixture manifest digest: %v", err)
	}
	return digest
}

func inspectProductionBootstrapFixtureImage(t *testing.T, tag string) productionBootstrapImageInspect {
	t.Helper()
	output := runBootstrapDocker(t, "image", "inspect", tag)
	document, err := decodeProductionBootstrapInspect([]byte(output))
	if err != nil {
		t.Fatal(err)
	}
	if document.Descriptor == nil || document.RootFS == nil {
		t.Skip("Docker image store does not expose descriptor/rootfs identity")
	}
	return document
}

func productionBootstrapFixtureIdentity(
	repository string,
	manifestDigest string,
	document productionBootstrapImageInspect,
) gatecontract.ImageIdentity {
	return gatecontract.ImageIdentity{
		Registry: repository, OCIIndexDigest: manifestDigest, PlatformManifestDigest: manifestDigest,
		ConfigDigest:  document.Descriptor.Annotations["config.digest"],
		RootFSDiffIDs: append([]string(nil), document.RootFS.Layers...),
		OS:            document.OS, Architecture: document.Architecture, Variant: document.Variant,
	}
}

func runBootstrapDocker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), coordinatorTimeout(gatecontract.ProfileLocalFast))
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf(
				"docker stage %q exceeded normal profile deadline %s: %s",
				args[0],
				coordinatorTimeout(gatecontract.ProfileLocalFast),
				strings.TrimSpace(string(output)),
			)
		}
		t.Fatalf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output)
}

func configureProductionHostDockerCommandTest(t *testing.T) (string, string, string) {
	t.Helper()
	for _, name := range []string{
		"DOCKER_HOST",
		"DOCKER_CONTEXT",
		"DOCKER_TLS",
		"DOCKER_TLS_VERIFY",
		"DOCKER_CERT_PATH",
	} {
		unsetProductionHostDockerTestEnvironment(t, name)
	}
	home := canonicalProductionHostDockerTestDirectory(t, t.TempDir())
	configDir := canonicalProductionHostDockerTestDirectory(t, t.TempDir())
	if err := os.Chmod(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	if path == "" {
		t.Fatal("PATH is required")
	}
	t.Setenv("HOME", home)
	t.Setenv("DOCKER_CONFIG", configDir)
	return home, configDir, path
}

func canonicalProductionHostDockerTestDirectory(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func unsetProductionHostDockerTestEnvironment(t *testing.T, name string) {
	t.Helper()
	value, exists := os.LookupEnv(name)
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var err error
		if exists {
			err = os.Setenv(name, value)
		} else {
			err = os.Unsetenv(name)
		}
		if err != nil {
			t.Errorf("restore %s: %v", name, err)
		}
	})
}
