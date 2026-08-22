//go:build windows && arm64 && e2e

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	emmyLuaWindowsARM64E2EEnv           = "SUPER_DOLPHIN_RUN_EMMYLUA_WINDOWS_ARM64_E2E"
	emmyLuaWindowsARM64PrecheckEnv      = "SUPER_DOLPHIN_EMMYLUA_WINDOWS_ARM64_E2E_PRECHECK"
	emmyLuaWindowsARM64EvidenceDirEnv   = "SUPER_DOLPHIN_EMMYLUA_WINDOWS_ARM64_E2E_EVIDENCE_DIR"
	emmyLuaWindowsARM64ProductRootEnv   = "SUPER_DOLPHIN_EMMYLUA_WINDOWS_ARM64_PRODUCT_ROOT"
	emmyLuaWindowsARM64ArchiveURL       = "https://github.com/EmmyLuaLs/emmylua-analyzer-rust/releases/download/0.25.1/emmylua_ls-win32-arm64.zip"
	emmyLuaWindowsARM64ArchiveSHA256    = "f6f335f01fccca6f000a6240fb78c6fbab069230b1bb4347361ef3f64550390a"
	emmyLuaWindowsARM64ExecutableSHA256 = "c05a85e354de013e0300c42197592355d425a8ef7fae7ef1eb3febd68c1791ac"
	emmyLuaWindowsARM64ProofIdle        = 15 * time.Minute
	emmyLuaWindowsARM64ManagerIdle      = 17 * time.Minute
	emmyLuaWindowsARM64PrecheckIdle     = 30 * time.Second
	emmyLuaWindowsARM64LockedGoVersion  = "go1.26.5"
)

var emmyLuaWindowsARM64AbsolutePath = regexp.MustCompile(`(?i)(?:[a-z]:[\\/]|\\\\)[^\"'\r\n,]+`)

// emmyLuaHTTPCounts 只保存 HTTP 生命周期计数，不保存 URL、请求头、token 或本机路径。
type emmyLuaHTTPCounts struct {
	Requests            int64 `json:"requests"`
	Attempts            int64 `json:"attempts"`
	Responses           int64 `json:"responses"`
	TransportErrors     int64 `json:"transport_errors"`
	RedirectResponses   int64 `json:"redirect_responses"`
	SuccessfulResponses int64 `json:"successful_responses"`
	FailedResponses     int64 `json:"failed_responses"`
}

// emmyLuaHTTPObserver 在 production install 阶段包裹默认 transport；所有计数使用原子变量，
// RoundTripper 不读取请求 URL 或内容，避免把供应链敏感信息写入 receipt。
type emmyLuaHTTPObserver struct {
	base                http.RoundTripper
	requests            atomic.Int64
	attempts            atomic.Int64
	responses           atomic.Int64
	transportErrors     atomic.Int64
	redirectResponses   atomic.Int64
	successfulResponses atomic.Int64
	failedResponses     atomic.Int64
}

func (o *emmyLuaHTTPObserver) RoundTrip(request *http.Request) (*http.Response, error) {
	if o == nil || o.base == nil {
		return nil, errors.New("EmmyLua HTTP observer transport is unavailable")
	}
	o.requests.Add(1)
	o.attempts.Add(1)
	response, err := o.base.RoundTrip(request)
	if err != nil {
		o.transportErrors.Add(1)
		return nil, err
	}
	o.responses.Add(1)
	if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < 400 {
		o.redirectResponses.Add(1)
	} else if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		o.successfulResponses.Add(1)
	} else {
		o.failedResponses.Add(1)
	}
	return response, nil
}

func (o *emmyLuaHTTPObserver) Snapshot() emmyLuaHTTPCounts {
	if o == nil {
		return emmyLuaHTTPCounts{}
	}
	return emmyLuaHTTPCounts{
		Requests:            o.requests.Load(),
		Attempts:            o.attempts.Load(),
		Responses:           o.responses.Load(),
		TransportErrors:     o.transportErrors.Load(),
		RedirectResponses:   o.redirectResponses.Load(),
		SuccessfulResponses: o.successfulResponses.Load(),
		FailedResponses:     o.failedResponses.Load(),
	}
}

// emmyLuaCacheSnapshot 只记录产品缓存的存在性、条目数和 ready/payload 是否存在，禁止写入路径。
type emmyLuaCacheSnapshot struct {
	RootExists             bool `json:"root_exists"`
	RootEntries            int  `json:"root_entries"`
	LSPCacheExists         bool `json:"lsp_cache_exists"`
	LSPCacheEntries        int  `json:"lsp_cache_entries"`
	PayloadPresent         bool `json:"payload_present"`
	ReadyExecutablePresent bool `json:"ready_executable_present"`
}

func snapshotEmmyLuaCache(t *testing.T, productRoot string, manifest installer.WindowsLockedAssetManifest, asset installer.WindowsLockedAsset) emmyLuaCacheSnapshot {
	t.Helper()
	rootExists, rootEntries := emmyLuaDirectoryState(t, productRoot)
	lspRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	lspCacheExists, lspCacheEntries := emmyLuaDirectoryState(t, lspRoot)
	assetRoot := filepath.Join(lspRoot, manifest.Name, asset.Version, asset.Architecture, strings.ToLower(asset.SHA256))
	payloadPresent := emmyLuaPathExists(t, filepath.Join(assetRoot, "payload.zip"))
	readyExecutablePresent := emmyLuaPathExists(t, filepath.Join(assetRoot, "ready", filepath.FromSlash(asset.BinaryPath)))
	return emmyLuaCacheSnapshot{
		RootExists:             rootExists,
		RootEntries:            rootEntries,
		LSPCacheExists:         lspCacheExists,
		LSPCacheEntries:        lspCacheEntries,
		PayloadPresent:         payloadPresent,
		ReadyExecutablePresent: readyExecutablePresent,
	}
}

func emmyLuaDirectoryState(t *testing.T, path string) (bool, int) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, 0
	}
	if err != nil {
		t.Fatalf("inspect EmmyLua cache directory: %s", redactEmmyLuaWindowsARM64Text(err.Error(), path))
	}
	return true, len(entries)
}

func emmyLuaPathExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatalf("inspect EmmyLua cache entry: %s", redactEmmyLuaWindowsARM64Text(err.Error(), path))
	}
	return true
}

// emmyLuaWindowsARM64ProductRoot 解析受控的产品根；未提供时创建一次性私有根，
// 提供时只复用现有绝对目录，便于短 precheck 复用已安装的 EmmyLua 缓存。
func emmyLuaWindowsARM64ProductRoot(t *testing.T) (string, bool, error) {
	t.Helper()
	configured := strings.TrimSpace(os.Getenv(emmyLuaWindowsARM64ProductRootEnv))
	if configured == "" {
		root, err := os.MkdirTemp("", "sd-emmylua-production-windows-arm64-")
		if err != nil {
			return "", false, fmt.Errorf("create private EmmyLua product root: %w", err)
		}
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(root); err != nil {
				t.Errorf("remove EmmyLua Windows ARM64 product root: %v", err)
			}
		})
		return root, false, nil
	}
	root := filepath.Clean(configured)
	if !filepath.IsAbs(root) {
		return "", true, fmt.Errorf("%s must be an absolute Windows product root: %q", emmyLuaWindowsARM64ProductRootEnv, configured)
	}
	if !strings.HasPrefix(strings.ToLower(filepath.Base(root)), "sd-emmylua-production-windows-arm64-") {
		return "", true, fmt.Errorf("%s must identify an approved EmmyLua product root: %q", emmyLuaWindowsARM64ProductRootEnv, filepath.Base(root))
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", true, fmt.Errorf("stat reusable EmmyLua product root %q: %w", root, err)
	}
	if !info.IsDir() {
		return "", true, fmt.Errorf("reusable EmmyLua product root %q is not a directory", root)
	}
	return root, true, nil
}

// TestEmmyLuaWindowsARM64ProductRootContract 锁定 fresh install 与既有缓存复用的入口，
// 并确保路径不满足产品根约束时立即失败。
func TestEmmyLuaWindowsARM64ProductRootContract(t *testing.T) {
	t.Run("unset creates private root", func(t *testing.T) {
		t.Setenv(emmyLuaWindowsARM64ProductRootEnv, "")
		root, reused, err := emmyLuaWindowsARM64ProductRoot(t)
		if err != nil {
			t.Fatalf("emmyLuaWindowsARM64ProductRoot() error = %v", err)
		}
		if reused || !strings.HasPrefix(strings.ToLower(filepath.Base(root)), "sd-emmylua-production-windows-arm64-") {
			t.Fatalf("emmyLuaWindowsARM64ProductRoot() = (%q, %t), want approved fresh root", root, reused)
		}
	})
	t.Run("approved existing root is reused", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "sd-emmylua-production-windows-arm64-reuse")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatalf("create approved product root: %v", err)
		}
		t.Setenv(emmyLuaWindowsARM64ProductRootEnv, root)
		got, reused, err := emmyLuaWindowsARM64ProductRoot(t)
		if err != nil || filepath.Clean(got) != filepath.Clean(root) || !reused {
			t.Fatalf("emmyLuaWindowsARM64ProductRoot() = (%q, %t, %v), want (%q, true, nil)", got, reused, err, root)
		}
	})
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "relative root", value: "relative-product-root"},
		{name: "unapproved root", value: filepath.Join(t.TempDir(), "other-product-root")},
		{name: "missing approved root", value: filepath.Join(t.TempDir(), "sd-emmylua-production-windows-arm64-missing")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(emmyLuaWindowsARM64ProductRootEnv, test.value)
			if _, _, err := emmyLuaWindowsARM64ProductRoot(t); err == nil {
				t.Fatalf("emmyLuaWindowsARM64ProductRoot(%q) unexpectedly succeeded", test.value)
			}
		})
	}
}

func emmyLuaInstallObservationPass(before, after emmyLuaCacheSnapshot, counts emmyLuaHTTPCounts) bool {
	if before.PayloadPresent && before.ReadyExecutablePresent {
		return after.PayloadPresent && after.ReadyExecutablePresent && counts.Requests == 0 && counts.Attempts == 0 && counts.Responses == 0 && counts.TransportErrors == 0
	}
	if before.PayloadPresent {
		return after.PayloadPresent && after.ReadyExecutablePresent && counts.TransportErrors == 0 &&
			((counts.Requests == 0 && counts.Attempts == 0 && counts.Responses == 0) ||
				(counts.Requests > 0 && counts.Attempts > 0 && counts.Responses > 0))
	}
	return !before.PayloadPresent && counts.Requests > 0 && counts.Attempts > 0 && counts.Responses > 0 && counts.TransportErrors == 0 && after.PayloadPresent && after.ReadyExecutablePresent
}

// TestWindowsARM64EmmyLuaProductionE2E 是 Windows ARM64 EmmyLua 的真实生产链路门禁。
//
// 该测试同时验证 production EnsureInstalled/Resolver、空私有 product cache 的真实
// 下载、固定资产身份和真实 mcp-lsp stdio 工具调用。它默认不编译进普通测试，且即使
// 显式加 e2e tag 也只有在 SUPER_DOLPHIN_RUN_EMMYLUA_WINDOWS_ARM64_E2E=1 时联网。
// action ledger 明确把 callable、unsupported、empty 和 null 分开；后 3 类永远不是
// 语义 PASS，供已知上游缺口和新回归分别审计。
func TestWindowsARM64EmmyLuaProductionE2E(t *testing.T) {
	if os.Getenv(emmyLuaWindowsARM64E2EEnv) != "1" {
		t.Skipf("set %s=1 to enable the networked Windows ARM64 EmmyLua E2E", emmyLuaWindowsARM64E2EEnv)
	}
	precheck := os.Getenv(emmyLuaWindowsARM64PrecheckEnv) == "1"
	if testing.Short() && !precheck {
		t.Skip("networked EmmyLua E2E is disabled by -short; set the explicit PRECHECK env for a non-PASS short precheck")
	}
	if runtime.GOOS != "windows" {
		t.Fatalf("Windows ARM64 EmmyLua E2E requires Windows, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	// Receipt 目录必须在测试启动时固定；不能依赖 stdout session 或测试进程
	// 最后还能否回传日志。显式目录仅由调用方提供，未提供时使用仓库受控证据目录。
	emmyLuaEvidenceDir(t)
	lifecycleStartedAt := time.Now().UTC()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	if precheck {
		t.Logf("NON_PASS EmmyLua lifecycle phase=PRECHECK_ONLY idle=%s; this run cannot establish the formal 15-minute lifecycle PASS", emmyLuaWindowsARM64PrecheckIdle)
	} else {
		t.Logf("EmmyLua lifecycle phase=FORMAL manager_idle_timeout=%s proof_idle_duration=%s headroom=process_identity_sampling_only", emmyLuaWindowsARM64ManagerIdle, emmyLuaWindowsARM64ProofIdle)
	}

	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform: %v", err)
	}
	if host.OS != installer.WindowsHostOSWindows || host.NativeArch != installer.WindowsHostArchARM64 || host.ProcessArch != installer.WindowsHostArchARM64 {
		t.Fatalf("EmmyLua Windows ARM64/process ARM64 E2E requires native and process ARM64, got os=%q native=%q process=%q", host.OS, host.NativeArch, host.ProcessArch)
	}
	t.Logf("Windows host os=%s native=%s process=%s build=%d; ProcessArch is diagnostic only", host.OS, host.NativeArch, host.ProcessArch, host.WindowsBuild)

	manifest, asset := requireEmmyLuaWindowsARM64LockedFacts(t, host)
	if got := installer.WindowsEmmyLuaCommandArguments(); !slicesEqual(got, []string{"--communication", "stdio", "--log-level", "error", "--resources-path", "none"}) {
		t.Fatalf("EmmyLua command args=%v", got)
	}

	productRoot, reusedProductRoot, err := emmyLuaWindowsARM64ProductRoot(t)
	if err != nil {
		t.Fatalf("resolve EmmyLua Windows ARM64 product root: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict private product root: %v", err)
	}
	cacheBefore := snapshotEmmyLuaCache(t, productRoot, manifest, asset)
	cacheAfter := emmyLuaCacheSnapshot{}
	installHTTP := emmyLuaHTTPCounts{}
	installObservationPass := false
	cacheReadyBefore := cacheBefore.PayloadPresent && cacheBefore.ReadyExecutablePresent
	cacheDecision := "fresh_empty_root_install"
	if reusedProductRoot {
		cacheDecision = "existing_product_root_auto_install"
		if cacheBefore.PayloadPresent {
			cacheDecision = "reused_payload_cache_repair"
		}
		if cacheReadyBefore {
			cacheDecision = "reused_ready_product_cache"
		}
	}
	t.Logf("EmmyLua product root mode=%s cache_ready_before=%t", cacheDecision, cacheReadyBefore)
	// The child mcp-lsp uses the same production resolver. No PATH or repository cache is
	// allowed to satisfy this run.
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)

	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	provider := installer.NewProvider()
	cfg := windowsLuaInstallerConfig(productRoot, nil)
	if cfg.BinaryName != installer.WindowsEmmyLuaBinaryName || cfg.InstallAction == nil || cfg.InstalledBinaryPathResolver == nil {
		t.Fatalf("production Lua config is not the independent EmmyLua resolver: binary=%q action=%t resolver=%t", cfg.BinaryName, cfg.InstallAction != nil, cfg.InstalledBinaryPathResolver != nil)
	}
	provider.Register("lua", cfg)
	httpObserver := &emmyLuaHTTPObserver{base: http.DefaultTransport}
	if httpObserver.base == nil {
		t.Fatalf("EmmyLua HTTP observation requires the default transport")
	}
	previousHTTPTransport := http.DefaultTransport
	http.DefaultTransport = httpObserver
	var installed installer.InstallResult
	func() {
		defer func() { http.DefaultTransport = previousHTTPTransport }()
		installed, err = provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), "lua")
	}()
	installHTTP = httpObserver.Snapshot()
	cacheAfter = snapshotEmmyLuaCache(t, productRoot, manifest, asset)
	installObservationPass = emmyLuaInstallObservationPass(cacheBefore, cacheAfter, installHTTP)
	if cacheReadyBefore {
		installObservationPass = installObservationPass && installed.Status == installer.InstallStatusPathFound && installHTTP.Requests == 0 && installHTTP.Attempts == 0 && installHTTP.Responses == 0
	} else if cacheBefore.PayloadPresent {
		installObservationPass = installObservationPass && installed.Status == installer.InstallStatusInstalledPath
	} else {
		installObservationPass = installObservationPass && installed.Status == installer.InstallStatusInstalledPath && installHTTP.Requests > 0 && installHTTP.Attempts > 0 && installHTTP.Responses > 0
	}
	t.Logf("EmmyLua install observation decision=%s cache_ready_before=%t cache_after_payload=%t cache_after_ready=%t http_requests=%d http_attempts=%d http_responses=%d http_transport_errors=%d http_redirect_responses=%d contract_pass=%t", cacheDecision, cacheReadyBefore, cacheAfter.PayloadPresent, cacheAfter.ReadyExecutablePresent, installHTTP.Requests, installHTTP.Attempts, installHTTP.Responses, installHTTP.TransportErrors, installHTTP.RedirectResponses, installObservationPass)
	if !installObservationPass {
		t.Fatalf("EmmyLua install/cache-reuse observation contract failed: decision=%s cache_ready_before=%t cache_after_payload=%t cache_after_ready=%t status=%s http_requests=%d http_attempts=%d http_responses=%d http_transport_errors=%d", cacheDecision, cacheReadyBefore, cacheAfter.PayloadPresent, cacheAfter.ReadyExecutablePresent, installed.Status, installHTTP.Requests, installHTTP.Attempts, installHTTP.Responses, installHTTP.TransportErrors)
	}
	if err != nil {
		t.Fatalf("production EnsureInstalledDetailed(lua) from empty private cache: %v", err)
	}
	wantStatus := installer.InstallStatusInstalledPath
	if cacheReadyBefore {
		wantStatus = installer.InstallStatusPathFound
	}
	if installed.Status != wantStatus || filepath.Clean(installed.Path) != filepath.Clean(mustResolveEmmyLuaPath(t, productRoot)) {
		t.Fatalf("production install/cache result status=%s binary=%s path_base=%s, want status=%s and resolver path", installed.Status, installed.Binary, filepath.Base(installed.Path), wantStatus)
	}
	resolvedByConfig, err := cfg.InstalledBinaryPathResolver(ctx)
	if err != nil {
		t.Fatalf("production EmmyLua InstalledBinaryPathResolver: %s", redactEmmyLuaWindowsARM64Text(err.Error(), productRoot))
	}
	if filepath.Clean(resolvedByConfig) != filepath.Clean(installed.Path) {
		t.Fatalf("production resolver path_base=%q, EnsureInstalled path_base=%q", filepath.Base(resolvedByConfig), filepath.Base(installed.Path))
	}
	if err := installer.ValidateWindowsEmmyLuaExecutable(installed.Path); err != nil {
		t.Fatalf("production installed EmmyLua identity: %v", err)
	}
	assertEmmyLuaWindowsARM64Cache(t, productRoot, manifest, asset, installed.Path)

	fixtureRoot := filepath.Join(t.TempDir(), "fixture")
	if err := os.MkdirAll(fixtureRoot, 0o700); err != nil {
		t.Fatalf("create Lua fixture root: %v", err)
	}
	writeEmmyLuaWindowsARM64Fixtures(t, fixtureRoot)

	prependEmmyLuaE2EGoToolchainToPATH(t)
	manifestPath := writeEmmyLuaE2EPackagedManifest(t, productRoot, installed.Path, asset)
	binary := buildMcpLSPBinaryForEmmyLuaE2E(t)
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, fixtureRoot, t.TempDir(), []string{
		"SUPER_DOLPHIN_HOME=" + productRoot,
		// The production manager remains unchanged; this E2E gives its idle recycler
		// 17m of headroom while the proof samples the required 15m window.
		"MCP_LSP_IDLE_TIMEOUT=" + emmyLuaWindowsARM64ManagerIdle.String(),
		// The production resolver returned this exact executable. The test-only
		// bundle makes semantic tools visible at tools/list without aliasing it as
		// LuaLS or relying on PATH discovery.
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=" + productRoot,
		"SUPER_DOLPHIN_LSP_MANIFEST=" + manifestPath,
	})
	mcpPID := client.cmd.Process.Pid
	tracked := map[realMCPProcessKey]realMCPProcessIdentity{}
	startToken, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		_ = closeEmmyLuaClient(t, client)
		t.Fatalf("capture mcp-lsp PID %d start identity: %v", mcpPID, err)
	}
	tracked[realMCPProcessKey{PID: mcpPID, StartToken: startToken}] = realMCPProcessIdentity{PID: mcpPID, StartToken: startToken, Name: "mcp-lsp", Language: "lua"}
	records := make([]emmyLuaARM64ActionRecord, 0, 36)
	idleSamples := make([]emmyLuaARM64IdleSample, 0)
	postIdle := make(map[string]emmyLuaARM64ActionRecord, 3)
	shutdownOK := false
	exitSent := false
	exitCode := -1
	emmyPID := 0
	emmyStartToken := ""
	shutdownSent := false
	failurePhase := "process_start"
	postIdleSemanticPass := false
	defer func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Errorf("EmmyLua receipt finalization panic phase=%s: %v", failurePhase, recovered)
				writeEmmyLuaARM64FailureReceipt(t, failurePhase, lifecycleStartedAt)
			}
		}()
		if client == nil || client.cmd == nil {
			writeEmmyLuaARM64FailureReceipt(t, failurePhase, lifecycleStartedAt)
			return
		}
		failurePhase = "shutdown"
		if !shutdownSent {
			// This is a best-effort failure cleanup. The success path below sends and
			// records the protocol shutdown response before exit.
			_ = writeMCPShutdownWithoutFatal(client)
		}
		treeCaptured := trackRealMCPProcessTree(t, mcpPID, "final-before-close", tracked)
		ownerCmd := client.cmd
		exitSent = closeEmmyLuaClient(t, client)
		if ownerCmd != nil && ownerCmd.ProcessState != nil {
			exitCode = ownerCmd.ProcessState.ExitCode()
		}
		if !treeCaptured {
			t.Errorf("Windows EmmyLua exact process tree snapshot failed; zero-residual proof is incomplete")
		}
		zeroResidual := false
		if treeCaptured {
			if len(tracked) <= 1 {
				t.Errorf("Windows EmmyLua process tree captured no LSP child; exact EmmyLua PID/start residual proof is incomplete")
			} else {
				requireRealMCPProcessIdentitiesGone(t, tracked)
				zeroResidual = true
			}
			writeEmmyLuaARM64ProcessLedger(t, mcpPID, startToken, tracked, zeroResidual, shutdownOK, exitSent, exitCode, failurePhase, lifecycleStartedAt)
		}
		if !exitSent {
			t.Errorf("Windows EmmyLua exit notification/process completion was not proven")
		}
		if !zeroResidual {
			t.Errorf("Windows EmmyLua exact PID/start zero-residual proof failed")
		}
		writeEmmyLuaARM64ActionLedger(t, host, manifest, asset, installed.Path, mcpPID, startToken, emmyPID, emmyStartToken, records, idleSamples, postIdle, shutdownOK, exitSent, zeroResidual, precheck, cacheBefore, cacheAfter, installHTTP, installObservationPass, postIdleSemanticPass, exitCode, failurePhase, lifecycleStartedAt)
	}()

	// MCP initialize is a protocol result object, not a tools/call CallToolResult;
	// client.call already fails on a JSON-RPC error, so do not require structuredContent here.
	failurePhase = "initialize"
	client.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "super-dolphin-emmylua-windows-arm64-e2e", "version": "1"},
	})
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))

	failurePhase = "actions"
	actions := emmyLuaWindowsARM64ActionSpecs(fixtureRoot)
	if len(actions) != 36 {
		t.Fatalf("EmmyLua public action closure=%d, want exact 36", len(actions))
	}
	for _, action := range actions {
		action := action
		t.Run(action.Tool+"/"+action.Name, func(t *testing.T) {
			if action.PreOpen != "" {
				opened := client.callTool(t, "structure", realMCPWindowsToolArguments("lua", fixtureRoot, "structure", "document_symbol", map[string]any{
					"action": "document_symbol", "file_path": action.PreOpen,
				}))
				if opened.Result.IsError {
					t.Fatalf("open patch target file=%s returned MCP error", filepath.Base(action.PreOpen))
				}
			}
			wireArgs := realMCPWindowsToolArguments("lua", fixtureRoot, action.Tool, action.Name, action.Args)
			response := client.callTool(t, action.Tool, wireArgs)
			record := classifyEmmyLuaARM64Action(action.Tool, action.Name, response)
			record.WireSHA256 = emmyLuaWindowsARM64SHA256([]byte(response.Result.ContentText()))
			record.ContentSHA256 = record.WireSHA256
			record.IsError = response.Result.IsError
			logDetail := redactEmmyLuaWindowsARM64Text(record.Detail, productRoot, fixtureRoot)
			if record.Status == emmyLuaActionError {
				stderr := client.stderrString()
				enrichEmmyLuaARM64ErrorEvidence(&record, response, stderr, productRoot, fixtureRoot)
				logDetail = fmt.Sprintf("%s; stderr_sha256=%s; stderr_bytes=%d", logDetail, emmyLuaWindowsARM64SHA256([]byte(stderr)), len(stderr))
			}
			record.Detail = emmyLuaActionReceiptDetail(record.Status)
			records = append(records, record)
			t.Logf("EmmyLua action tool=%s action=%s status=%s detail=%s wire_sha256=%s content_sha256=%s", record.Tool, record.Action, record.Status, logDetail, record.WireSHA256, record.ContentSHA256)
			if record.Status == emmyLuaActionError {
				t.Errorf("unclassified action error: %s", logDetail)
			}
		})
		if len(tracked) > 0 && action.TrackFamily {
			trackRealMCPProcessTree(t, mcpPID, action.Tool, tracked)
		}
	}
	actionCounts := emmyLuaActionCounts(records)
	t.Logf("EmmyLua 36-action ledger total=%d success=%d legal_empty=%d capability_unsupported=%d null=%d error=%d", len(records), actionCounts[string(emmyLuaActionSuccess)], actionCounts[string(emmyLuaActionLegalEmpty)], actionCounts[string(emmyLuaActionCapabilityUnsupported)], actionCounts[string(emmyLuaActionNull)], actionCounts[string(emmyLuaActionError)])
	if actionCounts[string(emmyLuaActionNull)] != 0 || actionCounts[string(emmyLuaActionError)] != 0 {
		t.Fatalf("EmmyLua 36-action ledger has null/error outcomes; these are not PASS")
	}
	if !emmyLuaActionClosurePass(records) {
		t.Fatalf("EmmyLua 36-action closure accounting failed")
	}

	if treeCaptured := trackRealMCPProcessTree(t, mcpPID, "before-shutdown", tracked); !treeCaptured {
		t.Errorf("before-shutdown process tree capture failed")
	}
	failurePhase = "idle"
	emmyPID, emmyStartToken = requireEmmyLuaARM64ProcessIdentity(t, tracked)
	idleSamples = waitForEmmyLuaARM64Idle(t, ctx, emmyPID, emmyStartToken, mcpPID, startToken, idleDurationForEmmyLuaE2E(precheck))
	if !precheck && len(idleSamples) < int(emmyLuaWindowsARM64ProofIdle/time.Minute) {
		t.Errorf("EmmyLua formal idle samples=%d, want at least %d one-minute samples", len(idleSamples), int(emmyLuaWindowsARM64ProofIdle/time.Minute))
	}
	postIdleSpecs := emmyLuaARM64PostIdleSemanticSpecs(fixtureRoot)
	failurePhase = "post_idle"
	postIdleSemanticPass = true
	for _, semantic := range postIdleSpecs {
		response := client.callTool(t, semantic.Tool, realMCPWindowsToolArguments("lua", fixtureRoot, semantic.Tool, semantic.Name, semantic.Args))
		record := classifyEmmyLuaARM64Action(semantic.Tool, semantic.Name, response)
		record.WireSHA256 = emmyLuaWindowsARM64SHA256([]byte(response.Result.ContentText()))
		record.ContentSHA256 = record.WireSHA256
		logDetail := redactEmmyLuaWindowsARM64Text(record.Detail, productRoot, fixtureRoot)
		record.Detail = emmyLuaActionReceiptDetail(record.Status)
		postIdle[semantic.Name] = record
		t.Logf("EmmyLua post-idle semantic tool=%s action=%s status=%s detail=%s wire_sha256=%s content_sha256=%s", semantic.Tool, semantic.Name, record.Status, logDetail, record.WireSHA256, record.ContentSHA256)
		if record.Status != emmyLuaActionSuccess || !realMCPActionSemanticContentNonEmpty(t, response, "EmmyLua post-idle "+semantic.Tool+"/"+semantic.Name) {
			postIdleSemanticPass = false
			t.Errorf("post-idle %s must be successful and non-empty; status=%s", semantic.Name, record.Status)
		}
	}
	if !postIdleSemanticPass || len(postIdle) != len(postIdleSpecs) {
		t.Fatalf("post-idle semantic proof failed: successful non-empty hover/definition/references required")
	}
	failurePhase = "shutdown"
	shutdown := client.call(t, "shutdown", map[string]any{})
	shutdownSent = true
	shutdownOK = shutdown.Error == nil && !shutdown.Result.IsError
	if !shutdownOK {
		t.Errorf("mcp-lsp shutdown response was not successful")
	}
}

type emmyLuaARM64IdleSample struct {
	ElapsedSeconds int64  `json:"elapsedSeconds"`
	PID            int    `json:"emmyLuaPID"`
	StartToken     string `json:"emmyLuaStartToken"`
	Alive          bool   `json:"emmyLuaAlive"`
	MCPPID         int    `json:"mcpPID"`
	MCPStartToken  string `json:"mcpStartToken"`
	MCPAlive       bool   `json:"mcpAlive"`
}

type emmyLuaARM64PostIdleSemanticSpec struct {
	Tool string
	Name string
	Args map[string]any
}

func idleDurationForEmmyLuaE2E(precheck bool) time.Duration {
	if precheck {
		return emmyLuaWindowsARM64PrecheckIdle
	}
	return emmyLuaWindowsARM64ProofIdle
}

// waitForEmmyLuaARM64Idle 只通过 Windows 进程 API 采样，不发送任何 MCP 请求；每分钟
// 复核同一 PID+启动身份，避免把持续的 MCP 活动误记为生命周期健康。
func requireEmmyLuaARM64ProcessIdentity(t *testing.T, tracked map[realMCPProcessKey]realMCPProcessIdentity) (int, string) {
	t.Helper()
	var matches []realMCPProcessIdentity
	for _, identity := range tracked {
		name := strings.ToLower(identity.Name + " " + identity.CommandLine)
		if strings.Contains(name, "emmy") || strings.Contains(name, "emmylua") {
			matches = append(matches, identity)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one tracked EmmyLua child identity, got %d", len(matches))
	}
	return matches[0].PID, matches[0].StartToken
}

func waitForEmmyLuaARM64Idle(t *testing.T, ctx context.Context, emmyPID int, emmyStartToken string, mcpPID int, mcpStartToken string, duration time.Duration) []emmyLuaARM64IdleSample {
	t.Helper()
	if duration <= 0 {
		t.Fatalf("EmmyLua idle duration must be positive, got %s", duration)
	}
	started := time.Now()
	samples := make([]emmyLuaARM64IdleSample, 0, int(duration/time.Minute)+2)
	sample := func() {
		alive, err := processAliveForE2E(emmyPID)
		if err != nil {
			t.Fatalf("EmmyLua idle PID=%d process sample failed: %s", emmyPID, redactEmmyLuaWindowsARM64Text(err.Error()))
		}
		current, err := windowsGoplsProcessStartIdentity(emmyPID)
		if err != nil {
			t.Fatalf("EmmyLua idle PID=%d start identity sample failed: %s", emmyPID, redactEmmyLuaWindowsARM64Text(err.Error()))
		}
		mcpAlive, err := processAliveForE2E(mcpPID)
		if err != nil {
			t.Fatalf("MCP idle PID=%d process sample failed: %s", mcpPID, redactEmmyLuaWindowsARM64Text(err.Error()))
		}
		mcpCurrent, err := windowsGoplsProcessStartIdentity(mcpPID)
		if err != nil {
			t.Fatalf("MCP idle PID=%d start identity sample failed: %s", mcpPID, redactEmmyLuaWindowsARM64Text(err.Error()))
		}
		if !alive || current != emmyStartToken || !mcpAlive || mcpCurrent != mcpStartToken {
			t.Fatalf("EmmyLua/MCP idle PID/start identity changed: emmy_pid=%d emmy_alive=%t emmy_start=%s want=%s mcp_pid=%d mcp_alive=%t mcp_start=%s want=%s", emmyPID, alive, current, emmyStartToken, mcpPID, mcpAlive, mcpCurrent, mcpStartToken)
		}
		elapsed := time.Since(started)
		samples = append(samples, emmyLuaARM64IdleSample{ElapsedSeconds: int64(elapsed / time.Second), PID: emmyPID, StartToken: current, Alive: alive, MCPPID: mcpPID, MCPStartToken: mcpCurrent, MCPAlive: mcpAlive})
		t.Logf("EmmyLua Windows ARM64 idle heartbeat source=windows-process-api-no-mcp-call elapsed=%s emmy_pid=%d emmy_start=%s mcp_pid=%d mcp_start=%s", elapsed.Round(time.Second), emmyPID, current, mcpPID, mcpCurrent)
	}

	sample()
	deadline := started.Add(duration)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		wait := remaining
		if wait > time.Minute {
			wait = time.Minute
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			t.Fatalf("EmmyLua idle sampling stopped before %s: %s", duration, redactEmmyLuaWindowsARM64Text(ctx.Err().Error()))
		case <-timer.C:
			if time.Now().Before(deadline) {
				sample()
			}
		}
	}
	// The final sample is outside the MCP request path and proves the full requested
	// window, even when the ticker fired just before the deadline.
	sample()
	return samples
}

func emmyLuaARM64PostIdleSemanticSpecs(root string) []emmyLuaARM64PostIdleSemanticSpec {
	main := filepath.Join(root, "main.lua")
	mainPos := main + ":20:16"
	return []emmyLuaARM64PostIdleSemanticSpec{
		{Tool: "inspect", Name: "hover", Args: map[string]any{"action": "hover", "pos": mainPos}},
		{Tool: "inspect", Name: "definition", Args: map[string]any{"action": "definition", "pos": mainPos}},
		{Tool: "xref", Name: "references", Args: map[string]any{"action": "references", "pos": mainPos, "include_declaration": true, "max_results": 20}},
	}
}

func emmyLuaWindowsARM64SHA256(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func redactEmmyLuaWindowsARM64Text(value string, roots ...string) string {
	for _, root := range roots {
		if strings.TrimSpace(root) != "" {
			value = strings.ReplaceAll(value, root, "<private-root>")
			value = strings.ReplaceAll(value, filepath.ToSlash(root), "<private-root>")
		}
	}
	return emmyLuaWindowsARM64AbsolutePath.ReplaceAllString(value, "<absolute-path>")
}

func emmyLuaActionReceiptDetail(status emmyLuaActionStatus) string {
	switch status {
	case emmyLuaActionSuccess:
		return ""
	case emmyLuaActionLegalEmpty:
		return "legal_empty_nonsemantic"
	case emmyLuaActionCapabilityUnsupported:
		return "capability_unsupported"
	case emmyLuaActionNull:
		return "null_structured_content"
	case emmyLuaActionError:
		return "error"
	default:
		return "unclassified"
	}
}

// requireEmmyLuaWindowsARM64LockedFacts freezes the external URL/version/SHA contract in
// the test as well as production, so a changed production manifest cannot silently widen the
// E2E to a different release.
func requireEmmyLuaWindowsARM64LockedFacts(t *testing.T, host installer.WindowsHostPlatform) (installer.WindowsLockedAssetManifest, installer.WindowsLockedAsset) {
	t.Helper()
	manifest := installer.WindowsEmmyLuaManifest()
	if err := manifest.Validate(); err != nil {
		t.Fatalf("EmmyLua manifest validation: %v", err)
	}
	if manifest.Name != string(installer.WindowsLSPProductEmmyLua) {
		t.Fatalf("EmmyLua manifest name=%q", manifest.Name)
	}
	asset, ok := manifest.Assets[installer.WindowsHostArchARM64]
	if !ok {
		t.Fatalf("EmmyLua manifest has no ARM64 asset: %#v", manifest.Assets)
	}
	if asset.Version != installer.WindowsEmmyLuaVersion || asset.URL != emmyLuaWindowsARM64ArchiveURL || !strings.EqualFold(asset.SHA256, emmyLuaWindowsARM64ArchiveSHA256) || asset.BinaryPath != installer.WindowsEmmyLuaBinaryName {
		t.Fatalf("EmmyLua locked asset changed: %#v", asset)
	}
	selected, err := installer.WindowsEmmyLuaAssetForPlatform(host)
	if err != nil || selected.Architecture != installer.WindowsHostArchARM64 {
		t.Fatalf("native ARM64 EmmyLua selection: asset=%#v err=%v", selected, err)
	}
	processOnly := host
	processOnly.ProcessArch = installer.WindowsHostArchX64
	if selectedForProcessOnly, err := installer.WindowsEmmyLuaAssetForPlatform(processOnly); err != nil || selectedForProcessOnly.Architecture != installer.WindowsHostArchARM64 {
		t.Fatalf("ProcessArch must remain diagnostic when native ARM64: asset=%#v err=%v", selectedForProcessOnly, err)
	}
	wrongNative := host
	wrongNative.NativeArch = installer.WindowsHostArchX64
	wrongNative.ProcessArch = installer.WindowsHostArchARM64
	if _, err := installer.WindowsEmmyLuaAssetForPlatform(wrongNative); !errors.Is(err, installer.ErrWindowsEmmyLuaRequiresARM64) {
		t.Fatalf("native x64 EmmyLua selection err=%v, want ErrWindowsEmmyLuaRequiresARM64", err)
	}
	return manifest, asset
}

func mustResolveEmmyLuaPath(t *testing.T, productRoot string) string {
	t.Helper()
	path, err := installer.ResolveWindowsEmmyLuaAssetPath(productRoot)
	if err != nil {
		t.Fatalf("ResolveWindowsEmmyLuaAssetPath(private-root): %s", redactEmmyLuaWindowsARM64Text(err.Error(), productRoot))
	}
	return path
}

func assertEmmyLuaWindowsARM64Cache(t *testing.T, productRoot string, manifest installer.WindowsLockedAssetManifest, asset installer.WindowsLockedAsset, executable string) {
	t.Helper()
	payload := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir, manifest.Name, asset.Version, asset.Architecture, strings.ToLower(asset.SHA256), "payload.zip")
	archiveDigest := sha256FileForEmmyLuaE2E(t, payload)
	if !strings.EqualFold(archiveDigest, asset.SHA256) || !strings.EqualFold(archiveDigest, emmyLuaWindowsARM64ArchiveSHA256) {
		t.Fatalf("downloaded EmmyLua payload SHA256=%s want=%s", archiveDigest, asset.SHA256)
	}
	contents, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("read EmmyLua executable file=%q: %s", filepath.Base(executable), redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	digest := sha256.Sum256(contents)
	gotSHA := hex.EncodeToString(digest[:])
	if !strings.EqualFold(gotSHA, emmyLuaWindowsARM64ExecutableSHA256) {
		t.Fatalf("EmmyLua executable SHA256=%s want=%s", gotSHA, emmyLuaWindowsARM64ExecutableSHA256)
	}
	image, err := pe.NewFile(bytes.NewReader(contents))
	if err != nil {
		t.Fatalf("parse EmmyLua PE file=%q: %s", filepath.Base(executable), redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	defer image.Close()
	if image.FileHeader.Machine != installer.WindowsEmmyLuaPEMachine {
		t.Fatalf("EmmyLua PE machine=0x%04x want ARM64 0x%04x", image.FileHeader.Machine, installer.WindowsEmmyLuaPEMachine)
	}
	if filepath.Base(executable) != installer.WindowsEmmyLuaBinaryName || !strings.HasPrefix(filepath.Clean(executable), filepath.Clean(productRoot)+string(os.PathSeparator)) {
		t.Fatalf("EmmyLua resolver escaped private product root: executable=%q", filepath.Base(executable))
	}
}

func writeEmmyLuaE2EPackagedManifest(t *testing.T, productRoot, executable string, asset installer.WindowsLockedAsset) string {
	t.Helper()
	manifestPath := filepath.Join(productRoot, "lsp-manifest.json")
	relativeExecutable, err := filepath.Rel(productRoot, executable)
	if err != nil || filepath.IsAbs(relativeExecutable) || relativeExecutable == ".." || strings.HasPrefix(relativeExecutable, ".."+string(filepath.Separator)) {
		t.Fatalf("EmmyLua resolver-owned executable is not inside product root: file=%q", filepath.Base(executable))
	}
	manifest := map[string]any{
		"servers": map[string]any{
			string(installer.WindowsLSPProductEmmyLua): map[string]any{
				"path":      filepath.ToSlash(relativeExecutable),
				"version":   asset.Version,
				"sha256":    emmyLuaWindowsARM64ExecutableSHA256,
				"languages": []string{"lua"},
			},
		},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal EmmyLua E2E packaged manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write EmmyLua E2E packaged manifest: %v", err)
	}
	t.Logf("EmmyLua E2E child bundle uses production-resolved executable=%s manifest=%s", filepath.Base(executable), filepath.Base(manifestPath))
	return manifestPath
}

func sha256FileForEmmyLuaE2E(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read SHA256 target file=%q: %s", filepath.Base(path), redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read empty-cache directory: %s", redactEmmyLuaWindowsARM64Text(err.Error(), path))
	}
	if len(entries) != 0 {
		t.Fatalf("private product root is not empty before download: entries=%v", entries)
	}
}

// buildMcpLSPBinaryForEmmyLuaE2E 使用仓库锁定的原生 ARM64 Go，不把 PATH 或系统 Go
// 当作正式证明输入；正式证明要求 GOWORK 未设置，并固定 GOTOOLCHAIN/CGO 以便复核构建身份。
func buildMcpLSPBinaryForEmmyLuaE2E(t *testing.T) string {
	t.Helper()
	if value, exists := os.LookupEnv("GOWORK"); exists {
		t.Fatalf("EmmyLua formal proof requires GOWORK to be unset, got %q", value)
	}
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	goExecutable := filepath.Join(repoRoot, ".build-cache", "go1.26.5-windows-arm64", "go", "bin", "go.exe")
	if _, err := os.Stat(goExecutable); err != nil {
		t.Fatalf("locked Go %s is unavailable: %s", emmyLuaWindowsARM64LockedGoVersion, redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	output := filepath.Join(t.TempDir(), lspBinaryExecutableNameForTest())
	cmd := exec.Command(goExecutable, "build", "-buildvcs=false", "-o", output, "./cmd/mcp-lsp")
	cmd.Dir = repoRoot
	cmd.Env = emmyLuaWindowsARM64SetEnv(emmyLuaWindowsARM64SetEnv(os.Environ(), "GOTOOLCHAIN", "local"), "CGO_ENABLED", "0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build mcp-lsp with locked Go %s failed: output_sha256=%s output_bytes=%d cause=%s", emmyLuaWindowsARM64LockedGoVersion, emmyLuaWindowsARM64SHA256(out), len(out), redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	t.Logf("EmmyLua build toolchain=%s platform=windows-native-arm64-process-arm64 output=%s", emmyLuaWindowsARM64LockedGoVersion, filepath.Base(output))
	return output
}

func emmyLuaWindowsARM64SetEnv(env []string, key, value string) []string {
	prefix := key + "="
	for index, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func prependEmmyLuaE2EGoToolchainToPATH(t *testing.T) {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	goBin := filepath.Join(repoRoot, ".build-cache", "go1.26.5-windows-arm64", "go", "bin")
	goExecutable := filepath.Join(goBin, "go.exe")
	if _, err := os.Stat(goExecutable); err != nil {
		t.Fatalf("locked Go %s is unavailable for child PATH: %s", emmyLuaWindowsARM64LockedGoVersion, redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	pathEntries := []string{goBin}
	if current := strings.TrimSpace(os.Getenv("PATH")); current != "" {
		pathEntries = append(pathEntries, strings.Split(current, string(os.PathListSeparator))...)
	}
	t.Setenv("PATH", strings.Join(pathEntries, string(os.PathListSeparator)))
}

func writeEmmyLuaWindowsARM64Fixtures(t *testing.T, root string) {
	t.Helper()
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	sourceRoot := filepath.Join(repoRoot, "bin", "LSP", "test")
	luaSourceRoot := filepath.Join(sourceRoot, "lua")
	if _, err := os.Stat(luaSourceRoot); err != nil {
		t.Fatalf("inspect checked-in Lua fixture root: %v", err)
	}
	// 真实 Lua 工程快照必须先复制到隔离 workspace；所有 MCP 编辑动作再使用
	// 该快照的独立副本，避免污染仓库内的 bin/LSP/test/lua 源文件。
	copyRealMCPBinSourceTree(t, luaSourceRoot, root)
	for _, relative := range []string{"lsp_fixture.lua", "middleclass.lua", "spec/class_spec.lua"} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("copied Lua fixture %q is unavailable: %v", relative, err)
		}
	}
	// grep/ast_search 是 sidecar ast-grep 动作，不是 EmmyLua LSP 请求；使用
	// 独立 JavaScript 辅助文件验证该公共动作，不把能力归因给 Lua server。
	writeRealFixture(t, filepath.Join(root, "ast.js"), "function ast_greet(x) {\n  return x;\n}\n")
	for _, name := range []string{"replace", "rename", "code_action", "format"} {
		destination := filepath.Join(root, ".mcp-actions", name, "lsp_fixture.lua")
		copyRealMCPBinSourceFile(t, sourceRoot, "lua/lsp_fixture.lua", destination)
	}
}

// TestEmmyLuaWindowsARM64FixtureContract 证明 action fixture 来自 bin/LSP/test/lua 的隔离副本，
// 且七个公开工具仍保持精确 36-action 闭包；该测试不启动 server 或下载产品。
func TestEmmyLuaWindowsARM64FixtureContract(t *testing.T) {
	root := t.TempDir()
	writeEmmyLuaWindowsARM64Fixtures(t, root)
	source := filepath.Join(repoRootForMcpLSPBinaryTest(t), "bin", "LSP", "test", "lua", "lsp_fixture.lua")
	target := filepath.Join(root, "lsp_fixture.lua")
	sourceBytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read checked-in Lua fixture: %v", err)
	}
	targetBytes, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read isolated Lua fixture: %v", err)
	}
	if !bytes.Equal(sourceBytes, targetBytes) {
		t.Fatal("isolated Lua fixture differs from bin/LSP/test/lua source")
	}
	actions := emmyLuaWindowsARM64ActionSpecs(root)
	if len(actions) != 36 {
		t.Fatalf("EmmyLua fixture action count=%d, want exact 36", len(actions))
	}
	for _, action := range actions {
		if strings.TrimSpace(action.Tool) == "" || strings.TrimSpace(action.Name) == "" {
			t.Fatalf("EmmyLua fixture action has empty tool/name: %#v", action)
		}
	}
}

type emmyLuaARM64ActionSpec struct {
	Tool        string
	Name        string
	Args        map[string]any
	PreOpen     string
	TrackFamily bool
}

func emmyLuaWindowsARM64ActionSpecs(root string) []emmyLuaARM64ActionSpec {
	main := filepath.Join(root, "lsp_fixture.lua")
	secondary := filepath.Join(root, "middleclass.lua")
	signature := filepath.Join(root, "spec", "class_spec.lua")
	call := filepath.Join(root, "spec", "class_spec.lua")
	ast := filepath.Join(root, "ast.js")
	replace := filepath.Join(root, ".mcp-actions", "replace", "lsp_fixture.lua")
	rename := filepath.Join(root, ".mcp-actions", "rename", "lsp_fixture.lua")
	codeAction := filepath.Join(root, ".mcp-actions", "code_action", "lsp_fixture.lua")
	format := filepath.Join(root, ".mcp-actions", "format", "lsp_fixture.lua")
	mainPos := main + ":3:10"
	return []emmyLuaARM64ActionSpec{
		{Tool: "file", Name: "open_file", Args: map[string]any{"action": "open_file", "file_path": main}, TrackFamily: true},
		{Tool: "file", Name: "read_file-single", Args: map[string]any{"action": "read_file", "file_path": main, "limit": 100}},
		{Tool: "file", Name: "read_file-full", Args: map[string]any{"action": "read_file", "file_path": main}},
		{Tool: "file", Name: "read_file-batch", Args: map[string]any{"action": "read_file", "file_paths": []string{main, secondary}, "limit": 100}},
		{Tool: "file", Name: "read_file-lines", Args: map[string]any{"action": "read_file", "pos": main + ":3", "scope": "lines", "limit": 1}},
		{Tool: "file", Name: "read_file-function", Args: map[string]any{"action": "read_file", "pos": main + ":3", "limit": 50}},
		{Tool: "file", Name: "diagnostics", Args: map[string]any{"action": "diagnostics", "file_path": codeAction}, TrackFamily: true},
		{Tool: "file", Name: "diagnostics-batch", Args: map[string]any{"action": "diagnostics", "file_paths": []string{codeAction, main}, "limit": 100}},

		{Tool: "inspect", Name: "hover", Args: map[string]any{"action": "hover", "pos": mainPos}, TrackFamily: true},
		{Tool: "inspect", Name: "definition", Args: map[string]any{"action": "definition", "pos": mainPos}},
		{Tool: "inspect", Name: "implementation", Args: map[string]any{"action": "implementation", "pos": mainPos}},
		{Tool: "inspect", Name: "type_definition", Args: map[string]any{"action": "type_definition", "pos": mainPos}},
		{Tool: "inspect", Name: "signature_help", Args: map[string]any{"action": "signature_help", "pos": signature + ":7:22"}},

		{Tool: "xref", Name: "references", Args: map[string]any{"action": "references", "pos": mainPos, "include_declaration": true, "max_results": 20}, TrackFamily: true},
		{Tool: "xref", Name: "references-no-declaration", Args: map[string]any{"action": "references", "pos": mainPos, "include_declaration": false, "max_results": 20}},
		{Tool: "xref", Name: "call_hierarchy-incoming", Args: map[string]any{"action": "call_hierarchy", "pos": call + ":1:10", "direction": "incoming"}},
		{Tool: "xref", Name: "call_hierarchy-outgoing", Args: map[string]any{"action": "call_hierarchy", "pos": call + ":1:10", "direction": "outgoing"}},
		{Tool: "xref", Name: "call_hierarchy-both", Args: map[string]any{"action": "call_hierarchy", "pos": call + ":1:10", "direction": "both"}},
		{Tool: "xref", Name: "type_hierarchy-supertypes", Args: map[string]any{"action": "type_hierarchy", "pos": mainPos, "direction": "supertypes"}},
		{Tool: "xref", Name: "type_hierarchy-subtypes", Args: map[string]any{"action": "type_hierarchy", "pos": mainPos, "direction": "subtypes"}},

		{Tool: "grep", Name: "text_search", Args: map[string]any{"action": "text_search", "query": "middleclass v4.1.1", "paths": []string{secondary}, "max_results": 10}, TrackFamily: true},
		{Tool: "grep", Name: "text_search-regex", Args: map[string]any{"action": "text_search", "query": "middleclass v4\\.[0-9]+\\.[0-9]+", "paths": []string{secondary}, "regex": true, "case_sensitive": true, "max_results": 10}},
		{Tool: "grep", Name: "text_search-paths", Args: map[string]any{"action": "text_search", "query": "middleclass v4.1.1", "paths": []string{filepath.Dir(secondary)}, "max_results": 10}},
		{Tool: "grep", Name: "text_search-file_paths", Args: map[string]any{"action": "text_search", "query": "middleclass v4.1.1", "paths": []string{secondary}, "max_results": 10}},
		{Tool: "grep", Name: "text_search-glob", Args: map[string]any{"action": "text_search", "query": "middleclass v4.1.1", "paths": []string{filepath.Dir(secondary)}, "glob": filepath.Base(secondary), "max_results": 10}},
		{Tool: "grep", Name: "ast_search", Args: map[string]any{"action": "ast_search", "query": "function $NAME($$$ARGS) { $$$BODY }", "paths": []string{ast}, "ast_language": "javascript", "max_results": 10}},

		{Tool: "structure", Name: "document_symbol", Args: map[string]any{"action": "document_symbol", "file_path": main, "max_results": 20}, TrackFamily: true},
		{Tool: "structure", Name: "workspace_symbol-file", Args: map[string]any{"action": "workspace_symbol", "file_path": main, "query": "M", "max_results": 20}},
		{Tool: "structure", Name: "workspace_symbol-language", Args: map[string]any{"action": "workspace_symbol", "workspace_language": "lua", "query": "M", "max_results": 20}},
		{Tool: "structure", Name: "folding_range", Args: map[string]any{"action": "folding_range", "file_path": main, "max_results": 20}},
		{Tool: "structure", Name: "semantic_tokens", Args: map[string]any{"action": "semantic_tokens", "file_path": main, "max_results": 20}},

		{Tool: "patch_edit", Name: "replace_range", PreOpen: replace, Args: map[string]any{"action": "replace_range", "file_path": replace, "patch": "@@\n-local M = {}\n+local M = { marker = true }\n"}, TrackFamily: true},
		{Tool: "patch_edit", Name: "rename", PreOpen: rename, Args: map[string]any{"action": "rename", "pos": rename + ":3:10", "new_name": "welcome"}},
		{Tool: "patch_edit", Name: "code_action", PreOpen: codeAction, Args: map[string]any{"action": "code_action", "pos": codeAction + ":1:1", "only": []string{"quickfix"}}},
		{Tool: "patch_edit", Name: "format", PreOpen: format, Args: map[string]any{"action": "format", "file_path": format}},

		{Tool: "completion", Name: "completion", Args: map[string]any{"pos": mainPos, "max_results": 20}, TrackFamily: true},
	}
}

type emmyLuaActionStatus string

const (
	emmyLuaActionSuccess               emmyLuaActionStatus = "success"
	emmyLuaActionLegalEmpty            emmyLuaActionStatus = "legal_empty"
	emmyLuaActionCapabilityUnsupported emmyLuaActionStatus = "capability_unsupported"
	emmyLuaActionNull                  emmyLuaActionStatus = "null"
	emmyLuaActionError                 emmyLuaActionStatus = "error"
)

type emmyLuaARM64ActionRecord struct {
	Tool                string              `json:"tool"`
	Action              string              `json:"action"`
	Status              emmyLuaActionStatus `json:"status"`
	Detail              string              `json:"detail,omitempty"`
	WireSHA256          string              `json:"wireSHA256,omitempty"`
	ContentSHA256       string              `json:"contentSHA256,omitempty"`
	IsError             bool                `json:"isError,omitempty"`
	ErrorContentSummary string              `json:"errorContentSummary,omitempty"`
	ErrorTextSHA256     string              `json:"errorTextSHA256,omitempty"`
	ErrorTextBytes      int                 `json:"errorTextBytes,omitempty"`
	SidecarStderrSHA256 string              `json:"sidecarStderrSHA256,omitempty"`
	SidecarStderrBytes  int                 `json:"sidecarStderrBytes,omitempty"`
	SidecarStderrTail   string              `json:"sidecarStderrTail,omitempty"`
}

// enrichEmmyLuaARM64ErrorEvidence 只在错误 action 上保存可复核的脱敏摘要与哈希；
// 原始路径、URL、token 不进入 receipt，stderr 尾部也设上限，避免失败证据反向泄露环境。
func enrichEmmyLuaARM64ErrorEvidence(record *emmyLuaARM64ActionRecord, response mcpLSPBinaryResponse, stderr string, roots ...string) {
	if record == nil || record.Status != emmyLuaActionError {
		return
	}
	text := strings.TrimSpace(response.Result.ContentText())
	record.ErrorContentSummary = emmyLuaE2EStderrSummary(redactEmmyLuaWindowsARM64Text(text, roots...))
	record.ErrorTextSHA256 = emmyLuaWindowsARM64SHA256([]byte(text))
	record.ErrorTextBytes = len([]byte(text))
	record.SidecarStderrSHA256 = emmyLuaWindowsARM64SHA256([]byte(stderr))
	record.SidecarStderrBytes = len([]byte(stderr))
	record.SidecarStderrTail = emmyLuaE2EStderrSummary(redactEmmyLuaWindowsARM64Text(stderr, roots...))
}

// classifyEmmyLuaARM64Action enforces the wire contract without upgrading empty/null or
// capability_unsupported into semantic success. Transport/runtime failures remain errors.
func classifyEmmyLuaARM64Action(tool, action string, response mcpLSPBinaryResponse) emmyLuaARM64ActionRecord {
	record := emmyLuaARM64ActionRecord{Tool: tool, Action: action, Status: emmyLuaActionSuccess}
	structured := bytes.TrimSpace(response.Result.StructuredContent)
	text := strings.TrimSpace(response.Result.ContentText())
	if len(structured) > 0 && !bytes.Equal(structured, []byte("null")) {
		record.Status = emmyLuaActionError
		record.Detail = "structuredContent must be empty under content-only MCP contract"
		return record
	}
	if response.Error != nil {
		record.Status = emmyLuaActionError
		record.Detail = fmt.Sprintf("jsonrpc_error code=%d message=%s", response.Error.Code, response.Error.Message)
		return record
	}
	if text == "" {
		record.Status = emmyLuaActionNull
		record.Detail = "MCP result has empty ContentText"
		return record
	}
	doc, err := lineprotocol.Parse(text)
	if err != nil {
		if !response.Result.IsError && realMCPRemoteCapabilityEmptyMessage(strings.ToLower(text)) {
			record.Status = emmyLuaActionCapabilityUnsupported
			record.Detail = text
			return record
		}
		record.Status = emmyLuaActionError
		record.Detail = fmt.Sprintf("ContentText is not strict line protocol: %v", err)
		return record
	}
	if doc.Error != nil {
		if doc.Error.Code == "capability_unsupported" {
			record.Status = emmyLuaActionCapabilityUnsupported
		} else {
			record.Status = emmyLuaActionError
		}
		record.Detail = firstNonEmptyEmmyLuaDetailLocal(doc.Error.Code, text)
		return record
	}
	if response.Result.IsError {
		record.Status = emmyLuaActionError
		record.Detail = "MCP result marked isError without an ERROR line protocol header"
		return record
	}
	if realMCPRemoteCapabilityEmptyMessage(strings.ToLower(text)) {
		record.Status = emmyLuaActionCapabilityUnsupported
		record.Detail = text
		return record
	}
	if !realMCPActionContentNonEmpty(doc) {
		record.Status = emmyLuaActionLegalEmpty
		record.Detail = "successful line protocol has showing=0; not semantic PASS"
	}
	return record
}

func firstNonEmptyEmmyLuaDetailLocal(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unspecified action result"
}

func emmyLuaE2EStderrSummary(stderr string) string {
	const maxBytes = 4096
	trimmed := strings.TrimSpace(stderr)
	if len(trimmed) <= maxBytes {
		return trimmed
	}
	return "...[stderr truncated] " + trimmed[len(trimmed)-maxBytes:]
}

func writeMCPShutdownWithoutFatal(client *mcpLSPBinaryClient) error {
	if client == nil || client.cmd == nil {
		return errors.New("mcp-lsp client is not live")
	}
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "shutdown", "params": map[string]any{}})
	if err != nil {
		return err
	}
	if _, err := client.stdin.Write(append(raw, '\n')); err != nil {
		return err
	}
	// Failure cleanup must not block forever on a malformed or stalled action. The
	// normal path reads the shutdown response with client.call before exit.
	return nil
}

// closeEmmyLuaClient 明确等待 exit 通知对应的 owner 进程退出；只有写入 exit、等待完成且
// 未触发强制 Kill 时才返回 true，避免 receipt 把超时清理误记为协议生命周期成功。
func closeEmmyLuaClient(t *testing.T, client *mcpLSPBinaryClient) bool {
	t.Helper()
	if client == nil || client.cmd == nil {
		return false
	}
	cmd := client.cmd
	client.cmd = nil
	closeHook := client.closeHook
	client.closeHook = nil
	exitPayload, marshalErr := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "exit"})
	exitSent := marshalErr == nil
	if marshalErr == nil {
		if _, err := client.stdin.Write(append(exitPayload, '\n')); err != nil {
			exitSent = false
			t.Errorf("EmmyLua exit notification write failed: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		}
	}
	if err := client.stdin.Close(); err != nil {
		exitSent = false
		t.Errorf("EmmyLua stdio close failed: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := true
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(30 * time.Second):
		waited = false
		_ = cmd.Process.Kill()
		waitErr = <-done
		t.Errorf("EmmyLua mcp-lsp owner required forced kill after exit; stderr_sha256=%s", emmyLuaWindowsARM64SHA256([]byte(client.stderrString())))
	}
	if closeHook != nil {
		if err := closeHook(); err != nil {
			waited = false
			t.Errorf("close mcp-lsp test process owner failed: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		}
	}
	if waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
		waited = false
		t.Logf("EmmyLua mcp-lsp owner exit error=%s stderr_sha256=%s", redactEmmyLuaWindowsARM64Text(waitErr.Error()), emmyLuaWindowsARM64SHA256([]byte(client.stderrString())))
	}
	return exitSent && waited
}

func emmyLuaEvidenceDir(t *testing.T) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv(emmyLuaWindowsARM64EvidenceDirEnv))
	if dir == "" {
		dir = filepath.Join(repoRootForMcpLSPBinaryTest(t), ".build-cache", "codex-emmylua-arm64-proof", "windows-arm64-e2e")
		t.Setenv(emmyLuaWindowsARM64EvidenceDirEnv, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create EmmyLua evidence directory: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
	}
	return dir
}

func writeEmmyLuaARM64FailureReceipt(t *testing.T, phase string, startedAt time.Time) string {
	t.Helper()
	dir := emmyLuaEvidenceDir(t)
	payload := map[string]any{
		"schema":       "windows-arm64-emmylua-process-arm64-failure-v1",
		"status":       "NON_PASS",
		"failurePhase": phase,
		"timestamps":   map[string]string{"startedAt": startedAt.UTC().Format(time.RFC3339Nano), "receiptAt": time.Now().UTC().Format(time.RFC3339Nano)},
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Logf("marshal EmmyLua failure receipt: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	path := filepath.Join(dir, "windows-arm64-emmylua-process-arm64-failure-receipt.json")
	if err := writeEmmyLuaARM64Receipt(path, encoded); err != nil {
		t.Logf("write EmmyLua failure receipt: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	return path
}

func writeEmmyLuaARM64ActionLedger(t *testing.T, host installer.WindowsHostPlatform, manifest installer.WindowsLockedAssetManifest, asset installer.WindowsLockedAsset, executable string, pid int, startToken string, emmyPID int, emmyStartToken string, records []emmyLuaARM64ActionRecord, idleSamples []emmyLuaARM64IdleSample, postIdle map[string]emmyLuaARM64ActionRecord, shutdownOK, exitSent, zeroResidual, precheck bool, cacheBefore, cacheAfter emmyLuaCacheSnapshot, installHTTP emmyLuaHTTPCounts, installObservationPass, postIdleSemanticPass bool, exitCode int, failurePhase string, lifecycleStartedAt time.Time) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv(emmyLuaWindowsARM64EvidenceDirEnv))
	if dir == "" {
		dir = filepath.Join(repoRootForMcpLSPBinaryTest(t), ".build-cache", "codex-emmylua-arm64-proof", "windows-arm64-e2e")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Logf("EmmyLua action ledger directory unavailable: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	counts := emmyLuaActionCounts(records)
	formalPass := !precheck && emmyLuaActionClosurePass(records) && len(idleSamples) >= int(emmyLuaWindowsARM64ProofIdle/time.Minute) && postIdleSemanticPass && shutdownOK && exitSent && zeroResidual && exitCode == 0
	cacheReadyBefore := cacheBefore.PayloadPresent && cacheBefore.ReadyExecutablePresent
	installCacheDecision := "auto_install_or_repair"
	if cacheReadyBefore {
		installCacheDecision = "reused_ready_product_cache"
	} else if cacheBefore.PayloadPresent {
		installCacheDecision = "reused_payload_cache_repair"
	}
	payload := map[string]any{
		"schema":     "windows-arm64-emmylua-process-arm64-formal-e2e-v3",
		"phase":      map[bool]string{true: "PRECHECK_ONLY", false: failurePhase}[precheck],
		"status":     map[bool]string{true: "NON_PASS_PRECHECK_ONLY", false: map[bool]string{true: "PASS", false: "NON_PASS"}[formalPass]}[precheck],
		"timestamps": map[string]string{"startedAt": lifecycleStartedAt.UTC().Format(time.RFC3339Nano), "receiptAt": time.Now().UTC().Format(time.RFC3339Nano)},
		"product":    "emmylua-analyzer-rust",
		"host":       map[string]any{"os": host.OS, "nativeArch": host.NativeArch, "processArch": host.ProcessArch, "windowsBuild": host.WindowsBuild},
		"manifest": map[string]any{
			"name": manifest.Name, "version": asset.Version, "architecture": asset.Architecture,
			"url": asset.URL, "archiveSHA256": asset.SHA256, "format": asset.Format,
		},
		"executable": map[string]any{
			"name": filepath.Base(executable), "sha256": emmyLuaWindowsARM64ExecutableSHA256,
			"peMachine": fmt.Sprintf("0x%04x", installer.WindowsEmmyLuaPEMachine),
		},
		"toolchain": map[string]any{"go": emmyLuaWindowsARM64LockedGoVersion, "buildvcs": false, "gowork": "unset", "gotoolchain": "local", "cgo": "0"},
		"install": map[string]any{
			"source":           "production_EnsureInstalled",
			"status":           map[bool]string{true: string(installer.InstallStatusPathFound), false: string(installer.InstallStatusInstalledPath)}[cacheReadyBefore],
			"cacheDecision":    installCacheDecision,
			"cacheEmptyBefore": cacheBefore.RootEntries == 0 && !cacheBefore.PayloadPresent,
			"cacheReadyBefore": cacheReadyBefore,
			"binary":           installer.WindowsEmmyLuaBinaryName,
		},
		"installationObservation": map[string]any{
			"contract_pass": installObservationPass,
			"cache_before":  cacheBefore,
			"cache_after":   cacheAfter,
			"http":          installHTTP,
		},
		"process": map[string]any{"mcpPID": pid, "mcpStartToken": startToken, "emmyLuaPID": emmyPID, "emmyLuaStartToken": emmyStartToken},
		"actions": map[string]any{
			"total": len(records), "counts": counts, "records": records,
			"closureAccountingPass":        emmyLuaActionClosurePass(records),
			"nonEmptySemanticSuccess":      counts[string(emmyLuaActionSuccess)],
			"all36NonEmptySemanticSuccess": len(records) == 36 && counts[string(emmyLuaActionSuccess)] == 36,
		},
		"idle": map[string]any{
			"managerIdleTimeout": emmyLuaWindowsARM64ManagerIdle.String(),
			"requested":          map[bool]string{true: emmyLuaWindowsARM64PrecheckIdle.String(), false: emmyLuaWindowsARM64ProofIdle.String()}[precheck],
			"minimumProduction":  "15m",
			"headroom":           "process_identity_sampling_only",
			"samplingSource":     "windows-process-api-no-mcp-call",
			"samples":            idleSamples,
		},
		"postIdleSemantic": postIdle,
		"lifecycle":        map[string]any{"shutdownResponse": shutdownOK, "exitSent": exitSent, "exitCode": exitCode, "zeroResidual": zeroResidual, "formalPass": formalPass},
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Logf("marshal EmmyLua action ledger: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	path := filepath.Join(dir, "windows-arm64-emmylua-process-arm64-action-ledger.json")
	if err := writeEmmyLuaARM64Receipt(path, encoded); err != nil {
		t.Logf("write EmmyLua action ledger: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	t.Logf("EmmyLua Windows ARM64/process ARM64 action receipt=%s receipt_sha256=%s", filepath.Base(path), emmyLuaWindowsARM64SHA256(emmyLuaWindowsARM64ReceiptBytes(encoded)))
	return path
}

func writeEmmyLuaARM64ProcessLedger(t *testing.T, pid int, startToken string, tracked map[realMCPProcessKey]realMCPProcessIdentity, zeroResidual, shutdownOK, exitSent bool, exitCode int, phase string, startedAt time.Time) string {
	t.Helper()
	dir := strings.TrimSpace(os.Getenv(emmyLuaWindowsARM64EvidenceDirEnv))
	if dir == "" {
		dir = filepath.Join(repoRootForMcpLSPBinaryTest(t), ".build-cache", "codex-emmylua-arm64-proof", "windows-arm64-e2e")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Logf("EmmyLua process ledger directory unavailable: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	identities := make([]map[string]any, 0, len(tracked))
	for _, identity := range tracked {
		identities = append(identities, map[string]any{
			"pid":        identity.PID,
			"startToken": identity.StartToken,
			"name":       identity.Name,
			"language":   identity.Language,
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		leftPID, _ := identities[i]["pid"].(int)
		rightPID, _ := identities[j]["pid"].(int)
		if leftPID != rightPID {
			return leftPID < rightPID
		}
		return identities[i]["startToken"].(string) < identities[j]["startToken"].(string)
	})
	payload := map[string]any{
		"schema":       "windows-arm64-emmylua-process-arm64-process-ledger-v2",
		"pid":          pid,
		"startToken":   startToken,
		"identities":   identities,
		"zeroResidual": zeroResidual,
		"lifecycle":    map[string]any{"shutdownResponse": shutdownOK, "exitSent": exitSent, "exitCode": exitCode, "phase": phase, "startedAt": startedAt.UTC().Format(time.RFC3339Nano), "receiptAt": time.Now().UTC().Format(time.RFC3339Nano)},
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Logf("marshal EmmyLua process ledger: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	path := filepath.Join(dir, "windows-arm64-emmylua-process-arm64-process-ledger.json")
	if err := writeEmmyLuaARM64Receipt(path, encoded); err != nil {
		t.Logf("write EmmyLua process ledger: %s", redactEmmyLuaWindowsARM64Text(err.Error()))
		return ""
	}
	t.Logf("EmmyLua Windows ARM64/process ARM64 process receipt=%s receipt_sha256=%s zeroResidual=%t identities=%d", filepath.Base(path), emmyLuaWindowsARM64SHA256(emmyLuaWindowsARM64ReceiptBytes(encoded)), zeroResidual, len(identities))
	return path
}

func writeEmmyLuaARM64Receipt(path string, encoded []byte) error {
	receiptBytes := emmyLuaWindowsARM64ReceiptBytes(encoded)
	if err := os.WriteFile(path, receiptBytes, 0o600); err != nil {
		return err
	}
	digest := emmyLuaWindowsARM64SHA256(receiptBytes)
	return os.WriteFile(path+".sha256", []byte(digest+"\n"), 0o600)
}

func emmyLuaWindowsARM64ReceiptBytes(encoded []byte) []byte {
	return append(append([]byte(nil), encoded...), '\n')
}

func emmyLuaActionCounts(records []emmyLuaARM64ActionRecord) map[string]int {
	counts := map[string]int{
		string(emmyLuaActionSuccess):               0,
		string(emmyLuaActionLegalEmpty):            0,
		string(emmyLuaActionCapabilityUnsupported): 0,
		string(emmyLuaActionNull):                  0,
		string(emmyLuaActionError):                 0,
	}
	for _, record := range records {
		counts[string(record.Status)]++
	}
	return counts
}

func emmyLuaActionClosurePass(records []emmyLuaARM64ActionRecord) bool {
	if len(records) != 36 {
		return false
	}
	for _, record := range records {
		if record.Status == emmyLuaActionNull || record.Status == emmyLuaActionError {
			return false
		}
	}
	return true
}

func slicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
