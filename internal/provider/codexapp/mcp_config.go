package codexapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const (
	managedMCPPrefix     = "mcp-"
	codexConfigFilePerm  = 0o600
	codexConfigServerKey = "mcp_servers"
)

func writeCodexMCPConfig(path string, manifest dto.MCPManifest, cwd string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("codexapp: mcp config path is required")
	}
	managed := collectManagedBinaries(manifest)
	if len(managed) == 0 {
		return nil
	}
	doc, perm, err := readCodexMCPDocument(path)
	if err != nil {
		return err
	}
	servers, err := ensureCodexMCPServers(doc)
	if err != nil {
		return err
	}
	cwd = strings.TrimSpace(cwd)
	// Remove legacy entries that used the "mcp-" prefixed server names
	// (e.g. "mcp-lsp", "mcp-orch"). These are superseded by the short
	// family names ("lsp", "orch") and would otherwise linger without
	// auto_approve, causing every MCP tool call to block on an
	// elicitation/approval request.
	for _, bin := range managed {
		oldName := managedMCPPrefix + bin.Name
		delete(servers, oldName)
	}
	for _, bin := range managed {
		if err := applyManagedMCPServer(servers, bin, cwd); err != nil {
			return err
		}
	}
	return writeCodexMCPDocument(path, doc, perm)
}

func applyManagedMCPServer(servers map[string]any, bin dto.MCPBinary, cwd string) error {
	server, skip, err := ensureManagedMCPServer(servers, bin.Name)
	if err != nil || skip {
		return err
	}
	server["type"] = "stdio"
	server["command"] = strings.TrimSpace(bin.Command[0])
	setServerStringSlice(server, "args", bin.Command[1:])
	setServerString(server, "cwd", cwd)
	setServerStringMap(server, "env", cloneStringMap(bin.Env))
	// Auto-approve all tools for managed MCP servers so the codex CLI does not
	// send elicitation/approval requests that the desktop app cannot handle.
	// Without this, every MCP tool call blocks indefinitely waiting for an
	// approval response that never comes.
	if len(bin.AutoApprove) > 0 {
		setServerStringSlice(server, "auto_approve", bin.AutoApprove)
	}
	return nil
}

func readCodexMCPDocument(path string) (map[string]any, os.FileMode, error) {
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		return map[string]any{}, codexConfigFilePerm, nil
	default:
		return nil, 0, fmt.Errorf("codexapp: read mcp config: %w", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, 0, fmt.Errorf("codexapp: stat mcp config: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, info.Mode().Perm(), nil
	}

	var doc map[string]any
	if _, err := toml.Decode(string(raw), &doc); err != nil {
		return nil, 0, fmt.Errorf("codexapp: decode mcp config: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, info.Mode().Perm(), nil
}

func ensureCodexMCPServers(doc map[string]any) (map[string]any, error) {
	if doc == nil {
		return nil, errors.New("codexapp: mcp config document is nil")
	}
	if raw, ok := doc[codexConfigServerKey]; ok {
		servers, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("codexapp: %s must be a table", codexConfigServerKey)
		}
		return servers, nil
	}
	servers := map[string]any{}
	doc[codexConfigServerKey] = servers
	return servers, nil
}

func ensureManagedMCPServer(servers map[string]any, name string) (map[string]any, bool, error) {
	name = strings.TrimSpace(name)
	if raw, ok := servers[name]; ok {
		server, ok := raw.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("codexapp: mcp server %q must be a table", name)
		}
		command, _ := server["command"].(string)
		if command = strings.TrimSpace(command); command != "" && !isManagedBinary(name, command) {
			return nil, true, nil
		}
		return server, false, nil
	}
	server := map[string]any{}
	servers[name] = server
	return server, false, nil
}

func writeCodexMCPDocument(path string, doc map[string]any, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("codexapp: create mcp config dir: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return fmt.Errorf("codexapp: encode mcp config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*")
	if err != nil {
		return fmt.Errorf("codexapp: create temp mcp config: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("codexapp: write temp mcp config: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("codexapp: chmod temp mcp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("codexapp: close temp mcp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("codexapp: replace mcp config: %w", err)
	}
	return nil
}

// mcpReadyWatcher tracks MCP server startup status events.
// Thread-safe: OnStartupStatus called from read-loop goroutine,
// Wait called from StartSession goroutine.
type mcpReadyWatcher struct {
	expected map[string]bool
	done     chan error
	mu       sync.Mutex
	finished bool
}

func newMCPReadyWatcher(names []string) *mcpReadyWatcher {
	expected := make(map[string]bool, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			expected[name] = false
		}
	}
	return &mcpReadyWatcher{expected: expected, done: make(chan error, 1)}
}

func (w *mcpReadyWatcher) OnStartupStatus(name, status string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return
	}
	if _, tracked := w.expected[name]; !tracked {
		return
	}
	switch strings.TrimSpace(status) {
	case "ready":
		w.expected[name] = true
		if w.allReady() {
			w.finish(nil)
		}
	case "failed", "cancelled":
		w.finish(fmt.Errorf("mcp server %s status: %s", name, status))
	}
}

func (w *mcpReadyWatcher) finish(err error) {
	if w.finished {
		return
	}
	w.finished = true
	select {
	case w.done <- err:
	default:
	}
}

func (w *mcpReadyWatcher) allReady() bool {
	for _, ready := range w.expected {
		if !ready {
			return false
		}
	}
	return true
}

func (w *mcpReadyWatcher) Wait(ctx context.Context) error {
	select {
	case err := <-w.done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("mcp ready timeout after waiting for servers %v: %w", w.pendingNames(), ctx.Err())
	}
}

// pollMCPStatus polls mcpServerStatus/list until all managed servers appear,
// or context is cancelled. Returns nil when all found.
func pollMCPStatus(ctx context.Context, t *transport, names []string, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	needed := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			needed[name] = struct{}{}
		}
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("mcp status poll timeout, still waiting for: %v", pendingKeys(needed))
		case <-ticker.C:
			raw, err := t.Call(ctx, "mcpServerStatus/list", map[string]any{})
			if err != nil {
				continue
			}
			for _, name := range parseMCPStatusNames(raw) {
				delete(needed, name)
			}
			if len(needed) == 0 {
				return nil
			}
		}
	}
}

func parseMCPStatusNames(raw json.RawMessage) []string {
	var resp struct {
		Data []struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	_ = json.Unmarshal(raw, &resp)
	names := make([]string, 0, len(resp.Data))
	for _, item := range resp.Data {
		if name := strings.TrimSpace(item.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func pendingKeys(m map[string]struct{}) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (w *mcpReadyWatcher) pendingNames() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	names := make([]string, 0, len(w.expected))
	for name, ready := range w.expected {
		if !ready {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func isManagedBinary(name, command string) bool {
	name = strings.TrimSpace(name)
	command = strings.TrimSpace(command)
	if name == "" || command == "" {
		return false
	}
	// The binary on disk keeps the "mcp-" prefix (e.g. "mcp-lsp") while the
	// server name in the codex config uses the short family name (e.g. "lsp")
	// to avoid redundant mcp__mcp-lsp__… tool names.
	return filepath.Base(command) == managedMCPPrefix+name
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func setServerString(server map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		server[key] = value
		return
	}
	delete(server, key)
}

func setServerStringSlice(server map[string]any, key string, values []string) {
	if len(values) > 0 {
		server[key] = append([]string(nil), values...)
		return
	}
	delete(server, key)
}

func setServerStringMap(server map[string]any, key string, values map[string]string) {
	if len(values) > 0 {
		server[key] = values
		return
	}
	delete(server, key)
}
