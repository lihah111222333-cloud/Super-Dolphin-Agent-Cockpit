//go:build windows

package installer

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestWindowsSqruffCargoInstallArgsArePinnedToProductRoot(t *testing.T) {
	cargoHome := filepath.Join(t.TempDir(), "cache", "rust-toolchain", "1.96.0", WindowsHostArchARM64, "cargo-home")
	target := "aarch64-pc-windows-msvc"
	got := windowsSqruffCargoInstallArgs(cargoHome, target)
	want := []string{"install", "sqruff", "--version", WindowsSqruffVersion, "--locked", "--root", cargoHome, "--target", target}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows sqruff Cargo args = %#v, want %#v", got, want)
	}
}

func TestWindowsSqruffCargoInstallArgsFollowNativeArchitecture(t *testing.T) {
	wantTargets := map[string]string{
		WindowsHostArchARM64: "aarch64-pc-windows-msvc",
		WindowsHostArchX64:   "x86_64-pc-windows-msvc",
		WindowsHostArchX86:   "i686-pc-windows-msvc",
	}
	for architecture, wantTarget := range wantTargets {
		architecture, wantTarget := architecture, wantTarget
		t.Run(architecture, func(t *testing.T) {
			target, err := windowsRustTargetTriple(architecture)
			if err != nil {
				t.Fatalf("windowsRustTargetTriple(%q): %v", architecture, err)
			}
			if target != wantTarget {
				t.Fatalf("windowsRustTargetTriple(%q) = %q, want %q", architecture, target, wantTarget)
			}
			args := windowsSqruffCargoInstallArgs(filepath.Join(t.TempDir(), "cargo-home"), target)
			if args[len(args)-2] != "--target" || args[len(args)-1] != wantTarget {
				t.Fatalf("Windows sqruff Cargo args for %q = %#v, want trailing target %q", architecture, args, wantTarget)
			}
		})
	}
}

func TestWindowsSqruffBinaryPathUsesProductCargoHome(t *testing.T) {
	rust := WindowsRustToolchainPaths{
		CargoHome: filepath.Join(t.TempDir(), "cache", "rust-toolchain", "1.96.0", WindowsHostArchARM64, "cargo-home"),
	}
	got := windowsSqruffBinaryPath(rust)
	want := filepath.Join(rust.CargoHome, "bin", "sqruff.exe")
	if got != want {
		t.Fatalf("Windows sqruff binary path = %q, want %q", got, want)
	}
}
