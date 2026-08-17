//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func TestRuntimeWindowsMarkdownModuleRootAndVersionGuard(t *testing.T) {
	cohort := t.TempDir()
	productRoot := filepath.Join(cohort, "product")
	moduleRoot := filepath.Join(productRoot, "cache", "lsp-assets", "markdown", "node_modules")
	if err := os.MkdirAll(filepath.Join(moduleRoot, ".bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	packageDir := filepath.Join(moduleRoot, "markdown-it")
	if err := os.MkdirAll(packageDir, 0o700); err != nil {
		t.Fatal(err)
	}
	packageJSON := filepath.Join(packageDir, "package.json")
	if err := os.WriteFile(packageJSON, []byte(`{"version":"14.2.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	serverBinary := filepath.Join(moduleRoot, ".bin", "vscode-markdown-language-server.cmd")
	if err := os.WriteFile(serverBinary, []byte("@echo off\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	gotRoot, err := runtimeMarkdownModuleRoot(serverBinary)
	if err != nil {
		t.Fatalf("runtimeMarkdownModuleRoot() error = %v", err)
	}
	if gotRoot != moduleRoot {
		t.Fatalf("runtimeMarkdownModuleRoot() = %q, want %q", gotRoot, moduleRoot)
	}
	if err := runtimeMarkdownRequireExactPackage(gotRoot); err != nil {
		t.Fatalf("runtimeMarkdownRequireExactPackage() error = %v", err)
	}

	if err := os.WriteFile(packageJSON, []byte(`{"version":"14.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runtimeMarkdownRequireExactPackage(gotRoot); err == nil || !strings.Contains(err.Error(), "14.1.0") {
		t.Fatalf("version mismatch error = %v, want locked-version failure", err)
	}
	if _, err := runtimeMarkdownModuleRoot(filepath.Join(cohort, "bin", "server.cmd")); err == nil {
		t.Fatal("runtimeMarkdownModuleRoot accepted binary outside npm .bin cohort")
	}
}

func TestRuntimeWindowsMarkdownWorkspaceRejectsTraversalAndSymlinkEscape(t *testing.T) {
	workspaceRoot := t.TempDir()
	outsideRoot := t.TempDir()
	workspace, err := newRuntimeMarkdownWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(workspaceRoot, "inside.md")
	if err := os.WriteFile(inside, []byte("# inside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := workspace.pathFromURI(runtimeMarkdownFileURI(inside)); err != nil || got != filepath.Clean(inside) {
		t.Fatalf("inside URI resolved to %q, err=%v", got, err)
	}
	protocol := &runtimeWindowsMarkdownClientProtocol{workspace: workspace}
	request, err := json.Marshal(map[string]string{"uri": runtimeMarkdownFileURI(inside)})
	if err != nil {
		t.Fatal(err)
	}
	readResult, err := protocol.handleReadFile(request)
	if err != nil {
		t.Fatalf("handleReadFile() error = %v", err)
	}
	bytesResult, ok := readResult.([]int)
	if !ok || len(bytesResult) != len("# inside\n") || bytesResult[0] != int('#') {
		t.Fatalf("handleReadFile() = %#v, want official number[] bytes", readResult)
	}
	traversal := filepath.Join(workspaceRoot, "..", filepath.Base(outsideRoot), "outside.md")
	if _, err := workspace.resolvePath(traversal); !errors.Is(err, errRuntimeMarkdownWorkspaceEscape) {
		t.Fatalf("lexical traversal error = %v, want workspace escape", err)
	}

	link := filepath.Join(workspaceRoot, "outside-link")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Logf("symlink escape probe unavailable on this Windows host: %v", err)
		return
	}
	if _, err := workspace.resolvePath(filepath.Join(link, "escape.md")); !errors.Is(err, errRuntimeMarkdownWorkspaceEscape) {
		t.Fatalf("symlink escape error = %v, want workspace escape", err)
	}
}

func TestRuntimeWindowsMarkdownWatcherKindUsesOfficialProtocolKinds(t *testing.T) {
	tests := []struct {
		name string
		op   fsnotify.Op
		want string
	}{
		{name: "create", op: fsnotify.Create, want: "create"},
		{name: "change", op: fsnotify.Write, want: "change"},
		{name: "delete", op: fsnotify.Remove, want: "delete"},
		{name: "rename-delete", op: fsnotify.Rename, want: "delete"},
		{name: "none", op: 0, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := runtimeMarkdownWatcherKind(test.op); got != test.want {
				t.Fatalf("runtimeMarkdownWatcherKind(%v) = %q, want %q", test.op, got, test.want)
			}
		})
	}
	if runtimeMarkdownWatcherOnChangeMethod != "markdown/fs/watcher/onChange" {
		t.Fatalf("watcher change method = %q, want official VS Code method", runtimeMarkdownWatcherOnChangeMethod)
	}
}

func TestRuntimeWindowsMarkdownWatcherParentPathsStayInsideWorkspace(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := newRuntimeMarkdownWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(workspaceRoot, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(nested, "missing.md")
	paths, parentDirs, err := runtimeMarkdownWatcherPaths(workspace, target, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != nested || paths[1] != filepath.Clean(workspaceRoot) {
		t.Fatalf("watchParentDirs paths = %#v, want nested directory then workspace root", paths)
	}
	if _, ok := parentDirs[runtimeMarkdownWatchKey(nested)]; !ok {
		t.Fatalf("watchParentDirs parent set = %#v, want nested parent", parentDirs)
	}
	for _, path := range paths {
		if !runtimeMarkdownWithin(workspace.root, path) {
			t.Fatalf("watch path escaped workspace: %s", path)
		}
	}
}

func TestRuntimeWindowsMarkdownDirectoryWatcherSendsChildChange(t *testing.T) {
	workspaceRoot := t.TempDir()
	directoryTarget := filepath.Join(workspaceRoot, "docs")
	if err := os.Mkdir(directoryTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := newRuntimeMarkdownWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	type watcherMessage struct {
		method string
		params map[string]any
	}
	messages := make(chan watcherMessage, 8)
	protocol := &runtimeWindowsMarkdownClientProtocol{
		workspace: workspace,
		watchers:  make(map[int]*runtimeMarkdownWatcher),
		sender: func(_ context.Context, method string, params any) (json.RawMessage, error) {
			encoded, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				return nil, err
			}
			messages <- watcherMessage{method: method, params: decoded}
			return json.RawMessage("null"), nil
		},
	}
	watcher, err := newRuntimeMarkdownWatcher(protocol, 17, runtimeMarkdownFileURI(directoryTarget), directoryTarget, false, false, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			t.Errorf("close directory watcher: %v", err)
		}
	}()
	child := filepath.Join(directoryTarget, "child.md")
	caseInsensitiveWatcher := &runtimeMarkdownWatcher{
		target:          directoryTarget,
		directoryTarget: true,
		parentDirs:      map[string]struct{}{runtimeMarkdownWatchKey(workspaceRoot): {}},
	}
	if isTarget, isChild, isParent := caseInsensitiveWatcher.matchesEvent(strings.ToUpper(child)); !isTarget || !isChild || isParent {
		t.Fatalf("case-insensitive directory child match = target %v child %v parent %v, want target true/child true/parent false", isTarget, isChild, isParent)
	}
	if isTarget, isChild, isParent := caseInsensitiveWatcher.matchesEvent(strings.ToUpper(directoryTarget)); !isTarget || isChild || isParent {
		t.Fatalf("exact directory target match = target %v child %v parent %v, want target true/child false/parent false", isTarget, isChild, isParent)
	}
	if isTarget, isChild, isParent := caseInsensitiveWatcher.matchesEvent(strings.ToUpper(workspaceRoot)); isTarget || isChild || !isParent {
		t.Fatalf("case-insensitive parent match = target %v child %v parent %v, want target false/child false/parent true", isTarget, isChild, isParent)
	}
	waitMessage := func() watcherMessage {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case message := <-messages:
			return message
		case <-timer.C:
			t.Fatal("directory watcher emitted no official onChange request")
			return watcherMessage{}
		}
	}
	waitKind := func(want string) watcherMessage {
		for {
			message := waitMessage()
			if message.params["kind"] == want {
				return message
			}
		}
	}
	if err := os.WriteFile(child, []byte("# child\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	message := waitMessage()
	if message.method != runtimeMarkdownWatcherOnChangeMethod {
		t.Fatalf("directory child request method = %q, want %q", message.method, runtimeMarkdownWatcherOnChangeMethod)
	}
	if got := message.params["id"]; got != float64(17) {
		t.Fatalf("directory child request id = %#v, want 17", got)
	}
	if got := message.params["uri"]; got != runtimeMarkdownFileURI(directoryTarget) {
		t.Fatalf("directory child request uri = %#v, want %q", got, runtimeMarkdownFileURI(directoryTarget))
	}
	kind, ok := message.params["kind"].(string)
	if !ok || (kind != "create" && kind != "change") {
		t.Fatalf("directory child request kind = %#v, want create/change", message.params["kind"])
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if got := waitKind("delete").params["kind"]; got != "delete" {
		t.Fatalf("directory child delete kind = %#v, want delete", got)
	}
	if err := os.WriteFile(child, []byte("# recreated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := waitKind("create").params["kind"]; got != "create" {
		t.Fatalf("directory child recreate kind = %#v, want create", got)
	}
	if err := os.WriteFile(child, []byte("# recreated\n# changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := waitKind("change").params["kind"]; got != "change" {
		t.Fatalf("directory child post-recreate change kind = %#v, want change", got)
	}
}

func TestRuntimeWindowsMarkdownWatcherReaddsDeletedParent(t *testing.T) {
	workspaceRoot := t.TempDir()
	parent := filepath.Join(workspaceRoot, "level1")
	nested := filepath.Join(parent, "level2")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(nested, "target.md")
	if err := os.WriteFile(target, []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace, err := newRuntimeMarkdownWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	messages := make(chan string, 16)
	protocol := &runtimeWindowsMarkdownClientProtocol{
		workspace: workspace,
		watchers:  make(map[int]*runtimeMarkdownWatcher),
		sender: func(_ context.Context, _ string, params any) (json.RawMessage, error) {
			payload, err := json.Marshal(params)
			if err != nil {
				return nil, err
			}
			var decoded struct {
				Kind string `json:"kind"`
			}
			if err := json.Unmarshal(payload, &decoded); err != nil {
				return nil, err
			}
			messages <- decoded.Kind
			return json.RawMessage("null"), nil
		},
	}
	watcher, err := newRuntimeMarkdownWatcher(protocol, 23, runtimeMarkdownFileURI(target), target, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			t.Errorf("close parent recreation watcher: %v", err)
		}
	}()
	if err := os.RemoveAll(nested); err != nil {
		t.Fatal(err)
	}
	waitKind := func(want string) bool {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case got := <-messages:
				if got == want {
					return true
				}
			case <-timer.C:
				return false
			}
		}
	}
	if !waitKind("delete") {
		t.Fatal("parent deletion emitted no target delete onChange")
	}
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("recreated\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !waitKind("create") {
		t.Fatal("recreated parent/target emitted no target create onChange")
	}
	if !protocol.Healthy() {
		t.Fatal("parent deletion/recreation made watcher unhealthy")
	}
}

func TestRuntimeWindowsMarkdownWatcherRejectsInvalidDeleteAndPropagatesAsyncError(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspace, err := newRuntimeMarkdownWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	protocol := &runtimeWindowsMarkdownClientProtocol{workspace: workspace, watchers: make(map[int]*runtimeMarkdownWatcher)}
	target := filepath.Join(workspaceRoot, "watch.md")
	if err := os.WriteFile(target, []byte("watch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	watchRequest, err := json.Marshal(map[string]any{"id": 9, "uri": runtimeMarkdownFileURI(target)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.handleWatcherCreate(watchRequest); err != nil {
		t.Fatalf("handleWatcherCreate returned error: %v", err)
	}
	if _, err := protocol.handleWatcherCreate(watchRequest); err == nil {
		t.Fatal("handleWatcherCreate accepted duplicate watcher ID")
	}
	if _, err := protocol.handleWatcherDelete(watchRequest); err != nil {
		t.Fatalf("handleWatcherDelete returned error: %v", err)
	}
	if len(protocol.watchers) != 0 {
		t.Fatalf("watcher delete left entries: %#v", protocol.watchers)
	}
	deleteRequest, err := json.Marshal(map[string]int{"id": 9})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.handleWatcherDelete(deleteRequest); err == nil {
		t.Fatal("handleWatcherDelete accepted unknown watcher ID")
	}
	invalidCreate, err := json.Marshal(map[string]any{"id": -1, "uri": runtimeMarkdownFileURI(filepath.Join(workspaceRoot, "x.md"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.handleWatcherCreate(invalidCreate); err == nil {
		t.Fatal("handleWatcherCreate accepted negative watcher ID")
	}

	protocol.recordAsyncError(securefs.NewWindowsPermissionError("watcher", filepath.Join(workspaceRoot, "x.md"), syscall.Errno(1314)))
	if protocol.Healthy() {
		t.Fatal("protocol remained healthy after asynchronous watcher failure")
	}
	if err := protocol.Close(); err == nil {
		t.Fatal("protocol Close lost asynchronous watcher root cause")
	}
}

func TestRuntimeWindowsMarkdownFilesystemErrorPreservesAuthorizationCodes(t *testing.T) {
	for _, code := range []syscall.Errno{5, 1314} {
		t.Run(fmt.Sprintf("win32-%d", code), func(t *testing.T) {
			err := runtimeMarkdownFilesystemError("readFile", `C:\workspace\secret.md`, code)
			var permissionErr *securefs.WindowsPermissionError
			if !errors.As(err, &permissionErr) {
				t.Fatalf("runtimeMarkdownFilesystemError(%d) = %T %v, want typed Windows permission error", code, err, err)
			}
			if permissionErr.Win32Code() != uint32(code) {
				t.Fatalf("typed Windows permission code = %d, want %d", permissionErr.Win32Code(), code)
			}
		})
	}
}
