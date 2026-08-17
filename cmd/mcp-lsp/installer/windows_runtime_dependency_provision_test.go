//go:build windows

package installer

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindowsRuntimeDependencyProvisionGoCacheAndNoDownloadCheck(t *testing.T) {
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	root := t.TempDir()
	var fetches atomic.Int32
	var runs atomic.Int32
	fetch := func(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) error {
		fetches.Add(1)
		if err := ctx.Err(); err != nil {
			return err
		}
		entries := map[string]string{}
		switch asset.Component {
		case "go":
			entries["go/bin/go.exe"] = "go-runtime"
		case "gopls":
			entries["gopls@v0.23.0/go.mod"] = "module golang.org/x/tools/gopls\n"
		default:
			entries[asset.ArchivePath] = "asset"
		}
		return writeWindowsRuntimeDependencyTestZip(destination, entries)
	}
	runner := func(ctx context.Context, executable, workingDir string, args, env []string) error {
		runs.Add(1)
		if err := ctx.Err(); err != nil {
			return err
		}
		if executable != filepath.Join(workingDir, "go", "bin", "go.exe") {
			t.Fatalf("Go install executable = %q, want explicit cohort path", executable)
		}
		if strings.Join(args, "\x00") != "install\x00golang.org/x/tools/gopls@v0.23.0" {
			t.Fatalf("Go install args = %#v", args)
		}
		wantGOBIN := "GOBIN=" + filepath.Join(workingDir, "bin")
		found := false
		for _, value := range env {
			if value == wantGOBIN {
				found = true
			}
			if strings.HasPrefix(strings.ToUpper(value), "PATH=") {
				t.Fatalf("runtime install env must not provide PATH fallback: %q", value)
			}
		}
		if !found {
			t.Fatalf("Go install env has no explicit GOBIN: %#v", env)
		}
		if err := os.MkdirAll(filepath.Join(workingDir, "bin"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(workingDir, "bin", "gopls.exe"), []byte("gopls-v0.23.0"), 0o700)
	}
	options := WindowsRuntimeDependencyProvisionOptions{CacheRoot: root, Platform: &platform, FetchAsset: fetch, RunCommand: runner, InstallTimeout: time.Minute}
	result, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), WindowsRuntimeDependencyProductGoGopls, options)
	if err != nil {
		t.Fatal(err)
	}
	if result.CacheHit || result.Architecture != WindowsHostArchARM64 || result.ServerPath != filepath.Join(result.RootPath, "bin", "gopls.exe") || len(result.Env) == 0 {
		t.Fatalf("unexpected Go provision result: %+v", result)
	}
	if fetches.Load() != 2 || runs.Load() != 1 {
		t.Fatalf("first Go install calls = fetch %d/run %d, want fetch 2/run 1", fetches.Load(), runs.Load())
	}

	checked, err := ResolveWindowsRuntimeDependencyForPlatform(context.Background(), WindowsRuntimeDependencyProductGoGopls, root, platform)
	if err != nil {
		t.Fatal(err)
	}
	if !checked.CacheHit || checked.ServerPath != result.ServerPath {
		t.Fatalf("resolved Go result = %+v", checked)
	}
	if fetches.Load() != 2 || runs.Load() != 1 {
		t.Fatalf("read-only resolution performed work: fetch %d/run %d", fetches.Load(), runs.Load())
	}

	second, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), WindowsRuntimeDependencyProductGoGopls, options)
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit || fetches.Load() != 2 || runs.Load() != 1 {
		t.Fatalf("cache hit performed work: result=%+v fetch=%d run=%d", second, fetches.Load(), runs.Load())
	}
	if err := os.WriteFile(result.ServerPath, []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWindowsRuntimeDependencyForPlatform(context.Background(), WindowsRuntimeDependencyProductGoGopls, root, platform); !errors.Is(err, ErrWindowsRuntimeDependencyCacheMiss) {
		t.Fatalf("tampered cache error = %v, want ErrWindowsRuntimeDependencyCacheMiss", err)
	}
	if fetches.Load() != 2 || runs.Load() != 1 {
		t.Fatalf("tampered read-only check performed work: fetch %d/run %d", fetches.Load(), runs.Load())
	}
}

func TestWindowsRuntimeDependencyCheckOnlyHonorsCanceledContextForNonSwift(t *testing.T) {
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ResolveWindowsRuntimeDependencyForPlatform(ctx, WindowsRuntimeDependencyProductGoGopls, filepath.Join(t.TempDir(), "cache"), platform)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("non-Swift check-only canceled context error = %v, want context.Canceled", err)
	}
}

func TestWindowsRuntimeDependencyInstallCancellationDoesNotInvalidateReadyCohort(t *testing.T) {
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	root := t.TempDir()
	var initialFetches atomic.Int32
	var initialRuns atomic.Int32
	fetch := func(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) error {
		initialFetches.Add(1)
		if err := ctx.Err(); err != nil {
			return err
		}
		if asset.Component == "go" {
			return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"go/bin/go.exe": "go-runtime"})
		}
		return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"gopls@v0.23.0/go.mod": "module gopls"})
	}
	runner := func(ctx context.Context, executable, workingDir string, args, env []string) error {
		initialRuns.Add(1)
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(workingDir, "bin"), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(workingDir, "bin", "gopls.exe"), []byte("gopls-v0.23.0"), 0o700); err != nil {
			return err
		}
		probe, err := os.OpenFile(filepath.Join(workingDir, "hash-cancellation-probe.bin"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		chunk := make([]byte, 64<<10)
		for index := 0; index < 2048; index++ {
			if _, err := probe.Write(chunk); err != nil {
				_ = probe.Close()
				return err
			}
		}
		return probe.Close()
	}
	options := WindowsRuntimeDependencyProvisionOptions{CacheRoot: root, Platform: &platform, FetchAsset: fetch, RunCommand: runner, InstallTimeout: time.Minute}
	result, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), WindowsRuntimeDependencyProductGoGopls, options)
	if err != nil {
		t.Fatal(err)
	}
	if initialFetches.Load() != 2 || initialRuns.Load() != 1 {
		t.Fatalf("initial install calls = fetch %d/run %d, want fetch 2/run 1", initialFetches.Load(), initialRuns.Load())
	}
	readyPath := filepath.Join(result.RootPath, runtimeDependencyReadyFile)
	readyBefore, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(10*time.Millisecond, cancel)
	defer cancel()
	var canceledFetches atomic.Int32
	var canceledRuns atomic.Int32
	canceledOptions := options
	canceledOptions.FetchAsset = func(context.Context, WindowsRuntimeDependencyAsset, string) error {
		canceledFetches.Add(1)
		return errors.New("canceled install must not fetch")
	}
	canceledOptions.RunCommand = func(context.Context, string, string, []string, []string) error {
		canceledRuns.Add(1)
		return errors.New("canceled install must not run installer")
	}
	_, err = ProvisionWindowsRuntimeDependencyWithOptions(ctx, WindowsRuntimeDependencyProductGoGopls, canceledOptions)
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled install error = %v, want context cancellation", err)
	}
	if canceledFetches.Load() != 0 || canceledRuns.Load() != 0 {
		t.Fatalf("canceled install performed work: fetch %d/run %d", canceledFetches.Load(), canceledRuns.Load())
	}
	if _, err := os.Stat(result.RootPath); err != nil {
		t.Fatalf("canceled install removed ready cohort: %v", err)
	}
	readyAfter, err := os.ReadFile(readyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(readyAfter) != string(readyBefore) {
		t.Fatal("canceled install changed ready manifest")
	}
	entries, err := os.ReadDir(filepath.Dir(result.RootPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") {
			t.Fatalf("canceled install left staging directory %q", entry.Name())
		}
	}
}

func TestWindowsRuntimeDependencyGoSQLSNativeArchitectureAndLaunchContract(t *testing.T) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductGoSQLS)
	if err != nil {
		t.Fatal(err)
	}
	wantInstallArgs := []string{"build", "-trimpath", "-mod=readonly", "-o", "bin/sqls.exe", "./"}
	cases := []struct {
		name        string
		nativeArch  string
		processArch string
	}{
		{name: "arm64 native x64 process", nativeArch: WindowsHostArchARM64, processArch: WindowsHostArchX64},
		{name: "x64 native arm64 process", nativeArch: WindowsHostArchX64, processArch: WindowsHostArchARM64},
		{name: "x86 native arm64 process", nativeArch: WindowsHostArchX86, processArch: WindowsHostArchARM64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: tc.nativeArch, ProcessArch: tc.processArch, WindowsVersion: "10.0", WindowsBuild: 26100}
			root := filepath.Join(t.TempDir(), "go-sqls", tc.nativeArch)
			result := runtimeDependencyResult(entry, platform, tc.nativeArch, "cohort", root, false)
			if result.Architecture != tc.nativeArch {
				t.Fatalf("result architecture=%q, want NativeArch %q (ProcessArch=%q)", result.Architecture, tc.nativeArch, tc.processArch)
			}
			wantServerPath := filepath.Join(root, "bin", WindowsGoSQLSBinaryName)
			if result.ServerPath != wantServerPath || result.ExecutablePath != wantServerPath || len(result.Args) != 0 {
				t.Fatalf("unexpected Go SQLS launch result: %+v", result)
			}
			if strings.Join(result.InstallArgs, "\x00") != strings.Join(wantInstallArgs, "\x00") {
				t.Fatalf("Go SQLS build args=%#v, want %#v", result.InstallArgs, wantInstallArgs)
			}
			wantAppData := "APPDATA=" + filepath.Join(root, "config")
			if !slices.Contains(result.Env, wantAppData) {
				t.Fatalf("Go SQLS APPDATA=%q missing from native launch env: %#v", wantAppData, result.Env)
			}
			assets, err := WindowsRuntimeDependencyAssetsForArchitecture(WindowsRuntimeDependencyProductGoSQLS, tc.nativeArch)
			if err != nil {
				t.Fatal(err)
			}
			if len(assets) != 2 || assets[0].Architecture != tc.nativeArch || assets[1].Architecture != tc.nativeArch {
				t.Fatalf("Go SQLS assets selected for %q: %#v", tc.nativeArch, assets)
			}
		})
	}

	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, ProcessArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 26100}
	fetch := func(context.Context, WindowsRuntimeDependencyAsset, string) error {
		return errors.New("fetch must not be called for typed catalog verdict")
	}
	for _, product := range []WindowsRuntimeDependencyProduct{WindowsRuntimeDependencyProductSwiftSourceKitLS} {
		gapRoot := filepath.Join(t.TempDir(), "cache")
		var gapFetches atomic.Int32
		_, gapErr := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), product, WindowsRuntimeDependencyProvisionOptions{CacheRoot: gapRoot, Platform: &platform, FetchAsset: func(context.Context, WindowsRuntimeDependencyAsset, string) error {
			gapFetches.Add(1)
			return errors.New("fetch must not be called for typed gap")
		}})
		var typedGap *WindowsRuntimeDependencyEvidenceGapError
		if !errors.As(gapErr, &typedGap) {
			t.Fatalf("%q error = %v, want typed evidence gap", product, gapErr)
		}
		if gapFetches.Load() != 0 {
			t.Fatalf("%q fetched assets despite evidence gap", product)
		}
		if _, statErr := os.Stat(gapRoot); !os.IsNotExist(statErr) {
			t.Fatalf("%q created cache root despite evidence gap: %v", product, statErr)
		}
	}

	unsupportedPlatform := platform
	unsupportedPlatform.NativeArch = WindowsHostArchX86
	_, unsupportedErr := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), WindowsRuntimeDependencyProductJDKJDTLS, WindowsRuntimeDependencyProvisionOptions{CacheRoot: filepath.Join(t.TempDir(), "cache"), Platform: &unsupportedPlatform, FetchAsset: fetch})
	var typedUnsupported *WindowsRuntimeDependencyUnsupportedError
	if !errors.As(unsupportedErr, &typedUnsupported) {
		t.Fatalf("JDTLS x86 error = %v, want typed unsupported", unsupportedErr)
	}
}

func TestWindowsRuntimeDependencyJDTLSLaunchContractUsesAbsolutePaths(t *testing.T) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductJDKJDTLS)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "jdk-jdtls-1.60.0")
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchX64, ProcessArch: WindowsHostArchX64, WindowsVersion: "10.0", WindowsBuild: 26100}
	result := runtimeDependencyResult(entry, platform, WindowsHostArchX64, "cohort", root, false)
	if result.WorkingDirectory != root || result.ServerPath != filepath.Join(root, entry.Install.ServerPath) {
		t.Fatalf("JDTLS launch root/server = %q/%q, want %q/%q", result.WorkingDirectory, result.ServerPath, root, filepath.Join(root, entry.Install.ServerPath))
	}
	for index, argument := range result.Args {
		if argument != "-jar" && argument != "-configuration" {
			continue
		}
		if index+1 >= len(result.Args) || !filepath.IsAbs(result.Args[index+1]) {
			t.Fatalf("JDTLS %s argument is not an absolute path: %#v", argument, result.Args)
		}
		want := result.ServerPath
		if argument == "-configuration" {
			want = filepath.Join(runtimeDependencyJDTLSWorkspaceRoot(root, WindowsHostArchX64, "cohort"), "config_win")
		}
		if result.Args[index+1] != want {
			t.Fatalf("JDTLS %s path = %q, want %q", argument, result.Args[index+1], want)
		}
	}
	dataPath, ok := windowsRuntimeDependencyArgumentValue(result.Args, "-data")
	if !ok || !filepath.IsAbs(dataPath) {
		t.Fatalf("JDTLS -data argument is not absolute: %#v", result.Args)
	}
	jdtlsWorkspaceRoot := runtimeDependencyJDTLSWorkspaceRoot(root, WindowsHostArchX64, "cohort")
	workspaceDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.ToLower("cohort\x00"+filepath.Clean(jdtlsWorkspaceRoot)))))
	wantData := filepath.Join(filepath.Dir(jdtlsWorkspaceRoot), "jdtls-data", workspaceDigest)
	if dataPath != wantData {
		t.Fatalf("JDTLS -data = %q, want mutable workspace path %q", dataPath, wantData)
	}
	if relative, err := filepath.Rel(result.RootPath, dataPath); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("JDTLS -data points inside immutable asset tree: %q", dataPath)
	}
}

func TestWindowsRuntimeDependencyRubyLaunchContractUsesAbsolutePaths(t *testing.T) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductRubySolargraph)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "ruby-solargraph-4.0.5-1")
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	result := runtimeDependencyResult(entry, platform, WindowsHostArchARM64, "cohort", root, false)
	if result.ExecutablePath != filepath.Join(root, "rubyinstaller-4.0.5-1-arm", "bin", "ruby.exe") {
		t.Fatalf("Ruby installer executable = %q, want explicit ruby.exe path", result.ExecutablePath)
	}
	if result.ServerPath != filepath.Join(root, "bin", "solargraph") {
		t.Fatalf("Solargraph server path = %q, want explicit solargraph script path", result.ServerPath)
	}
	if len(result.Args) != 2 || result.Args[0] != result.ServerPath || result.Args[1] != "stdio" {
		t.Fatalf("Solargraph launch args = %#v, want [absolute solargraph script stdio]", result.Args)
	}
	gemRoot := filepath.Join(root, "gems")
	for _, want := range []string{"GEM_HOME=" + gemRoot, "GEM_PATH=" + gemRoot} {
		found := false
		for _, value := range result.Env {
			if value == want {
				found = true
			}
			if strings.HasPrefix(strings.ToUpper(value), "PATH=") {
				t.Fatalf("Ruby launch environment must not provide PATH fallback: %q", value)
			}
		}
		if !found {
			t.Fatalf("Ruby launch environment missing %q: %#v", want, result.Env)
		}
	}
	workspace := runtimeDependencyRubySolargraphWorkspaceRoot(root, WindowsHostArchARM64, "cohort")
	if !filepath.IsAbs(workspace) || workspace == root {
		t.Fatalf("Ruby workspace root = %q, want separate absolute mutable path", workspace)
	}
}

func TestWindowsRuntimeDependencyRubyEvidenceGapDoesNotFetch(t *testing.T) {
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	var fetches atomic.Int32
	_, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), WindowsRuntimeDependencyProductRubySolargraph, WindowsRuntimeDependencyProvisionOptions{
		CacheRoot: cacheRoot,
		Platform:  &platform,
		FetchAsset: func(context.Context, WindowsRuntimeDependencyAsset, string) error {
			fetches.Add(1)
			return errors.New("fetch must not be called for Ruby evidence gap")
		},
	})
	var evidenceGap *WindowsRuntimeDependencyEvidenceGapError
	if !errors.As(err, &evidenceGap) {
		t.Fatalf("Ruby error = %v, want typed evidence gap", err)
	}
	if fetches.Load() != 0 {
		t.Fatalf("Ruby evidence gap fetched %d assets", fetches.Load())
	}
	if _, statErr := os.Stat(cacheRoot); !os.IsNotExist(statErr) {
		t.Fatalf("Ruby evidence gap created cache root: %v", statErr)
	}
}

func TestWindowsRuntimeDependencyProvisionFailureLeavesNoReadyTree(t *testing.T) {
	platform := WindowsHostPlatform{OS: WindowsHostOSWindows, NativeArch: WindowsHostArchARM64, ProcessArch: WindowsHostArchARM64, WindowsVersion: "10.0", WindowsBuild: 26100}
	root := t.TempDir()
	fetch := func(ctx context.Context, asset WindowsRuntimeDependencyAsset, destination string) error {
		if asset.Component == "gopls" {
			return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"gopls@v0.23.0/go.mod": "module gopls"})
		}
		return writeWindowsRuntimeDependencyTestZip(destination, map[string]string{"go/bin/go.exe": "go-runtime"})
	}
	runErr := errors.New("simulated compiler failure")
	_, err := ProvisionWindowsRuntimeDependencyWithOptions(context.Background(), WindowsRuntimeDependencyProductGoGopls, WindowsRuntimeDependencyProvisionOptions{CacheRoot: root, Platform: &platform, FetchAsset: fetch, RunCommand: func(context.Context, string, string, []string, []string) error { return runErr }})
	if !errors.Is(err, runErr) {
		t.Fatalf("failed Go install error = %v, want runner error", err)
	}
	runtimeRoot := filepath.Join(root, "runtime-dependencies", string(WindowsRuntimeDependencyProductGoGopls))
	entries, readErr := os.ReadDir(runtimeRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") {
			t.Fatalf("failed install left staging directory %q", entry.Name())
		}
	}
	entry, entryErr := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductGoGopls)
	if entryErr != nil {
		t.Fatal(entryErr)
	}
	cohortRoot := filepath.Join(runtimeRoot, WindowsHostArchARM64, runtimeDependencyCohort(entry, WindowsHostArchARM64))
	if _, statErr := os.Stat(cohortRoot); !os.IsNotExist(statErr) {
		t.Fatalf("failed install left a ready cohort tree: %v", statErr)
	}
}

func windowsRuntimeDependencyArgumentValue(args []string, name string) (string, bool) {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == name {
			return args[index+1], true
		}
	}
	return "", false
}

func writeWindowsRuntimeDependencyTestZip(destination string, entries map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	file, err := os.Create(destination)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, createErr := writer.Create(name)
		if createErr != nil {
			_ = file.Close()
			return createErr
		}
		if _, writeErr := entry.Write([]byte(content)); writeErr != nil {
			_ = file.Close()
			return writeErr
		}
	}
	if err := writer.Close(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func TestWindowsRuntimeDependencyCommandFailureSummaryRedactsOutputArgsEnvAndPaths(t *testing.T) {
	secretOutput := "secret-runtime-output-token"
	secretArg := "--secret-arg=user-private-token"
	secretEnv := "SECRET_ENV=user-private-env"
	workingDir := filepath.Join(t.TempDir(), "user-private", "working")
	if err := os.MkdirAll(workingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(workingDir, "runtime.cmd")
	if err := os.WriteFile(executable, []byte("@echo off\r\necho "+secretOutput+"\r\nexit /b 23\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := defaultWindowsRuntimeDependencyCommandRunner(context.Background(), executable, workingDir, []string{secretArg}, []string{secretEnv})
	if err == nil {
		t.Fatal("runtime dependency command returned nil error")
	}
	var summary *ProcessFailureError
	if !errors.As(err, &summary) {
		t.Fatalf("error = %T, want *ProcessFailureError: %v", err, err)
	}
	if !summary.ExitCodePresent || summary.ExitCode != 23 || summary.ArgsCount != 1 || summary.OutputBytes == 0 || summary.OutputSHA256 == "" {
		t.Fatalf("runtime process summary = %+v", summary)
	}
	if got := err.Error(); strings.Contains(got, secretOutput) || strings.Contains(got, secretArg) || strings.Contains(got, secretEnv) || strings.Contains(got, executable) || strings.Contains(got, workingDir) {
		t.Fatalf("runtime command failure leaked process data: %q", got)
	}
	receipt, marshalErr := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if got := string(receipt); strings.Contains(got, secretOutput) || strings.Contains(got, secretArg) || strings.Contains(got, secretEnv) || strings.Contains(got, workingDir) {
		t.Fatalf("runtime receipt leaked process data: %q", got)
	}
}

func TestWindowsRuntimeDependencyInjectedRunnerFailureIsSummarized(t *testing.T) {
	entry, err := WindowsRuntimeDependencyCatalogEntryForProduct(WindowsRuntimeDependencyProductGoGopls)
	if err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	architecture := WindowsHostArchARM64
	runtimePath := filepath.Join(stage, filepath.FromSlash(runtimeDependencyRuntimeExecutablePath(entry, architecture)))
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := "secret-injected-runner-error"
	installErr := installWindowsRuntimeDependency(context.Background(), entry, architecture, stage, nil, func(context.Context, string, string, []string, []string) error {
		return errors.New(secret)
	})
	if installErr == nil {
		t.Fatal("injected runner returned nil error")
	}
	var summary *ProcessFailureError
	if !errors.As(installErr, &summary) {
		t.Fatalf("error = %T, want *ProcessFailureError: %v", installErr, installErr)
	}
	if strings.Contains(installErr.Error(), secret) || strings.Contains(installErr.Error(), stage) {
		t.Fatalf("injected runner error leaked raw data: %q", installErr)
	}
}
