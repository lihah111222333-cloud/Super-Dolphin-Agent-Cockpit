//go:build windows && e2e

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsGoplsTestBreakawayFromJob = 0x01000000
	windowsGoplsTestCreateNoWindow   = 0x08000000
	windowsGoplsTestJobKillOnClose   = 0x00002000
	windowsGoplsTestJobBreakawayOK   = 0x00000800
	windowsFakeGoplsStrictFileURIEnv = "MCP_LSP_FAKE_GOPLS_STRICT_FILE_URI"
)

type windowsGoplsTestInstall struct {
	Root, Binary, Bundle, Manifest, Gopls string
}

type windowsGoplsBrokerRecordV2 struct {
	SchemaVersion         int    `json:"schema_version"`
	ConfigDigest          string `json:"config_digest"`
	Endpoint              string `json:"endpoint"`
	OwnerPID              int    `json:"owner_pid"`
	OwnerStartIdentity    string `json:"owner_start_identity"`
	OwnerExecutablePath   string `json:"owner_executable_path"`
	OwnerSHA256           string `json:"owner_sha256"`
	DaemonPID             int    `json:"daemon_pid"`
	DaemonStartIdentity   string `json:"daemon_start_identity"`
	GoplsExecutablePath   string `json:"gopls_executable_path"`
	GoplsSHA256           string `json:"gopls_sha256"`
	IdleTimeoutNanos      int64  `json:"idle_timeout_nanos"`
	ObservationEndpoint   string `json:"observation_endpoint"`
	ObservationCapability string `json:"observation_capability"`
	ReclaimCapability     string `json:"reclaim_capability"`
}

func startWindowsGoplsMCPBinaryForTest(t *testing.T, ctx context.Context, binary, root, binDir string, env []string) *mcpLSPBinaryClient {
	t.Helper()
	job := createWindowsGoplsTestSidecarJob(t)
	transferred := false
	defer func() {
		if !transferred {
			_ = windows.CloseHandle(job)
		}
	}()
	return startMcpLSPBinaryForTestWithEnvConfigured(
		t, ctx, binary, root, binDir, env,
		func(command *exec.Cmd) {
			command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windowsGoplsTestBreakawayFromJob | windowsGoplsTestCreateNoWindow, HideWindow: true}
		},
		func(command *exec.Cmd) (func() error, error) {
			if err := assignWindowsGoplsTestSidecarJob(job, command); err != nil {
				return nil, err
			}
			transferred = true
			return func() error { return windows.CloseHandle(job) }, nil
		},
	)
}

func createWindowsGoplsTestSidecarJob(t *testing.T) windows.Handle {
	t.Helper()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("create Windows gopls test sidecar Job: %v", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windowsGoplsTestJobKillOnClose | windowsGoplsTestJobBreakawayOK}}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("configure Windows gopls test sidecar Job: %v", err)
	}
	return job
}

func assignWindowsGoplsTestSidecarJob(job windows.Handle, command *exec.Cmd) error {
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	return errors.Join(assignErr, windows.CloseHandle(process))
}

func buildWindowsGoplsTestInstall(t *testing.T) windowsGoplsTestInstall {
	t.Helper()
	root := t.TempDir()
	install := windowsGoplsTestInstall{
		Root:     root,
		Binary:   filepath.Join(root, "bin", "mcp-lsp.exe"),
		Bundle:   filepath.Join(root, "lsp"),
		Manifest: filepath.Join(root, "lsp", "lsp-manifest.json"),
		Gopls:    filepath.Join(root, "lsp", "bin", "gopls.exe"),
	}
	if err := os.MkdirAll(filepath.Dir(install.Binary), 0o700); err != nil {
		t.Fatalf("create Windows broker test bin: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(install.Gopls), 0o700); err != nil {
		t.Fatalf("create Windows broker test LSP bin: %v", err)
	}
	buildWindowsTestExecutable(t, install.Binary, "./cmd/mcp-lsp")
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(windowsFakeGoplsSource), 0o600); err != nil {
		t.Fatalf("write native fake gopls source: %v", err)
	}
	buildWindowsTestExecutable(t, install.Gopls, source)
	writeWindowsGoplsManifest(t, install, windowsFileSHA256(t, install.Gopls))
	return install
}

func installWindowsGoplsTestHost(t *testing.T, target string) {
	t.Helper()
	payload, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatalf("read Windows gopls test host: %v", err)
	}
	if err := os.WriteFile(target, payload, 0o700); err != nil {
		t.Fatalf("install Windows gopls test host: %v", err)
	}
}

func startWindowsGoplsTestHostInJob(t *testing.T, command *exec.Cmd) {
	t.Helper()
	job := createWindowsGoplsTestSidecarJob(t)
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windowsGoplsTestBreakawayFromJob | windowsGoplsTestCreateNoWindow,
		HideWindow:    true,
	}
	if err := command.Start(); err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("start Windows gopls test host: %v", err)
	}
	if err := assignWindowsGoplsTestSidecarJob(job, command); err != nil {
		cleanupErr := errors.Join(command.Process.Kill(), command.Wait(), windows.CloseHandle(job))
		t.Fatalf("assign Windows gopls test host Job: %v", errors.Join(err, cleanupErr))
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(job); err != nil {
			t.Errorf("close Windows gopls test host Job: %v", err)
		}
	})
}

func buildWindowsTestExecutable(t *testing.T, output, target string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-trimpath", "-o", output, target)
	cmd.Dir = repoRootForMcpLSPBinaryTest(t)
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build Windows test executable %s: %v\n%s", output, err, result)
	}
}

func writeWindowsGoplsManifest(t *testing.T, install windowsGoplsTestInstall, digest string) {
	t.Helper()
	manifest := runtimeServerWindowsLSPManifest{
		SchemaVersion: 1,
		BundlePath:    "lsp",
		Profile:       "standard",
		Servers: map[string]runtimeServerWindowsLSPServer{
			"gopls": {
				Path:      "bin/gopls.exe",
				Version:   fakeWindowsGoplsVersion,
				SHA256:    digest,
				Languages: []string{"go", "gomod", "gosum", "gowork"},
			},
		},
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("encode strict Windows LSP manifest: %v", err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(install.Manifest, payload, 0o600); err != nil {
		t.Fatalf("write strict Windows LSP manifest: %v", err)
	}
}

func windowsGoplsSidecarEnv(t *testing.T, install windowsGoplsTestInstall, extra ...string) []string {
	t.Helper()
	env := []string{
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=" + install.Bundle,
		"SUPER_DOLPHIN_LSP_MANIFEST=" + install.Manifest,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(t.TempDir(), "cache"),
		"MCP_LSP_IDLE_TIMEOUT=" + fakeWindowsGoplsIdle.String(),
	}
	return append(env, extra...)
}

func waitForWindowsGoplsBrokerRecord(t *testing.T, cacheRoot string) windowsGoplsBrokerRecordV2 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		paths, err := windowsGoplsBrokerRecordPaths(cacheRoot)
		if err != nil {
			t.Fatalf("find Windows gopls broker record: %v", err)
		}
		if len(paths) == 1 {
			return readWindowsGoplsBrokerRecord(t, paths[0])
		}
		if len(paths) > 1 || time.Now().After(deadline) {
			t.Fatalf("Windows gopls broker records = %v, want exactly one", paths)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func windowsGoplsBrokerRecordPaths(cacheRoot string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(cacheRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && entry.Name() == "daemon.json" {
			paths = append(paths, path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return paths, err
}

func readWindowsGoplsBrokerRecord(t *testing.T, path string) windowsGoplsBrokerRecordV2 {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Windows gopls broker record: %v", err)
	}
	var record windowsGoplsBrokerRecordV2
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		t.Fatalf("decode strict Windows gopls broker record: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("Windows gopls broker record has trailing JSON: %v", err)
	}
	return record
}

func requireWindowsGoplsBrokerRecord(t *testing.T, record windowsGoplsBrokerRecordV2, install windowsGoplsTestInstall, endpoint string, daemonPID int) {
	requireWindowsGoplsBrokerRecordWithIdle(t, record, install, endpoint, daemonPID, fakeWindowsGoplsIdle)
}

func requireWindowsGoplsBrokerRecordWithIdle(t *testing.T, record windowsGoplsBrokerRecordV2, install windowsGoplsTestInstall, endpoint string, daemonPID int, idle time.Duration) {
	t.Helper()
	requireWindowsGoplsBrokerRecordBase(t, record, endpoint, idle)
	requireWindowsGoplsBrokerProcessIdentities(t, record, daemonPID)
	requireWindowsGoplsBrokerExecutablePaths(t, record, install)
	requireWindowsExecutableSHA256(t, install.Binary, record.OwnerSHA256, "broker owner")
	requireWindowsExecutableSHA256(t, install.Gopls, record.GoplsSHA256, "gopls daemon")
}

func requireWindowsGoplsBrokerRecordBase(t *testing.T, record windowsGoplsBrokerRecordV2, endpoint string, idle time.Duration) {
	t.Helper()
	if record.SchemaVersion != runtimeServerWindowsGoplsDaemonSchema || record.ConfigDigest == "" ||
		record.Endpoint != endpoint || record.IdleTimeoutNanos != idle.Nanoseconds() ||
		!runtimeServerWindowsSHA256Valid(record.ObservationCapability) ||
		!runtimeServerWindowsSHA256Valid(record.ReclaimCapability) || record.ObservationCapability == record.ReclaimCapability {
		t.Fatalf("Windows gopls broker record contract mismatch: %+v", record)
	}
}

func requireWindowsGoplsBrokerProcessIdentities(t *testing.T, record windowsGoplsBrokerRecordV2, daemonPID int) {
	t.Helper()
	if record.OwnerPID <= 1 || record.DaemonPID != daemonPID || record.OwnerPID == record.DaemonPID || record.OwnerStartIdentity == "" || record.DaemonStartIdentity == "" {
		t.Fatalf("Windows gopls broker/daemon identity mismatch: %+v", record)
	}
}

func requireWindowsGoplsBrokerExecutablePaths(t *testing.T, record windowsGoplsBrokerRecordV2, install windowsGoplsTestInstall) {
	t.Helper()
	if !strings.EqualFold(filepath.Clean(record.OwnerExecutablePath), filepath.Clean(install.Binary)) {
		t.Fatalf("Windows gopls owner executable = %q, want trusted self %q", record.OwnerExecutablePath, install.Binary)
	}
	if !strings.EqualFold(filepath.Clean(record.GoplsExecutablePath), filepath.Clean(install.Gopls)) {
		t.Fatalf("Windows gopls executable = %q, want native fake %q", record.GoplsExecutablePath, install.Gopls)
	}
}

func requireWindowsExecutableSHA256(t *testing.T, path, got, label string) {
	t.Helper()
	decoded, err := hex.DecodeString(got)
	if err != nil || len(decoded) != sha256.Size || got != strings.ToLower(got) {
		t.Fatalf("%s sha256 = %q, want canonical 64hex: %v", label, got, err)
	}
	if want := windowsFileSHA256(t, path); got != want {
		t.Fatalf("%s sha256 = %s, want actual executable digest %s", label, got, want)
	}
}

func windowsFileSHA256(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read executable for sha256 %s: %v", path, err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

const windowsFakeGoplsSource = `package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type request struct {
	ID json.RawMessage ` + "`json:\"id\"`" + `
	Method string ` + "`json:\"method\"`" + `
	Params json.RawMessage ` + "`json:\"params\"`" + `
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	if err := logInvocation(args); err != nil {
		return err
	}
	if len(args) > 0 && args[0] == "rss-child" {
		return runRSSChild()
	}
	if len(args) > 0 && args[0] == "serve" {
		return runDaemon(args)
	}
	connection, err := connectDaemon(args)
	if err != nil {
		return err
	}
	runErr := runLSP()
	if connection == nil {
		return runErr
	}
	return errors.Join(runErr, connection.Close())
}

func argument(args []string, prefix string) string {
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, prefix); ok {
			return value
		}
	}
	return ""
}

func logInvocation(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(os.Getenv("MCP_LSP_FAKE_GOPLS_ARGS_LOG"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(file, "invocation\t%s pid=%d exe_hex=%s\n", strings.Join(args, " "), os.Getpid(), hex.EncodeToString([]byte(filepath.Clean(executable))))
	return errors.Join(writeErr, file.Close())
}

func runDaemon(args []string) (retErr error) {
	address := argument(args, "-listen=tcp;")
	idle, err := time.ParseDuration(argument(args, "-listen.timeout="))
	if err != nil || address == "" || idle <= 0 {
		return errors.New("fake gopls daemon arguments are invalid")
	}
	child, err := startRSSChild()
	if err != nil {
		return err
	}
	defer func() { retErr = errors.Join(retErr, stopRSSChild(child)) }()
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return err
	}
	tcp, ok := listener.(*net.TCPListener)
	if !ok {
		_ = listener.Close()
		return errors.New("fake gopls listener is not TCP")
	}
	idleSince := time.Now()
	var connections []net.Conn
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for {
		connections, err = retainOpenConnections(connections)
		if err != nil {
			_ = listener.Close()
			return err
		}
		if len(connections) > 0 {
			idleSince = time.Now()
		} else if time.Since(idleSince) >= idle {
			return listener.Close()
		}
		if err := tcp.SetDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
			_ = listener.Close()
			return err
		}
		connection, err := tcp.Accept()
		if err == nil {
			connections = append(connections, connection)
			idleSince = time.Now()
			continue
		}
		if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
			_ = listener.Close()
			return err
		}
	}
}

func startRSSChild() (*exec.Cmd, error) {
	if os.Getenv("MCP_LSP_FAKE_GOPLS_RSS_CHILD") != "1" {
		return nil, nil
	}
	command := exec.Command(os.Args[0], "rss-child")
	command.Env = os.Environ()
	if err := command.Start(); err != nil {
		return nil, err
	}
	return command, nil
}

func stopRSSChild(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	killErr := command.Process.Kill()
	waitErr := command.Wait()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		waitErr = nil
	}
	return errors.Join(killErr, waitErr)
}

func runRSSChild() error {
	payload := make([]byte, 24<<20)
	for index := 0; index < len(payload); index += 4096 {
		payload[index] = 1
	}
	for {
		time.Sleep(time.Second)
		if payload[0] != 1 {
			return errors.New("fake gopls RSS child payload changed")
		}
	}
}

func retainOpenConnections(connections []net.Conn) ([]net.Conn, error) {
	open := connections[:0]
	for _, connection := range connections {
		if err := connection.SetReadDeadline(time.Now().Add(5 * time.Millisecond)); err != nil {
			return nil, err
		}
		var probe [1]byte
		_, err := connection.Read(probe[:])
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			open = append(open, connection)
			continue
		}
		_ = connection.Close()
	}
	return open, nil
}

func connectDaemon(args []string) (net.Conn, error) {
	address := argument(args, "-remote=tcp;")
	if address == "" {
		return nil, nil
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		connection, err := net.DialTimeout("tcp4", address, 100*time.Millisecond)
		if err == nil {
			return connection, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func runLSP() error {
	reader := bufio.NewReader(os.Stdin)
	strictFileURI := os.Getenv("` + windowsFakeGoplsStrictFileURIEnv + `") == "1"
	for {
		body, err := readFrame(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var req request
		if err := json.Unmarshal(body, &req); err != nil {
			return err
		}
		if req.Method == "exit" {
			return nil
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			continue
		}
		if strictFileURI {
			if err := validateWindowsDriveFileURIRequest(req); err != nil {
				if err := writeFrame(map[string]any{
					"jsonrpc": "2.0",
					"id": req.ID,
					"error": map[string]any{"code": -32602, "message": err.Error()},
				}); err != nil {
					return err
				}
				continue
			}
		}
		var result any
		switch req.Method {
		case "initialize":
			capabilities := map[string]any{"textDocumentSync": 1, "completionProvider": map[string]any{}, "hoverProvider": true, "definitionProvider": true, "referencesProvider": true}
			if strictFileURI {
				capabilities["documentSymbolProvider"] = true
			}
			result = map[string]any{"capabilities": capabilities}
		case "textDocument/completion":
			result = map[string]any{"isIncomplete": false, "items": []any{}}
		case "textDocument/hover":
			result = map[string]any{"contents": map[string]any{"kind": "plaintext", "value": "WindowsFakeHover"}}
		case "textDocument/definition", "textDocument/references":
			var params struct {
				TextDocument struct {
					URI string ` + "`json:\"uri\"`" + `
				} ` + "`json:\"textDocument\"`" + `
			}
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return err
			}
			zero := map[string]any{"line": 0, "character": 0}
			result = []any{map[string]any{"uri": params.TextDocument.URI, "range": map[string]any{"start": zero, "end": zero}}}
		case "textDocument/documentSymbol":
			if strictFileURI {
				zero := map[string]any{"line": 0, "character": 0}
				rangeValue := map[string]any{"start": zero, "end": zero}
				result = []any{map[string]any{
					"name": "WindowsDriveURI", "kind": 12,
					"range": rangeValue, "selectionRange": rangeValue,
				}}
			}
		}
		if err := writeFrame(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
}

func validateWindowsDriveFileURIRequest(req request) error {
	var rawURI string
	switch req.Method {
	case "initialize":
		var params struct {
			RootURI string ` + "`json:\"rootUri\"`" + `
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return err
		}
		rawURI = params.RootURI
	case "textDocument/documentSymbol":
		var params struct {
			TextDocument struct {
				URI string ` + "`json:\"uri\"`" + `
			} ` + "`json:\"textDocument\"`" + `
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return err
		}
		rawURI = params.TextDocument.URI
	default:
		return nil
	}
	parsed, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("parse Windows drive file URI: %w", err)
	}
	path := parsed.Path
	if parsed.Scheme != "file" || parsed.Host != "" || len(path) < 4 || path[0] != '/' || path[2] != ':' || path[3] != '/' || !isWindowsDriveLetter(path[1]) {
		return fmt.Errorf("invalid Windows drive file URI: %s", rawURI)
	}
	return nil
}

func isWindowsDriveLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func readFrame(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if length < 0 {
		return nil, errors.New("missing Content-Length")
	}
	body := make([]byte, length)
	_, err := io.ReadFull(reader, body)
	return body, err
}

func writeFrame(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err = os.Stdout.Write(payload)
	return err
}
`
