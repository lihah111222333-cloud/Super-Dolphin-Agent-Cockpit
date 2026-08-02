package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestVerifyRemoteBuilderToolchainBindsCandidateLockAndPortableGo(t *testing.T) {
	t.Parallel()
	sourceRoot := t.TempDir()
	lockPath := filepath.Join(sourceRoot, "build", "gate", "toolchain.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	lock := []byte("candidate toolchain lock\n")
	if err := os.WriteFile(lockPath, lock, 0o600); err != nil {
		t.Fatal(err)
	}
	goRoot := "/opt/super-dolphin-gate/runtime/go"
	expected := fmt.Sprintf("sha256:%x", sha256.Sum256(lock))

	for _, test := range []struct {
		name     string
		digest   string
		version  string
		identity string
		wantErr  string
	}{
		{name: "exact identity", digest: expected, version: "go version " + gatecontract.RequiredGoToolchain + " linux/amd64\n", identity: goRoot + "\n" + filepath.Join(goRoot, "pkg", "tool", "linux_amd64") + "\n"},
		{name: "candidate lock drift", digest: "sha256:" + strings.Repeat("0", 64), wantErr: "toolchain lock identity drift"},
		{name: "Go version drift", digest: expected, version: "go version go1.25.6 linux/amd64\n", wantErr: "Go version drift"},
		{name: "GOROOT drift", digest: expected, version: "go version " + gatecontract.RequiredGoToolchain + " linux/amd64\n", identity: goRoot + "\n" + filepath.Join(goRoot, "pkg", "tool", "linux_arm64") + "\n", wantErr: "GOROOT identity drift"},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := func(_ context.Context, binary string, args, environment []string) ([]byte, error) {
				if binary != filepath.Join(goRoot, "bin", "go") || !containsExact(environment, "GOROOT="+goRoot) || !containsExact(environment, "GOTOOLCHAIN=local") {
					return nil, fmt.Errorf("unexpected Go invocation")
				}
				switch strings.Join(args, " ") {
				case "version":
					return []byte(test.version), nil
				case "env GOROOT GOTOOLDIR":
					return []byte(test.identity), nil
				default:
					return nil, fmt.Errorf("unexpected Go arguments %q", args)
				}
			}
			err := verifyRemoteBuilderToolchain(context.Background(), filepath.Join(goRoot, "bin", "go"), goRoot, sourceRoot, test.digest, run)
			if test.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("verify error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

func containsExact(values []string, want string) bool {
	return slices.Contains(values, want)
}
