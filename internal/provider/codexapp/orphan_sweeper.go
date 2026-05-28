package codexapp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func cleanOrphanedAppServersWithProtectedPIDs(extraProtectedPIDs map[int]struct{}) int {
	allProcs, appServerProcs := discoverAppServerProcesses()
	if len(appServerProcs) == 0 {
		return 0
	}

	protected := mergeProtectedPIDs(buildCurrentRuntimeProtectionSet(allProcs), extraProtectedPIDs)
	orphans := filterOrphanAppServers(appServerProcs, protected)
	if len(orphans) == 0 {
		return 0
	}

	sigtermed := sigtermAppServers(orphans)
	waitForAppServerExit(sigtermed)
	killed := sigkillAppServerSurvivors(sigtermed, allProcs)

	if killed > 0 {
		pkglogger.Info("orphan sweeper: summary", "total_killed", killed)
	}
	return killed
}

func filterOrphanAppServers(procs []appServerProcessInfo, myTree map[int]struct{}) []appServerProcessInfo {
	orphans := make([]appServerProcessInfo, 0, len(procs))
	for _, proc := range procs {
		if proc.pid <= 1 {
			continue
		}
		// A process with a live non-init parent belongs to another running
		// application/tool runner, not to stale orphan cleanup.
		if proc.ppid > 1 {
			continue
		}
		if _, inTree := myTree[proc.pid]; !inTree {
			orphans = append(orphans, proc)
		}
	}
	return orphans
}

func sigtermAppServers(orphans []appServerProcessInfo) []appServerProcessInfo {
	sigtermed := make([]appServerProcessInfo, 0, len(orphans))
	for _, proc := range orphans {
		if err := sendSignalToPID(proc.pid, sigTerminate); err != nil {
			if !isProcessGoneErr(err) {
				pkglogger.Warn("orphan sweeper: SIGTERM failed",
					"pid", proc.pid, "ppid", proc.ppid, "error", err)
			}
			continue
		}
		sigtermed = append(sigtermed, proc)
	}
	return sigtermed
}

func waitForAppServerExit(sigtermed []appServerProcessInfo) {
	deadline := time.Now().Add(appServerKillGracePeriod)
	for time.Now().Before(deadline) {
		allGone := true
		for _, proc := range sigtermed {
			if isProcessAlive(proc.pid) {
				allGone = false
				break
			}
		}
		if allGone {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func sigkillAppServerSurvivors(sigtermed []appServerProcessInfo, allProcs map[int]int) int {
	killed := 0
	for _, proc := range sigtermed {
		if !isProcessAlive(proc.pid) {
			pkglogger.Info("orphan sweeper: killed orphaned app-server",
				"pid", proc.pid, "ppid", proc.ppid)
			killed++
		} else if err := killMCPProcess(proc.pid); err != nil {
			pkglogger.Warn("orphan sweeper: kill app-server failed",
				"pid", proc.pid, "ppid", proc.ppid, "error", err)
			continue
		} else {
			pkglogger.Info("orphan sweeper: killed orphaned app-server",
				"pid", proc.pid, "ppid", proc.ppid)
			killed++
		}
		killed += killDescendants(proc, allProcs)
	}
	return killed
}

func snapshotProcessDescendants(rootPID int) map[int]struct{} {
	if rootPID <= 1 {
		return nil
	}
	allProcs, _ := discoverAppServerProcesses()
	if len(allProcs) == 0 {
		return nil
	}
	tree := buildProcessTree(rootPID, allProcs)
	delete(tree, rootPID)
	if len(tree) == 0 {
		return nil
	}
	return tree
}

func killProcessDescendants(rootPID int, descendants map[int]struct{}) int {
	if rootPID <= 1 || len(descendants) == 0 {
		return 0
	}
	killed := 0
	for descPID := range descendants {
		if descPID <= 1 || !isProcessAlive(descPID) {
			continue
		}
		if err := killMCPProcess(descPID); err != nil {
			pkglogger.Warn("codexapp: kill descendant failed",
				"parent_pid", rootPID, "descendant_pid", descPID, "error", err)
			continue
		}
		pkglogger.Info("codexapp: killed app-server descendant",
			"parent_pid", rootPID, "descendant_pid", descPID)
		killed++
	}
	return killed
}

// killDescendants kills remaining descendant processes of an app-server
// (mcp-server-postgres, exa-mcp-server, etc.) that survived the parent shutdown.
func killDescendants(proc appServerProcessInfo, allProcs map[int]int) int {
	killed := 0
	descendants := buildProcessTree(proc.pid, allProcs)
	for descPID := range descendants {
		if descPID == proc.pid || descPID <= 1 {
			continue
		}
		if !isProcessAlive(descPID) {
			continue // already exited with parent
		}
		if err := killMCPProcess(descPID); err != nil {
			pkglogger.Warn("orphan sweeper: kill descendant failed",
				"parent_pid", proc.pid, "descendant_pid", descPID, "error", err)
			continue
		}
		pkglogger.Info("orphan sweeper: killed orphaned descendant",
			"parent_pid", proc.pid, "descendant_pid", descPID)
		killed++
	}
	return killed
}

// appServerProcessInfo holds PID and PPID for a discovered codex app-server process.
type appServerProcessInfo struct {
	pid  int
	ppid int
}

// discoverAppServerProcesses delegates to the platform-specific process
// enumerator (Unix: `ps`, Windows: no-op for Phase 1).
func discoverAppServerProcesses() (allProcs map[int]int, appServers []appServerProcessInfo) {
	return discoverAppServerProcessList()
}

const appServerKillGracePeriod = 5 * time.Second

func (l *execPeerLauncher) peerEnvForTest(name string, parent []string) ([]string, error) {
	var roots []string
	if l != nil && l.workspaceRoots != nil {
		roots = l.workspaceRoots()
	}
	return peerProcessEnv(name, parent, roots)
}

func peerProcessEnv(name string, parent []string, configuredRoots []string) ([]string, error) {
	env := append([]string(nil), parent...)
	env = append(env, peerModeEnv+"=1")
	var err error
	env, err = ensurePeerSessionToken(env)
	if err != nil {
		return nil, err
	}
	env, err = injectPeerBootstrapIdentity(env, name)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) != "mcp-lsp" {
		return env, nil
	}
	if raw, ok := lookupEnvValue(env, "GO_AGENT_LSP_ROOTS"); ok {
		return env, validateMcpLSPPeerWorkspaceRoots(raw)
	}
	if root, ok := lookupEnvValue(env, "GO_AGENT_LSP_ROOT"); ok {
		return env, validateMcpLSPPeerWorkspaceRoot(root)
	}
	roots, err := normalizePeerWorkspaceRoots(configuredRoots)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(roots)
	if err != nil {
		return nil, err
	}
	env = append(env, "GO_AGENT_LSP_ROOT="+roots[0], "GO_AGENT_LSP_ROOTS="+string(raw))
	return env, nil
}

func injectPeerBootstrapIdentity(env []string, name string) ([]string, error) {
	name = strings.TrimSpace(name)
	clientKind, err := managedPeerClientKind(name)
	if err != nil {
		return nil, err
	}
	env = removeEnvKeys(env,
		"GO_AGENT_CTL_INSTANCE_ID", "GO_AGENT_MCP_INSTANCE_ID",
		"GO_AGENT_CTL_BOOT_ID", "GO_AGENT_MCP_BOOT_ID",
		peerBinaryNameEnv, "GO_AGENT_MCP_BINARY_NAME",
		peerClientKindEnv, "GO_AGENT_MCP_CLIENT_KIND",
		"GO_AGENT_CTL_AGENT_ID", "GO_AGENT_MCP_AGENT_ID",
		"GO_AGENT_CTL_THREAD_ID", "GO_AGENT_MCP_THREAD_ID",
		peerBootstrapJSONEnv, "GO_AGENT_MCP_BOOT_CONTEXT",
	)
	boot, err := json.Marshal(map[string]string{
		"binary_name": name,
		"client_kind": clientKind,
	})
	if err != nil {
		return nil, err
	}
	return append(env,
		peerBinaryNameEnv+"="+name,
		peerClientKindEnv+"="+clientKind,
		peerBootstrapJSONEnv+"="+string(boot),
	), nil
}

func managedPeerClientKind(name string) (string, error) {
	switch strings.TrimSpace(name) {
	case "mcp-orch":
		return "orch", nil
	case "mcp-lsp":
		return "lsp", nil
	default:
		return "", errors.New("peer process client kind is not configured for " + name)
	}
}

func removeEnvKeys(env []string, keys ...string) []string {
	drop := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		drop[strings.ToUpper(key)] = struct{}{}
	}
	out := env[:0]
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			out = append(out, item)
			continue
		}
		if _, ok := drop[strings.ToUpper(key)]; ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func ensurePeerSessionToken(env []string) ([]string, error) {
	if _, ok := lookupTrimmedEnvValue(env, "GO_AGENT_CTL_SESSION_TOKEN"); ok {
		return env, nil
	}
	if token, ok := lookupTrimmedEnvValue(env, "GO_AGENT_MCP_SESSION_TOKEN"); ok {
		return append(env, "GO_AGENT_CTL_SESSION_TOKEN="+token), nil
	}
	return nil, errors.New("peer process requires GO_AGENT_CTL_SESSION_TOKEN or GO_AGENT_MCP_SESSION_TOKEN")
}

func validateMcpLSPPeerWorkspaceRoot(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return errors.New("mcp-lsp peer requires non-empty GO_AGENT_LSP_ROOT workspace root")
	}
	if !filepath.IsAbs(root) {
		return errors.New("mcp-lsp peer GO_AGENT_LSP_ROOT workspace root must be absolute")
	}
	return nil
}

func validateMcpLSPPeerWorkspaceRoots(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return errors.New("mcp-lsp peer requires non-empty GO_AGENT_LSP_ROOTS")
	}
	var roots []string
	if err := json.Unmarshal([]byte(raw), &roots); err != nil {
		return errors.New("mcp-lsp peer GO_AGENT_LSP_ROOTS must be a JSON array: " + err.Error())
	}
	if len(roots) == 0 || strings.TrimSpace(roots[0]) == "" {
		return errors.New("mcp-lsp peer requires non-empty GO_AGENT_LSP_ROOTS")
	}
	if !filepath.IsAbs(strings.TrimSpace(roots[0])) {
		return errors.New("mcp-lsp peer GO_AGENT_LSP_ROOTS primary root must be absolute")
	}
	return nil
}

func normalizePeerWorkspaceRoots(roots []string) ([]string, error) {
	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			return nil, errors.New("mcp-lsp peer configured workspace root must be absolute")
		}
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		out = append(out, root)
	}
	if len(out) == 0 {
		return nil, errors.New("mcp-lsp peer requires configured workspace root")
	}
	return out, nil
}

func lookupEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if value, ok := strings.CutPrefix(env[i], prefix); ok {
			return value, true
		}
	}
	return "", false
}

func lookupTrimmedEnvValue(env []string, key string) (string, bool) {
	value, ok := lookupEnvValue(env, key)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

// resolvePeerBinDirs returns the ordered list of directories to probe for peer
// binaries. GO_AGENT_PEER_BIN_DIR (path-list) wins over os.Executable()'s dir.
func resolvePeerBinDirs() ([]string, error) {
	var dirs []string
	if override := strings.TrimSpace(os.Getenv(peerBinDirEnv)); override != "" {
		for _, part := range filepath.SplitList(override) {
			if p := strings.TrimSpace(part); p != "" {
				dirs = append(dirs, p)
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	dirs = append(dirs, filepath.Dir(exe))
	return dirs, nil
}

func findPeerBinary(dirs []string, name string) (string, bool) {
	candidates := []string{name}
	if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
		candidates = []string{name + ".exe", name}
	}
	for _, dir := range dirs {
		for _, leaf := range candidates {
			candidate := filepath.Join(dir, leaf)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, true
			}
		}
	}
	return "", false
}
