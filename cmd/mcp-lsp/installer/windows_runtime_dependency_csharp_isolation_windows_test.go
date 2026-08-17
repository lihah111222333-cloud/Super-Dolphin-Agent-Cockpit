//go:build windows

package installer

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWindowsCsharpInstallUsesOnlyPrivateNuGetSourceAndUserState(t *testing.T) {
	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "dotnet.exe"), []byte("dotnet"), 0o700); err != nil {
		t.Fatalf("write fake dotnet: %v", err)
	}
	sourceDir := filepath.Join(stage, ".runtime-assets", "csharp-ls")
	if err := ensureDirectoryNoSymlink(sourceDir); err != nil {
		t.Fatalf("create private NuGet source: %v", err)
	}
	payload := filepath.Join(sourceDir, "csharp-ls.nupkg")
	if err := os.WriteFile(payload, []byte("locked csharp package"), 0o600); err != nil {
		t.Fatalf("write locked nupkg: %v", err)
	}

	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductDotnetCsharpLS)
	if err != nil {
		t.Fatal(err)
	}
	var gotArgs []string
	var gotEnv []string
	runner := func(_ context.Context, _ string, _ string, args, env []string) error {
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		return nil
	}
	if err := installWindowsRuntimeDependency(
		context.Background(), entry, WindowsHostArchARM64, stage,
		map[string]string{"csharp-ls": payload}, runner,
	); err != nil {
		t.Fatalf("install csharp-ls: %v", err)
	}

	configPath := filepath.Join(stage, ".nuget", "NuGet.Config")
	wantArgs := append([]string(nil), entry.Install.Args...)
	wantArgs = append(wantArgs, "--source", sourceDir, "--configfile", configPath)
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("csharp-ls install args=%#v, want %#v", gotArgs, wantArgs)
	}
	if slices.Contains(gotArgs, "--add-source") {
		t.Fatalf("csharp-ls install retained additive NuGet source: %#v", gotArgs)
	}

	wantEnv := map[string]string{
		"APPDATA":               filepath.Join(stage, ".dotnet-user", "AppData", "Roaming"),
		"LOCALAPPDATA":          filepath.Join(stage, ".dotnet-user", "AppData", "Local"),
		"USERPROFILE":           filepath.Join(stage, ".dotnet-user"),
		"NUGET_CONFIG":          configPath,
		"NUGET_PACKAGES":        filepath.Join(stage, ".nuget-packages"),
		"NUGET_HTTP_CACHE_PATH": filepath.Join(stage, ".nuget-http-cache"),
		"DOTNET_CLI_HOME":       filepath.Join(stage, ".dotnet-home"),
	}
	assertWindowsRuntimeDependencyEnvironment(t, gotEnv, wantEnv)

	for key, value := range map[string]string{
		"APPDATA":               `C:\\outside\\roaming`,
		"LOCALAPPDATA":          `C:\\outside\\local`,
		"USERPROFILE":           `C:\\outside\\profile`,
		"NUGET_CONFIG":          `C:\\outside\\NuGet.Config`,
		"NUGET_PACKAGES":        `C:\\outside\\packages`,
		"NUGET_HTTP_CACHE_PATH": `C:\\outside\\http-cache`,
		"DOTNET_CLI_HOME":       `C:\\outside\\dotnet-home`,
	} {
		t.Setenv(key, value)
	}
	assertWindowsRuntimeDependencyEnvironment(t, runtimeDependencyCommandEnvironment(gotEnv), wantEnv)

	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read private NuGet config: %v", err)
	}
	configText := string(config)
	if !strings.Contains(configText, "<clear") || !strings.Contains(configText, sourceDir) {
		t.Fatalf("private NuGet config does not clear sources and select the locked source: %q", configText)
	}
	if err := requireWindowsCsharpInstallIsolation(stage); err != nil {
		t.Fatalf("private C# isolation does not satisfy the cached-cohort contract: %v", err)
	}
	for _, path := range []string{
		wantEnv["APPDATA"], wantEnv["LOCALAPPDATA"], wantEnv["USERPROFILE"],
		wantEnv["NUGET_PACKAGES"], wantEnv["NUGET_HTTP_CACHE_PATH"], wantEnv["DOTNET_CLI_HOME"],
	} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			t.Fatalf("private .NET user-state directory %q is unavailable: %v", path, err)
		}
	}
}

func assertWindowsRuntimeDependencyEnvironment(t *testing.T, env []string, want map[string]string) {
	t.Helper()
	values := make(map[string]string)
	for _, value := range env {
		key, item, ok := strings.Cut(value, "=")
		if ok {
			values[strings.ToUpper(key)] = item
		}
	}
	for key, expected := range want {
		if values[key] != expected {
			t.Fatalf("environment %s=%q, want %q; environment=%#v", key, values[key], expected, env)
		}
	}
}
