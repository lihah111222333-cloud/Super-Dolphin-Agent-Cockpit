package heterocinode

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const linuxMachineFlag = "-m"

type linuxPhysicalAttestor interface {
	Attest(context.Context) (heteroci.PhysicalHostAttestation, error)
}

type linuxExecPhysicalAttestor struct {
	dockerRunner      linuxDockerRunner
	machineIDPath     string
	machineIDSHA256   string
	osReleasePath     string
	osReleaseIdentity string
	unameExecutable   string
	unameIdentity     string
}

func (attestor linuxExecPhysicalAttestor) Attest(ctx context.Context) (heteroci.PhysicalHostAttestation, error) {
	if attestor.dockerRunner == nil {
		return heteroci.PhysicalHostAttestation{}, errors.New("physical attestor Docker runner is required")
	}
	machineID, machineIDIdentity, err := readMachineID(attestor.machineIDPath)
	if err != nil {
		return heteroci.PhysicalHostAttestation{}, err
	}
	if machineID == "" || machineIDIdentity != attestor.machineIDSHA256 {
		return heteroci.PhysicalHostAttestation{}, errors.New("machine-id content or identity does not match caller receipt")
	}
	osID, osReleaseIdentity, err := readUbuntuOSRelease(attestor.osReleasePath)
	if err != nil {
		return heteroci.PhysicalHostAttestation{}, err
	}
	if osReleaseIdentity != attestor.osReleaseIdentity {
		return heteroci.PhysicalHostAttestation{}, errors.New("os-release identity does not match caller receipt")
	}
	machine, unameIdentity, err := readLinuxMachine(ctx, attestor.unameExecutable)
	if err != nil {
		return heteroci.PhysicalHostAttestation{}, err
	}
	if unameIdentity != attestor.unameIdentity {
		return heteroci.PhysicalHostAttestation{}, errors.New("uname executable identity does not match caller receipt")
	}
	dockerPlatform, err := attestor.dockerRunner.Run(ctx, "info", "--format", "{{.OSType}} {{.Architecture}}")
	if err != nil || dockerPlatform != "linux x86_64" {
		return heteroci.PhysicalHostAttestation{}, errors.New("Docker physical platform is not exact linux/x86_64")
	}
	attestation := heteroci.PhysicalHostAttestation{
		SchemaVersion:      heteroci.PhysicalHostAttestationSchemaVersion,
		OSID:               osID,
		Machine:            machine,
		MachineIDSHA256:    machineIDIdentity,
		OSReleaseIdentity:  osReleaseIdentity,
		UnameIdentity:      unameIdentity,
		DockerOSType:       "linux",
		DockerArchitecture: "x86_64",
	}
	attestation.Digest = attestation.CanonicalDigest()
	if err := attestation.Validate(); err != nil {
		return heteroci.PhysicalHostAttestation{}, err
	}
	return attestation, nil
}

func readUbuntuOSRelease(path string) (string, string, error) {
	data, identity, err := readStableRegularFile(path, 16<<10)
	if err != nil {
		return "", "", err
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || strings.TrimSpace(key) != key || strings.ContainsAny(key, " \t\r\n") {
			return "", "", errors.New("os-release contains an invalid field")
		}
		if _, duplicate := values[key]; duplicate {
			return "", "", errors.New("os-release contains a duplicate field")
		}
		parsed, err := parseOSReleaseValue(value)
		if err != nil {
			return "", "", err
		}
		values[key] = parsed
	}
	if err := scanner.Err(); err != nil {
		return "", "", errors.New("os-release scanner failed")
	}
	if values["ID"] != "ubuntu" {
		return "", "", errors.New("os-release ID is not exact Ubuntu")
	}
	return values["ID"], identity, nil
}

func parseOSReleaseValue(value string) (string, error) {
	if strings.HasPrefix(value, "\"") {
		parsed, err := strconv.Unquote(value)
		if err != nil || parsed == "" {
			return "", errors.New("os-release quoted value is invalid")
		}
		return parsed, nil
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("os-release value is invalid")
	}
	return value, nil
}

func readMachineID(path string) (string, string, error) {
	data, identity, err := readStableRegularFile(path, 256)
	if err != nil {
		return "", "", err
	}
	value := strings.TrimSuffix(string(data), "\n")
	if value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n\t") || len(value) != 32 || !isLowerHex(value) {
		return "", "", errors.New("machine-id must be exactly 32 lowercase hexadecimal characters")
	}
	return value, identity, nil
}

func readLinuxMachine(ctx context.Context, executable string) (string, string, error) {
	_, identityBefore, err := readStableRegularFile(executable, 32<<20)
	if err != nil {
		return "", "", errors.New("uname executable identity cannot be established")
	}
	command := exec.CommandContext(ctx, executable, linuxMachineFlag)
	output, err := command.Output()
	if err != nil || strings.TrimSpace(string(output)) != "x86_64" {
		return "", "", errors.New("uname machine is not exact x86_64")
	}
	_, identityAfter, err := readStableRegularFile(executable, 32<<20)
	if err != nil || identityBefore != identityAfter {
		return "", "", errors.New("uname executable changed during attestation")
	}
	return "x86_64", identityAfter, nil
}

func readStableRegularFile(path string, maxBytes int64) ([]byte, string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, "", errors.New("attestation file path is invalid")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, "", errors.New("attestation file path contains a symlink")
	}
	initial, err := os.Lstat(path)
	if err != nil || !initial.Mode().IsRegular() || initial.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("attestation file must be a regular non-symlink file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", errors.New("attestation file cannot be opened")
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(initial, opened) || opened.Size() != initial.Size() {
		_ = file.Close()
		return nil, "", errors.New("attestation file changed before hashing")
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || int64(len(data)) > maxBytes {
		return nil, "", errors.New("attestation file cannot be read within bounds")
	}
	final, err := os.Lstat(path)
	if err != nil || !final.Mode().IsRegular() || final.Mode()&os.ModeSymlink != 0 || !os.SameFile(initial, final) || final.Size() != initial.Size() || int64(len(data)) != final.Size() {
		return nil, "", errors.New("attestation file changed while hashing")
	}
	digest := sha256.Sum256(data)
	return data, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if !(character >= '0' && character <= '9' || character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}

// ValidateSHA256Identity validates caller-supplied file identities used by the physical host receipt.
func ValidateSHA256Identity(value string) error {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") || !isLowerHex(value[len("sha256:"):]) {
		return errors.New("identity must be canonical sha256:<64 lowercase hex>")
	}
	return nil
}
