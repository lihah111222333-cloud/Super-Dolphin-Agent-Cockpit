package heterocinode

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

type linuxDockerRunnerFixture struct {
	outputs map[string]string
	errors  map[string]error
}

const linuxDockerServerIDFixture = "docker-server-fixture"

type linuxPhysicalAttestorFixture struct {
	attestation heteroci.PhysicalHostAttestation
	err         error
}

func (fixture linuxPhysicalAttestorFixture) Attest(context.Context) (heteroci.PhysicalHostAttestation, error) {
	if fixture.err != nil {
		return heteroci.PhysicalHostAttestation{}, fixture.err
	}
	return fixture.attestation, nil
}

func linuxWineQualificationFixtureKey(imageID string) string {
	return strings.Join([]string{"run", "--rm", "--network", "none", "--user", "65532:65532", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev", "--entrypoint", "/bin/sh", imageID, "-c", linuxWineQualificationScript}, "\x00")
}

func TestLinuxWineQualificationUsesWritableProbePrefixAndRegular64BitBinaries(t *testing.T) {
	for _, required := range []string{
		"export WINEARCH=win64",
		"baked_prefix=\"${WINEPREFIX:?}\"",
		"mkdir -p /tmp/wine-prefix",
		"cp -a -- \"$baked_prefix\"/. /tmp/wine-prefix/",
		"export WINEPREFIX=/tmp/wine-prefix",
		"export WINELOADER=/usr/lib/wine/wine64",
		"export WINESERVER=/usr/lib/wine/wineserver64",
		"$(/usr/bin/dpkg-query -W -f='${Version}' wine64)",
		linuxWineExpectedPackageVersion,
		"/usr/lib/wine/wine64 /opt/super-dolphin/smoke/wine-smoke.exe >/dev/null 2>&1",
		"/usr/lib/wine/wineserver64 -w >/dev/null 2>&1",
		"/usr/lib/wine/wineserver64 -k >/dev/null 2>&1",
	} {
		if !strings.Contains(linuxWineQualificationScript, required) {
			t.Fatalf("Wine qualification script is missing %q: %s", required, linuxWineQualificationScript)
		}
	}
	for _, forbidden := range []string{"/usr/bin/wine", "/usr/bin/wineserver", "WINEPREFIX=/home/ci-runner/.wine64"} {
		if strings.Contains(linuxWineQualificationScript, forbidden) {
			t.Fatalf("Wine qualification script retains forbidden path %q: %s", forbidden, linuxWineQualificationScript)
		}
	}
	if count := strings.Count(linuxWineQualificationScript, "/usr/bin/wine"); count != 0 {
		t.Fatalf("Wine wrapper must not be invoked, count=%d script=%s", count, linuxWineQualificationScript)
	}
	killIndex := strings.Index(linuxWineQualificationScript, "/usr/lib/wine/wineserver64 -k")
	waitIndex := strings.Index(linuxWineQualificationScript, "/usr/lib/wine/wineserver64 -w")
	if killIndex < 0 || waitIndex < 0 || killIndex >= waitIndex {
		t.Fatalf("Wine server shutdown/wait order is invalid: kill=%d wait=%d script=%s", killIndex, waitIndex, linuxWineQualificationScript)
	}
}

func TestLinuxWineQualificationRedirectsFakeNoiseBeforeStrictStdout(t *testing.T) {
	commands := []string{
		"/usr/bin/xvfb-run --auto-servernum",
		"/usr/lib/wine/wineserver64 -k",
		"/usr/lib/wine/wineserver64 -w",
	}
	for _, command := range commands {
		start := strings.Index(linuxWineQualificationScript, command)
		if start < 0 {
			t.Fatalf("qualification command %q is missing: %s", command, linuxWineQualificationScript)
		}
		end := strings.Index(linuxWineQualificationScript[start:], ";")
		if end < 0 || !strings.Contains(linuxWineQualificationScript[start:start+end], ">/dev/null 2>&1") {
			t.Fatalf("fake stdout/stderr noise from %q is not redirected: %s", command, linuxWineQualificationScript)
		}
	}
	printfIndex := strings.LastIndex(linuxWineQualificationScript, "printf '%s")
	if printfIndex < 0 {
		t.Fatalf("strict qualification stdout marker is missing: %s", linuxWineQualificationScript)
	}
	if strings.Contains(linuxWineQualificationScript[printfIndex:], "2>/dev/null") {
		t.Fatalf("strict stdout marker unexpectedly redirects its output: %s", linuxWineQualificationScript)
	}
}

func TestLinuxWineQualificationFakeDockerArgvKeepsReadOnlyNoNetworkBoundary(t *testing.T) {
	key := linuxWineQualificationFixtureKey("sha256:wine-image")
	for _, required := range []string{"run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev", "sha256:wine-image", "-c", linuxWineQualificationScript} {
		if !strings.Contains(key, required) {
			t.Fatalf("fake Docker argv is missing %q: %q", required, key)
		}
	}
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
		strings.Join([]string{"info", "--format", "{{.ID}}"}, "\x00"):                                                linuxDockerServerIDFixture,
		strings.Join([]string{"info", "--format", "{{.NCPU}} {{.MemTotal}}"}, "\x00"):                                "8 17179869184",
		strings.Join([]string{"ps", "--quiet", "--no-trunc"}, "\x00"):                                                "current\nexternal",
		strings.Join([]string{"inspect", "--format", linuxDockerReservationFormat, "current"}, "\x00"):               "7 2000000000 2147483648",
		strings.Join([]string{"inspect", "--format", linuxDockerReservationFormat, "external"}, "\x00"):              "6 1000000000 1073741824",
		strings.Join([]string{"image", "inspect", "--format", "{{.Id}}", config.Node.Capabilities[0].Image}, "\x00"): config.Node.Capabilities[0].ImageDigest,
		strings.Join([]string{"image", "inspect", "--format", "{{.Id}}", config.Node.Capabilities[1].Image}, "\x00"): config.Node.Capabilities[1].ImageDigest,
		linuxWineQualificationFixtureKey(config.Node.Capabilities[1].ImageDigest):                                    linuxWineExpectedVersion + "|smoke-ok",
	}}
	provider := &LinuxFactsProvider{runner: runner, attestor: linuxPhysicalAttestorFixture{attestation: testPhysicalAttestation()}, dockerServerID: linuxDockerServerIDFixture, cgroupRoot: root, bootIDPath: boot}
	facts, err := provider.Collect(context.Background(), config, heteroci.ProbeRequest{OwnerEpoch: 7})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(facts.Capabilities) != 2 || facts.Capabilities[0].Runtime != heteroci.RuntimeUbuntuContainer || facts.Capabilities[1].Runtime != heteroci.RuntimeUbuntuWineContainer || facts.Total.CPUMilli != 4_000 || facts.Available.CPUMilli != 3_000 || facts.Available.MemoryBytes != 7<<30 || facts.Available.MaxConcurrency != 3 {
		t.Fatalf("facts = %#v", facts)
	}
}

func TestLinuxFactsProviderRejectsWineQualificationOutputDrift(t *testing.T) {
	config := testConfig()
	provider := &LinuxFactsProvider{runner: linuxDockerRunnerFixture{outputs: map[string]string{
		strings.Join([]string{"image", "inspect", "--format", "{{.Id}}", config.Node.Capabilities[0].Image}, "\x00"): config.Node.Capabilities[0].ImageDigest,
		strings.Join([]string{"image", "inspect", "--format", "{{.Id}}", config.Node.Capabilities[1].Image}, "\x00"): config.Node.Capabilities[1].ImageDigest,
		linuxWineQualificationFixtureKey(config.Node.Capabilities[1].ImageDigest):                                    "wine-9.0|smoke-drift",
	}}}
	if _, err := provider.verifyLinuxCapabilities(context.Background(), config.Node.Capabilities); err == nil {
		t.Fatal("verifyLinuxCapabilities accepted drifted Wine qualification output")
	}
}

func TestLinuxFactsProviderRejectsDockerServerIdentityDrift(t *testing.T) {
	provider := &LinuxFactsProvider{
		runner:         linuxDockerRunnerFixture{outputs: map[string]string{strings.Join([]string{"info", "--format", "{{.ID}}"}, "\x00"): "other-server"}},
		dockerServerID: linuxDockerServerIDFixture,
	}
	if err := provider.verifyDockerServer(context.Background()); err == nil {
		t.Fatal("verifyDockerServer() accepted a drifted Docker server identity")
	}
}

func TestLinuxPhysicalAttestorRejectsOSMachineAndDockerPlatformDrift(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	osRelease := filepath.Join(root, "os-release")
	uname := filepath.Join(root, "uname")
	machineID := filepath.Join(root, "machine-id")
	if err := os.WriteFile(osRelease, []byte("ID=ubuntu\nNAME=Ubuntu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(uname, []byte("#!/bin/sh\nprintf '%s\\n' x86_64\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineID, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, machineIDIdentity, err := readStableRegularFile(machineID, 256)
	if err != nil {
		t.Fatal(err)
	}
	_, osReleaseIdentity, err := readStableRegularFile(osRelease, 16<<10)
	if err != nil {
		t.Fatal(err)
	}
	_, unameIdentity, err := readStableRegularFile(uname, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	dockerInfoKey := strings.Join([]string{"info", "--format", "{{.OSType}} {{.Architecture}}"}, "\x00")
	newAttestor := func(machinePath, osPath, unamePath, dockerPlatform string) linuxExecPhysicalAttestor {
		return linuxExecPhysicalAttestor{
			dockerRunner:  linuxDockerRunnerFixture{outputs: map[string]string{dockerInfoKey: dockerPlatform}},
			machineIDPath: machinePath, machineIDSHA256: machineIDIdentity,
			osReleasePath: osPath, osReleaseIdentity: osReleaseIdentity,
			unameExecutable: unamePath, unameIdentity: unameIdentity,
		}
	}
	valid, err := newAttestor(machineID, osRelease, uname, "linux x86_64").Attest(context.Background())
	if err != nil || valid.OSID != "ubuntu" || valid.Machine != "x86_64" || valid.MachineIDSHA256 != machineIDIdentity || valid.OSReleaseIdentity != osReleaseIdentity || valid.UnameIdentity != unameIdentity {
		t.Fatalf("valid physical attestation = %#v/%v", valid, err)
	}
	if _, err := newAttestor(machineID, osRelease, uname, "linux aarch64").Attest(context.Background()); err == nil {
		t.Fatal("physical attestor accepted non-x86_64 Docker architecture")
	}
	if err := os.WriteFile(osRelease, []byte("ID=debian\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newAttestor(machineID, osRelease, uname, "linux x86_64").Attest(context.Background()); err == nil {
		t.Fatal("physical attestor accepted non-Ubuntu os-release")
	}
	if err := os.WriteFile(osRelease, []byte("ID=ubuntu\nNAME=Ubuntu\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(machineID, []byte("fedcba9876543210fedcba9876543210\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newAttestor(machineID, osRelease, uname, "linux x86_64").Attest(context.Background()); err == nil {
		t.Fatal("physical attestor accepted a machine-id whose content hash drifted from the receipt")
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
			strings.Join([]string{"info", "--format", "{{.ID}}"}, "\x00"):                                   linuxDockerServerIDFixture,
			strings.Join([]string{"info", "--format", "{{.NCPU}} {{.MemTotal}}"}, "\x00"):                   "8 17179869184",
			strings.Join([]string{"ps", "--quiet", "--no-trunc"}, "\x00"):                                   "external",
			strings.Join([]string{"inspect", "--format", linuxDockerReservationFormat, "external"}, "\x00"): "6 0 0",
		},
	}, attestor: linuxPhysicalAttestorFixture{attestation: testPhysicalAttestation()}, dockerServerID: linuxDockerServerIDFixture, cgroupRoot: root, bootIDPath: boot}
	if _, err := provider.Collect(context.Background(), config, heteroci.ProbeRequest{OwnerEpoch: 7}); err == nil {
		t.Fatal("Collect() accepted an unbounded external reservation")
	}
	config.Node.Capabilities[0].Runtime = heteroci.RuntimeKind("retired_runtime")
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
	socketPath := filepath.Join(root, "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("Unix socket unavailable: %v", err)
	}
	defer listener.Close()
	config := LinuxFactsProviderConfig{DockerExecutable: docker, DockerHost: "unix://" + socketPath, DockerServerID: linuxDockerServerIDFixture, OSReleasePath: boot, UnameExecutable: docker, MachineIDPath: boot, MachineIDSHA256: "sha256:" + strings.Repeat("1", 64), OSReleaseIdentity: "sha256:" + strings.Repeat("2", 64), UnameIdentity: "sha256:" + strings.Repeat("3", 64), CgroupRoot: root, BootIDPath: boot, Timeout: 0, WaitDelay: 0}
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

func TestLinuxExecDockerRunnerBindsExplicitDockerEnvironment(t *testing.T) {
	root := t.TempDir()
	docker := filepath.Join(root, "docker")
	socketPath := filepath.Join(root, "docker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("Unix socket unavailable: %v", err)
	}
	defer listener.Close()
	if err := os.WriteFile(docker, []byte("#!/bin/sh\nprintf '%s|%s\\n' \"$DOCKER_HOST\" \"${DOCKER_CONTEXT-unset}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "unix:///wrong.sock")
	t.Setenv("DOCKER_CONTEXT", "stale-context")
	runner := linuxExecDockerRunner{executable: docker, dockerHost: "unix://" + socketPath, timeout: time.Second, waitDelay: time.Second}
	output, err := runner.Run(context.Background(), "info", "--format", "{{.ID}}")
	if err != nil {
		t.Fatalf("linuxExecDockerRunner.Run() error = %v", err)
	}
	if want := "unix://" + socketPath + "|unset"; output != want {
		t.Fatalf("Docker environment = %q, want %q", output, want)
	}
}

func writeLinuxFact(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

var _ linuxDockerRunner = linuxDockerRunnerFixture{}
