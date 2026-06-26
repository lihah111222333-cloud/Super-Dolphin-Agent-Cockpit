package codexapp

import (
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// cleanOrphanedAppServersWithProtectedPIDs 清理已经脱离当前进程树的 Codex app-server。
// extraProtectedPIDs 由调用方传入正在使用的 server，避免 sweep 误杀刚分配的进程。
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

// sigtermAppServers 先对孤儿 app-server 发送 SIGTERM。
// 已退出进程不计为错误；其他信号失败只记录告警并跳过后续强杀。
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

// waitForAppServerExit 等待已发送 SIGTERM 的 app-server 自行退出。
// 宽限期结束后仍存活的进程会交给强杀阶段处理，这里不阻塞关闭流程。
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

// sigkillAppServerSurvivors 对 SIGTERM 后仍存活的 app-server 升级强杀。
// 每个 parent 处理完后继续扫 descendants，避免 MCP 子进程留下来占端口或文件句柄。
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

// killProcessDescendants 强制终止指定 root 的已知 descendant 集合。
// root 本身不在这里处理，调用方负责先关闭父进程再清理子树。
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

// killDescendants 清理 app-server 关闭后仍存活的 MCP 子进程。
// 这些进程可能来自外部 MCP server，父进程退出后仍需逐个 kill。
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

// appServerProcessInfo 描述进程表中发现的 Codex app-server。
// PPID 用于判断它是否已经脱离当前宿主进程树。
type appServerProcessInfo struct {
	pid  int
	ppid int
}

// discoverAppServerProcesses 调用平台实现枚举 app-server 进程。
// Unix 侧通过 ps 扫描，Windows 侧由对应实现决定可见范围，调用方只依赖抽象结果。
func discoverAppServerProcesses() (allProcs map[int]int, appServers []appServerProcessInfo) {
	return discoverAppServerProcessList()
}

const appServerKillGracePeriod = 5 * time.Second
