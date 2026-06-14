package codexapp

import (
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

// sigtermAppServers 处理sigtermappservers。
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

// waitForAppServerExit 为app服务端exit等待codexapp provider。
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

// sigkillAppServerSurvivors 处理sigkillapp服务端survivors。
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

// killProcessDescendants 处理kill进程descendants。
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
// killDescendants 处理killdescendants。
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
