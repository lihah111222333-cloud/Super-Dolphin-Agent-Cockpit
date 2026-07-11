package uistate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	rpcpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestNewUIStateHandlersRegistersProjectRoutes(t *testing.T) {
	t.Parallel()

	_, svc, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	handlers := NewUIStateHandlers(svc).Handlers
	for _, method := range []string{
		"ui/projects/get",
		"ui/projects/setActive",
		"ui/projects/add",
		"ui/projects/remove",
	} {
		if _, ok := handlers[method]; !ok {
			t.Fatalf("Handlers missing %q", method)
		}
	}
}

func TestProjectHandlersDispatchRoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	wantPath := normalizeProjectPath(dir)
	_, svc, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)

	added := dispatchProjectsState(t, server, "ui/projects/add", `{"path":"`+filepath.ToSlash(dir)+`/"}`)
	if len(added.Projects) != 1 || added.Projects[0] != wantPath || added.Active != "." {
		t.Fatalf("add state = %#v", added)
	}

	active := dispatchProjectsState(t, server, "ui/projects/setActive", `{"path":"`+filepath.ToSlash(dir)+`"}`)
	if active.Active != wantPath {
		t.Fatalf("setActive state = %#v", active)
	}

	removed := dispatchProjectsState(t, server, "ui/projects/remove", `{"path":"`+filepath.ToSlash(dir)+`"}`)
	if len(removed.Projects) != 0 || removed.Active != "." {
		t.Fatalf("remove state = %#v", removed)
	}
}

func TestProjectHandlersEncodeEmptyProjectsAsArray(t *testing.T) {
	t.Parallel()

	_, svc, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	server := rpcpkg.NewServer(rpcpkg.Params{Config: &contract.Config{RPCAddr: "127.0.0.1:0"}})
	server.Register(NewUIStateHandlers(svc).Handlers)

	raw, err := server.Dispatch(context.Background(), "ui/projects/get", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/projects/get) error = %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal(ui/projects/get) error = %v", err)
	}
	if string(payload["projects"]) != "[]" {
		t.Fatalf("projects JSON = %s, want [] in %s", payload["projects"], raw)
	}
}

func TestAddProjectDeduplicatesEquivalentPaths(t *testing.T) {
	t.Parallel()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	dir, err := os.MkdirTemp(cwd, ".uistate-project-")
	if err != nil {
		t.Fatalf("MkdirTemp(%q) error = %v", cwd, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	relative, err := filepath.Rel(cwd, dir)
	if err != nil {
		t.Fatalf("filepath.Rel() error = %v", err)
	}
	_, svc, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := svc.AddProject(context.Background(), dir); err != nil {
		t.Fatalf("AddProject(abs) error = %v", err)
	}
	state, err := svc.AddProject(context.Background(), relative)
	if err != nil {
		t.Fatalf("AddProject(rel) error = %v", err)
	}
	if len(state.Projects) != 1 || state.Projects[0] != normalizeProjectPath(dir) {
		t.Fatalf("deduped projects = %#v", state.Projects)
	}
}

func TestAddProjectRejectsMissingDirectory(t *testing.T) {
	t.Parallel()

	_, svc, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := svc.AddProject(context.Background(), missing); err == nil {
		t.Fatal("AddProject(missing) error = nil, want missing directory rejection")
	}
}

func TestAddProjectDeduplicatesSymlinkAlias(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	target := filepath.Join(root, "target")
	aliasPath := filepath.Join(root, "alias")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll(target) error = %v", err)
	}
	if err := os.Symlink(target, aliasPath); err != nil {
		aliasPath = filepath.Join(target, "..", filepath.Base(target))
	}
	_, svc, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if _, err := svc.AddProject(context.Background(), target); err != nil {
		t.Fatalf("AddProject(target) error = %v", err)
	}
	state, err := svc.AddProject(context.Background(), aliasPath)
	if err != nil {
		t.Fatalf("AddProject(aliasPath) error = %v", err)
	}
	if len(state.Projects) != 1 || state.Projects[0] != normalizeProjectPath(target) {
		t.Fatalf("symlink dedupe projects = %#v", state.Projects)
	}
}

func dispatchProjectsState(t *testing.T, server *rpcpkg.Server, method, payload string) ProjectsState {
	t.Helper()

	raw, err := server.Dispatch(context.Background(), method, json.RawMessage(payload))
	if err != nil {
		t.Fatalf("Dispatch(%s) error = %v", method, err)
	}
	var state ProjectsState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", method, err)
	}
	return state
}
