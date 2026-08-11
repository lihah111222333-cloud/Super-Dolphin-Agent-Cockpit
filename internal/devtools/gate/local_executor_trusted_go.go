package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// TrustedGoBinary is the receipt-bound Go executable and its canonical GOROOT.
type TrustedGoBinary struct {
	path    string
	digest  string
	version string
	goRoot  string
}

// VerifiedPath rechecks the full sealed Go proof before every consumer uses it.
func (binary TrustedGoBinary) VerifiedPath() (string, error) {
	if err := binary.validate(); err != nil {
		return "", err
	}
	path, err := binary.verifiedReceiptPath()
	if err != nil {
		return "", err
	}
	if err := binary.verifyRuntime(path); err != nil {
		return "", err
	}
	return path, nil
}

func (binary TrustedGoBinary) validate() error {
	if binary.path == "" || binary.goRoot == "" || !isPrefixedSHA256Digest(binary.digest) || strings.TrimSpace(binary.version) == "" {
		return errors.New("trusted Go binary proof is incomplete")
	}
	return nil
}

func (binary TrustedGoBinary) verifiedReceiptPath() (string, error) {
	path, err := resolveReceiptToolPath(binary.path)
	if err != nil {
		return "", fmt.Errorf("reverify trusted Go binary path: %w", err)
	}
	if path != binary.path {
		return "", errors.New("trusted Go binary path drifted")
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return "", fmt.Errorf("digest trusted Go binary: %w", err)
	}
	if digest != binary.digest {
		return "", errors.New("trusted Go binary content drifted")
	}
	return path, nil
}

func (binary TrustedGoBinary) verifyRuntime(path string) error {
	version, err := probeReceiptToolVersion(context.Background(), "go", path)
	if err != nil {
		return fmt.Errorf("version trusted Go binary: %w", err)
	}
	if version != binary.version {
		return errors.New("trusted Go binary version drifted")
	}
	goRoot, err := resolveLocalToolchainRoot(path)
	if err != nil {
		return fmt.Errorf("resolve trusted Go binary GOROOT: %w", err)
	}
	if goRoot != binary.goRoot {
		return errors.New("trusted Go binary GOROOT drifted")
	}
	return nil
}

// GoRoot returns the receipt-bound canonical GOROOT after revalidating the Go
// binary, version, digest, and root as one proof.
func (binary TrustedGoBinary) GoRoot() (string, error) {
	if _, err := binary.VerifiedPath(); err != nil {
		return "", err
	}
	return binary.goRoot, nil
}

func trustedGoBinaryFromProofs(tools []localExecutorToolProof) (TrustedGoBinary, error) {
	for _, tool := range tools {
		if tool.name == "go" {
			binary := TrustedGoBinary{path: tool.path, digest: tool.digest, version: tool.version, goRoot: tool.goRoot}
			if _, err := binary.VerifiedPath(); err != nil {
				return TrustedGoBinary{}, err
			}
			return binary, nil
		}
	}
	return TrustedGoBinary{}, errors.New("local executor receipt Go tool proof is missing")
}

// ResolveTrustedGoBinary is the sole resolver used while initially sealing a
// receipt. It binds Go to the running gate's canonical GOROOT, never PATH.
func ResolveTrustedGoBinary(ctx context.Context) (TrustedGoBinary, error) {
	path, err := localExecutorTrustedGoBinary()
	if err != nil {
		return TrustedGoBinary{}, err
	}
	proofs, err := localReceiptToolProofs(ctx, map[string]string{"go": path})
	if err != nil {
		return TrustedGoBinary{}, err
	}
	return trustedGoBinaryFromProofs(proofs)
}
