package heterocinode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type linuxDockerRunnerFixture struct {
	outputs map[string]string
	errors  map[string]error
}

func (fixture linuxDockerRunnerFixture) Run(_ context.Context, args ...string) (string, error) {
	key := strings.Join(args, "\x00")
	return fixture.outputs[key], fixture.errors[key]
}

func TestLinuxFactsProviderCollectsCapacityAndIgnoresCurrentOwnerContainers(t *testing.T) {
	config := testConfig()
	root := t.TempDir()
	writeLinuxFact(t, filepath.Join(root, "cpu.max"), "400000 100000\n")
	writeLinuxFact(t, filepath.Join(root, "memory.max"), fmt.Sprintf("%d\n", 8<<30))
	boot := filepath.Join(t.TempDir(), "boot-id")
	writeLinuxFact(t, boot, "boot-linux\n")
	runner := linuxDockerRunnerFixture{outputs: map[string]string{
		strings.Join([]string{"info", "--format", "{{.NCPU}} {{.MemTotal}}"}, "\x00"):                                "8 17179869184",
		strings.Join([]string{"ps", "--quiet", "--no-trunc"}, "\x00"):                                                "current\nexternal",
		strings.Join([]string{"inspect", "--format", linuxDockerReservationFormat, "current"}, "\x00"):               "7 2000000000 2147483648",
		strings.Join([]string{"inspect", "--format", linuxDockerReservationFormat, "external"}, "\x00"):              "6 1000000000 1073741824",
		strings.Join([]string{"image", "inspect", "--format", "{{.Id}}", config.Node.Capabilities[0].Image}, "\x00"): config.Node.Capabilities[0].ImageDigest,
	}}
	provider := &LinuxFactsProvider{runner: runner, cgroupRoot: root, bootIDPath: boot}
	facts, err := provider.Collect(context.Background(), config, heteroci.ProbeRequest{OwnerEpoch: 7})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if facts.Total.CPUMilli != 4_000 || facts.Available.CPUMilli != 3_000 || facts.Available.MemoryBytes != 7<<30 || facts.Available.MaxConcurrency != 3 {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestLinuxFactsProviderRejectsUnboundedExternalContainerAndCapability(t *testing.T) {
	config := testConfig()
	root := t.TempDir()
	writeLinuxFact(t, filepath.Join(root, "cpu.max"), "max 100000\n")
	writeLinuxFact(t, filepath.Join(root, "memory.max"), "max\n")
	boot := filepath.Join(t.TempDir(), "boot-id")
	writeLinuxFact(t, boot, "boot-linux\n")
	provider := &LinuxFactsProvider{runner: linuxDockerRunnerFixture{
		outputs: map[string]string{
			strings.Join([]string{"info", "--format", "{{.NCPU}} {{.MemTotal}}"}, "\x00"):                   "8 17179869184",
			strings.Join([]string{"ps", "--quiet", "--no-trunc"}, "\x00"):                                   "external",
			strings.Join([]string{"inspect", "--format", linuxDockerReservationFormat, "external"}, "\x00"): "6 0 0",
		},
	}, cgroupRoot: root, bootIDPath: boot}
	if _, err := provider.Collect(context.Background(), config, heteroci.ProbeRequest{OwnerEpoch: 7}); err == nil {
		t.Fatal("Collect() accepted an unbounded external reservation")
	}
	config.Node.Capabilities[0].Runtime = heteroci.RuntimeDarlingCompatRuntime
	if _, err := provider.verifyLinuxCapabilities(context.Background(), config.Node.Capabilities); err == nil {
		t.Fatal("verifyLinuxCapabilities() accepted unqualified runtime")
	}
}

func TestNewLinuxFactsProviderRejectsImplicitOrSymlinkPaths(t *testing.T) {
	root := t.TempDir()
	docker := filepath.Join(root, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	boot := filepath.Join(root, "boot")
	writeLinuxFact(t, boot, "boot\n")
	config := LinuxFactsProviderConfig{DockerExecutable: docker, CgroupRoot: root, BootIDPath: boot, Timeout: 0, WaitDelay: 0}
	if _, err := NewLinuxFactsProvider(config); err == nil {
		t.Fatal("NewLinuxFactsProvider() accepted missing timing")
	}
	link := filepath.Join(t.TempDir(), "docker-link")
	if err := os.Symlink(docker, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	config.DockerExecutable = link
	config.Timeout = 5e9
	config.WaitDelay = 1e9
	if _, err := NewLinuxFactsProvider(config); err == nil {
		t.Fatal("NewLinuxFactsProvider() accepted symlink executable")
	}
}

func writeLinuxFact(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

var _ linuxDockerRunner = linuxDockerRunnerFixture{}
