package pidregistry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	// filePrefix is the naming convention for PID registry files.
	filePrefix = "super-agent-pids-"
	fileSuffix = ".json"
	// registryDir is where PID files are stored. /tmp is cleaned on reboot
	// which provides a natural safety net for very stale files.
	registryDir = "/tmp"
	// orphanKillGrace is how long to wait after SIGTERM before SIGKILL.
	orphanKillGrace = 3 * time.Second
)

// ChildInfo describes a subprocess registered with the PID registry.
type ChildInfo struct {
	PID       int               `json:"pid"`
	Kind      string            `json:"kind"`
	StartedAt string            `json:"started_at"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// registryFile is the on-disk JSON structure.
type registryFile struct {
	AppPID   int         `json:"app_pid"`
	Children []ChildInfo `json:"children"`
}

// Registry tracks subprocess PIDs for crash-safe cleanup.
// Each application instance gets its own file keyed by app PID.
type Registry struct {
	mu     sync.Mutex
	appPID int
	path   string
	children map[int]ChildInfo
}

// New creates a new PID registry for the current process.
func New() *Registry {
	pid := os.Getpid()
	return &Registry{
		appPID:   pid,
		path:     registryPath(pid),
		children: make(map[int]ChildInfo),
	}
}

// Register adds a child process to the registry and persists to disk.
func (r *Registry) Register(pid int, kind string, meta map[string]string) {
	if r == nil || pid <= 1 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.children[pid] = ChildInfo{
		PID:       pid,
		Kind:      kind,
		StartedAt: time.Now().Format(time.RFC3339),
		Meta:      meta,
	}
	r.persist()
}

// Unregister removes a child process from the registry (normal exit).
func (r *Registry) Unregister(pid int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.children, pid)
	r.persist()
}

// Close removes the registry file. Called during normal shutdown.
func (r *Registry) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = os.Remove(r.path)
	_ = os.Remove(r.path + ".tmp")
}

// staleOrphan describes a process registered in a stale PID registry file.
type staleOrphan struct {
	pid  int
	kind string
}

// CleanupStale finds PID registry files from dead app instances and kills
// their registered children. Returns the total number of processes killed.
//
// All orphaned processes are SIGTERM'd concurrently, then we wait once for
// the grace period before SIGKILL'ing survivors.
func CleanupStale() int {
	staleFiles := findStaleRegistryFiles()
	if len(staleFiles) == 0 {
		return 0
	}

	orphans := collectStaleOrphans(staleFiles)
	if len(orphans) == 0 {
		cleanupStaleFiles(staleFiles)
		return 0
	}

	sigtermed := sigtermOrphans(orphans)
	waitForOrphanExit(sigtermed)
	killed := sigkillSurvivors(sigtermed)

	cleanupStaleFiles(staleFiles)
	if killed > 0 {
		pkglogger.Info("pidregistry: stale cleanup summary", "total_killed", killed)
	}
	return killed
}

// collectStaleOrphans gathers alive PIDs from stale registry files.
func collectStaleOrphans(staleFiles []staleFile) []staleOrphan {
	var orphans []staleOrphan
	for _, sf := range staleFiles {
		for _, child := range sf.Children {
			if child.PID > 1 && syscall.Kill(child.PID, 0) == nil {
				orphans = append(orphans, staleOrphan{pid: child.PID, kind: child.Kind})
			}
		}
	}
	return orphans
}

// sigtermOrphans sends SIGTERM to all orphans and returns those successfully signalled.
func sigtermOrphans(orphans []staleOrphan) []staleOrphan {
	sigtermed := make([]staleOrphan, 0, len(orphans))
	for _, o := range orphans {
		if err := syscall.Kill(o.pid, syscall.SIGTERM); err != nil {
			if err != syscall.ESRCH {
				pkglogger.Warn("pidregistry: SIGTERM failed",
					"pid", o.pid, "kind", o.kind, "error", err)
			}
			continue
		}
		sigtermed = append(sigtermed, o)
	}
	return sigtermed
}

// waitForOrphanExit polls until all sigtermed processes have exited or the grace period elapses.
func waitForOrphanExit(sigtermed []staleOrphan) {
	deadline := time.Now().Add(orphanKillGrace)
	for time.Now().Before(deadline) {
		if allProcessesGone(sigtermed) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func allProcessesGone(orphans []staleOrphan) bool {
	for _, o := range orphans {
		if syscall.Kill(o.pid, 0) == nil {
			return false
		}
	}
	return true
}

// sigkillSurvivors force-kills any processes still alive after the grace period.
func sigkillSurvivors(sigtermed []staleOrphan) int {
	killed := 0
	for _, o := range sigtermed {
		if syscall.Kill(o.pid, 0) != nil {
			pkglogger.Info("pidregistry: killed orphaned process",
				"pid", o.pid, "kind", o.kind)
			killed++
			continue
		}
		if err := forceKill(o.pid); err != nil {
			pkglogger.Warn("pidregistry: force kill failed",
				"pid", o.pid, "kind", o.kind, "error", err)
			continue
		}
		pkglogger.Info("pidregistry: force-killed orphaned process",
			"pid", o.pid, "kind", o.kind)
		killed++
	}
	return killed
}

// persist writes the registry to disk atomically (write-to-tmp + rename).
func (r *Registry) persist() {
	data := registryFile{
		AppPID:   r.appPID,
		Children: make([]ChildInfo, 0, len(r.children)),
	}
	for _, child := range r.children {
		data.Children = append(data.Children, child)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		pkglogger.Warn("pidregistry: marshal failed", "error", err)
		return
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		pkglogger.Warn("pidregistry: write failed", "error", err)
		return
	}
	if err := os.Rename(tmp, r.path); err != nil {
		pkglogger.Warn("pidregistry: rename failed", "error", err)
		_ = os.Remove(tmp)
	}
}

func registryPath(appPID int) string {
	return filepath.Join(registryDir, filePrefix+strconv.Itoa(appPID)+fileSuffix)
}

type staleFile struct {
	path     string
	registryFile
}

func findStaleRegistryFiles() []staleFile {
	pattern := filepath.Join(registryDir, filePrefix+"*"+fileSuffix)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	myPID := os.Getpid()
	var stale []staleFile
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rf registryFile
		if err := json.Unmarshal(data, &rf); err != nil {
			// Corrupt file — remove it.
			_ = os.Remove(path)
			continue
		}
		// Skip our own file.
		if rf.AppPID == myPID {
			continue
		}
		// Check if the app process is still alive.
		if err := syscall.Kill(rf.AppPID, 0); err == nil {
			continue // app is still running, don't touch it
		}
		stale = append(stale, staleFile{path: path, registryFile: rf})
	}
	return stale
}

func cleanupStaleFiles(files []staleFile) {
	for _, sf := range files {
		_ = os.Remove(sf.path)
	}
}

func forceKill(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("refusing to kill PID <= 1")
	}
	// Try process group first.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	err := syscall.Kill(pid, syscall.SIGKILL)
	if err == syscall.ESRCH {
		return nil
	}
	return err
}

// RegistryFilesMatchingKind reads all stale files and returns PIDs of a given kind.
// This is used as a fallback by the orphan sweeper for backwards compatibility
// with codex app-server processes that weren't tracked by the registry.
func RegistryFilesMatchingKind(kind string) []int {
	staleFiles := findStaleRegistryFiles()
	var pids []int
	for _, sf := range staleFiles {
		for _, child := range sf.Children {
			if child.Kind == kind && child.PID > 1 {
				pids = append(pids, child.PID)
			}
		}
	}
	return pids
}

// HasStaleFiles returns true if there are PID registry files from dead
// app instances. Used to decide whether to use registry-based cleanup or
// fall back to the legacy ps-scan approach.
func HasStaleFiles() bool {
	return len(findStaleRegistryFiles()) > 0
}

// StaleChildCount counts total alive children across all stale registry files.
func StaleChildCount() int {
	staleFiles := findStaleRegistryFiles()
	count := 0
	for _, sf := range staleFiles {
		for _, child := range sf.Children {
			if child.PID > 1 {
				if err := syscall.Kill(child.PID, 0); err == nil {
					count++
				}
			}
		}
	}
	return count
}

// StaleAppPIDs returns the app PIDs of dead registry files (for logging).
func StaleAppPIDs() []int {
	staleFiles := findStaleRegistryFiles()
	pids := make([]int, 0, len(staleFiles))
	for _, sf := range staleFiles {
		pids = append(pids, sf.AppPID)
	}
	return pids
}

// ParsePIDFromFilename extracts the app PID from a registry filename.
func ParsePIDFromFilename(name string) (int, bool) {
	name = filepath.Base(name)
	if !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
		return 0, false
	}
	pidStr := name[len(filePrefix) : len(name)-len(fileSuffix)]
	pid, err := strconv.Atoi(pidStr)
	return pid, err == nil && pid > 0
}
