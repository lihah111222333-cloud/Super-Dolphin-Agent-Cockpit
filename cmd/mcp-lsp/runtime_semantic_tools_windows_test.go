//go:build windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

func TestWindowsProductionAutoInstallerExposesAllSemanticToolFamilies(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", windowsProductionDependencyProfile)
	t.Setenv("SUPER_DOLPHIN_HOME", filepath.Join(t.TempDir(), "product-home"))

	provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}
	list, err := provider.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools() with Windows production auto-installer: %v", err)
	}
	got := make([]string, 0, len(list))
	for _, tool := range list {
		got = append(got, tool.Name)
	}
	want := []string{"file", "inspect", "xref", "grep", "structure", "patch_edit", "completion"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Windows production tools/list names = %#v, want %#v", got, want)
	}
}

func TestWindowsProductionToolsListFailsClosedForInvalidProductRoot(t *testing.T) {
	for _, test := range []struct {
		name string
		root string
	}{
		{name: "relative", root: "relative-product-root"},
		{name: "dot", root: "."},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", windowsProductionDependencyProfile)
			t.Setenv("SUPER_DOLPHIN_HOME", test.root)
			t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", "")
			t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", "")

			provider := registryToolProvider{defs: toolDefinitions(ToolHandlers{})}
			list, err := provider.ListTools(context.Background())
			if err == nil {
				t.Fatalf("ListTools() with invalid product root returned tools=%#v; want fail-fast error", list)
			}
			if list != nil {
				t.Fatalf("ListTools() with invalid product root returned non-nil tools=%#v", list)
			}
		})
	}
}

func TestWindowsAutoInstallerVisibilityRequiresProductionProfile(t *testing.T) {
	for _, profile := range []string{"", "desktop_host", "test"} {
		t.Run(profile, func(t *testing.T) {
			t.Setenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE", profile)
			available, err := runtimePlatformSemanticLSPToolsAvailable(context.Background())
			if err != nil {
				t.Fatalf("runtimePlatformSemanticLSPToolsAvailable(%q): %v", profile, err)
			}
			if available {
				t.Fatalf("runtimePlatformSemanticLSPToolsAvailable(%q) = true, want false", profile)
			}
		})
	}
}

func TestWindowsSemanticInstallerProvisionPredicateRejectsResolverOnly(t *testing.T) {
	resolverOnly := installer.InstallerConfig{
		BinaryName:          "resolver-only.exe",
		AllowInstallCommand: true,
		InstalledBinaryPathResolver: func(context.Context) (string, error) {
			return filepath.Join(os.TempDir(), "resolver-only.exe"), nil
		},
	}
	if windowsSemanticInstallerCanProvision(resolverOnly) {
		t.Fatal("resolver-only Windows installer was treated as auto-provisionable")
	}
}
