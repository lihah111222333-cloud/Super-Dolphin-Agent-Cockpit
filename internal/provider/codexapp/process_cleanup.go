package codexapp

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// mcpBinaryNames lists the sidecar binaries that must be singleton per
// application instance. Regardless of how many agents are running, exactly
// one mcp-orch and one mcp-lsp should exist.
var mcpBinaryNames = []string{"mcp-orch", "mcp-lsp"}

// mcpProcessInfo holds PID and PPID for a discovered MCP sidecar process.
type mcpProcessInfo struct {
	pid    int
	ppid   int
	binary string
}

// cleanOrphanedMCPProcesses finds mcp-orch and mcp-lsp processes that are
// NOT part of the current application process tree. Such processes are
// orphans from a previous run (crash, SIGKILL, run-debug restart, etc.).
//
// Returns the number of processes killed.
func cleanOrphanedMCPProcesses(skipPIDs map[int]struct{}) int {
	// Single ps call: get PID, PPID, COMM for all processes.
	allProcs, mcpProcs := discoverProcesses()
	if len(mcpProcs) == 0 {
		return 0
	}

	myTree := buildProcessTree(os.Getpid(), allProcs)

	killed := 0
	for _, proc := range mcpProcs {
		if proc.pid <= 1 {
			continue
		}
		if len(skipPIDs) > 0 {
			if _, skip := skipPIDs[proc.pid]; skip {
				continue
			}
		}
		// Skip any process that belongs to the current application's
		// process tree: super-agent → codex → mcp-orch/mcp-lsp.
		if _, inTree := myTree[proc.pid]; inTree {
			continue
		}
		if err := killMCPProcess(proc.pid); err != nil {
			pkglogger.Warn("orphan cleanup: kill failed",
				"binary", proc.binary, "pid", proc.pid, "ppid", proc.ppid,
				"error", err)
			continue
		}
		pkglogger.Info("orphan cleanup: killed orphaned MCP process",
			"binary", proc.binary, "pid", proc.pid, "ppid", proc.ppid)
		killed++
	}
	if killed > 0 {
		pkglogger.Info("orphan cleanup: summary", "total_killed", killed)
	}
	return killed
}

// discoverProcesses runs a single `ps -eo pid,ppid,comm` and returns:
//   - allProcs: map[pid]ppid for the entire process table (used for tree building)
//   - mcpProcs: filtered list of mcp-orch/mcp-lsp entries
func discoverProcesses() (allProcs map[int]int, mcpProcs []mcpProcessInfo) {
	out, err := exec.Command("ps", "-eo", "pid,ppid,comm").Output()
	if err != nil {
		pkglogger.Warn("orphan cleanup: ps command failed", "error", err)
		return nil, nil
	}

	allProcs = make(map[int]int, 256)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		allProcs[pid] = ppid

		// Check if this is an MCP binary (need at least 3 fields for comm).
		if len(fields) < 3 {
			continue
		}
		comm := fields[len(fields)-1]
		basename := comm
		if idx := strings.LastIndex(comm, "/"); idx >= 0 {
			basename = comm[idx+1:]
		}
		for _, name := range mcpBinaryNames {
			if basename == name {
				mcpProcs = append(mcpProcs, mcpProcessInfo{
					pid:    pid,
					ppid:   ppid,
					binary: name,
				})
				break
			}
		}
	}
	return allProcs, mcpProcs
}

// buildProcessTree returns all PIDs that are descendants of rootPID
// (including rootPID itself). Uses a children-map + BFS for O(N) traversal.
func buildProcessTree(rootPID int, allProcs map[int]int) map[int]struct{} {
	// Build parent → children adjacency list.
	children := make(map[int][]int, len(allProcs))
	for pid, ppid := range allProcs {
		children[ppid] = append(children[ppid], pid)
	}

	// BFS from rootPID.
	tree := make(map[int]struct{}, 16)
	tree[rootPID] = struct{}{}
	queue := []int{rootPID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, child := range children[current] {
			if _, seen := tree[child]; !seen {
				tree[child] = struct{}{}
				queue = append(queue, child)
			}
		}
	}
	return tree
}

// killMCPProcess terminates a process and its process group.
func killMCPProcess(pid int) error {
	if pid <= 1 {
		return errors.New("refusing to kill PID <= 1")
	}
	// Try process group kill first (catches any children the MCP process spawned).
	pgErr := syscall.Kill(-pid, syscall.SIGKILL)
	if pgErr == nil {
		return nil
	}
	// If process group kill failed (e.g. not a group leader), kill directly.
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil // already dead
	}
	return err
}

// mcpOrphanCleanupGracePeriod is the delay after stopping the codex process
// before scanning for residual MCP sidecars. This gives the codex process
// time to propagate SIGTERM to its MCP children.
const mcpOrphanCleanupGracePeriod = 500 * time.Millisecond
