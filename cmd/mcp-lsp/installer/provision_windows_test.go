//go:build windows

package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWindowsLSPCommandMetadataIsClosed(t *testing.T) {
	want := map[WindowsLSPProduct][]string{
		WindowsLSPProductClangd:        nil,
		WindowsLSPProductBuf:           {"lsp", "serve"},
		WindowsLSPProductKotlin:        {"--stdio"},
		WindowsLSPProductDart:          {"language-server", "--protocol=lsp"},
		WindowsLSPProductTerraform:     {"serve"},
		WindowsLSPProductRustAnalyzer:  nil,
		WindowsLSPProductLuaLanguageLS: nil,
	}
	for product, wantArgs := range want {
		args, env, err := windowsLSPCommandMetadata(product)
		if err != nil {
			t.Fatalf("windowsLSPCommandMetadata(%q) error = %v", product, err)
		}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Errorf("windowsLSPCommandMetadata(%q) args = %#v, want %#v", product, args, wantArgs)
		}
		if len(env) != 0 {
			t.Errorf("windowsLSPCommandMetadata(%q) env = %#v, want no PATH-derived environment", product, env)
		}
	}
	if _, _, err := windowsLSPCommandMetadata("unknown"); err == nil {
		t.Fatal("windowsLSPCommandMetadata(unknown) error = nil")
	}
}

func TestResolveProvisionEntrySupportsProductAndLanguageWithoutFallback(t *testing.T) {
	byProduct, err := resolveProvisionEntry(WindowsProvisionRequest{Product: "rust"})
	if err != nil {
		t.Fatalf("resolveProvisionEntry(rust) error = %v", err)
	}
	if byProduct.Product != WindowsLSPProductRustAnalyzer {
		t.Fatalf("resolved rust product = %q, want %q", byProduct.Product, WindowsLSPProductRustAnalyzer)
	}
	byLanguage, err := resolveProvisionEntry(WindowsProvisionRequest{Language: "CPP"})
	if err != nil {
		t.Fatalf("resolveProvisionEntry(CPP) error = %v", err)
	}
	if byLanguage.Product != WindowsLSPProductClangd {
		t.Fatalf("resolved CPP product = %q, want %q", byLanguage.Product, WindowsLSPProductClangd)
	}
	if _, err := resolveProvisionEntry(WindowsProvisionRequest{Product: WindowsLSPProductDart, Language: "rust"}); err == nil {
		t.Fatal("resolveProvisionEntry() accepted a product/language mismatch")
	}
	if _, err := resolveProvisionEntry(WindowsProvisionRequest{}); err == nil {
		t.Fatal("resolveProvisionEntry() accepted an empty request")
	}
}

func TestProvisionForPlatformPreservesTypedArchitectureAndVersionErrors(t *testing.T) {
	cache, err := NewWindowsAssetCache(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	request := WindowsProvisionRequest{Product: WindowsLSPProductClangd, Cache: cache}
	_, err = WindowsProvisionForPlatform(context.Background(), request, WindowsHostPlatform{
		OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX86, ProcessArch: WindowsHostArchX86,
		WindowsVersion: "10.0", WindowsBuild: windowsLSPCatalogMinWindowsBuild,
	})
	var unsupported *WindowsUnsupportedAssetArchitectureError
	if !errors.As(err, &unsupported) || !errors.Is(err, ErrWindowsUnsupportedAssetArchitecture) {
		t.Fatalf("unsupported architecture error = %v, want typed unsupported error", err)
	}
	_, err = WindowsProvisionForPlatform(context.Background(), request, WindowsHostPlatform{
		OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, ProcessArch: WindowsHostArchX64,
		WindowsVersion: "10.0", WindowsBuild: windowsLSPCatalogMinWindowsBuild - 1,
	})
	var oldWindows *WindowsUnsupportedWindowsVersionError
	if !errors.As(err, &oldWindows) || !errors.Is(err, ErrWindowsUnsupportedWindowsVersion) {
		t.Fatalf("old Windows error = %v, want typed version error", err)
	}
}

func TestProvisionForPlatformRequiresExplicitCacheAndWindowsOS(t *testing.T) {
	_, err := WindowsProvisionForPlatform(context.Background(), WindowsProvisionRequest{Product: WindowsLSPProductDart}, WindowsHostPlatform{
		OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64,
		WindowsVersion: "10.0", WindowsBuild: windowsLSPCatalogMinWindowsBuild,
	})
	if err == nil {
		t.Fatal("ProvisionForPlatform() accepted a request without explicit cache")
	}
	cache, err := NewWindowsAssetCache(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	_, err = WindowsProvisionForPlatform(context.Background(), WindowsProvisionRequest{Product: WindowsLSPProductDart, Cache: cache}, WindowsHostPlatform{
		OS: "linux", NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64,
		WindowsVersion: "10.0", WindowsBuild: windowsLSPCatalogMinWindowsBuild,
	})
	if !errors.Is(err, ErrUnsupportedWindowsHostPlatform) {
		t.Fatalf("non-Windows ProvisionForPlatform() error = %v, want ErrUnsupportedWindowsHostPlatform", err)
	}
}

func TestProvisionForPlatformRejectsNonWindowsBeforeCreatingCacheRoot(t *testing.T) {
	cacheRoot := filepath.Join(t.TempDir(), "cache-must-not-exist")
	_, err := WindowsProvisionForPlatform(context.Background(), WindowsProvisionRequest{
		Product:   WindowsLSPProductDart,
		CacheRoot: cacheRoot,
	}, WindowsHostPlatform{
		OS: "linux", NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64,
		WindowsVersion: "10.0", WindowsBuild: windowsLSPCatalogMinWindowsBuild,
	})
	if !errors.Is(err, ErrUnsupportedWindowsHostPlatform) {
		t.Fatalf("non-Windows ProvisionForPlatform() error = %v, want typed unsupported error", err)
	}
	if _, statErr := os.Stat(cacheRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("non-Windows ProvisionForPlatform() cache root stat error = %v, want path to remain absent", statErr)
	}
}

func TestProvisionerRejectsCacheOverride(t *testing.T) {
	cache, err := NewWindowsAssetCache(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewWindowsAssetCache() error = %v", err)
	}
	provisioner, err := NewWindowsProvisioner(cache)
	if err != nil {
		t.Fatalf("NewWindowsProvisioner() error = %v", err)
	}
	_, err = provisioner.Provision(context.Background(), WindowsProvisionRequest{Product: WindowsLSPProductDart, Cache: cache})
	if err == nil {
		t.Fatal("Provisioner.Provision() accepted a cache override")
	}
	if _, err := NewWindowsProvisioner(nil); err == nil {
		t.Fatal("NewWindowsProvisioner(nil) error = nil")
	}
}
