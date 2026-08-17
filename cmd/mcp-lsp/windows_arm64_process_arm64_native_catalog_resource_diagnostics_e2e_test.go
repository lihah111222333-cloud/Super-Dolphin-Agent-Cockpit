//go:build windows && arm64 && e2e

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// nativeCatalogResourceDiagnosticStatus 明确这是资源诊断辅助文件，不是 15 分钟/540 动作交付证明。
const nativeCatalogResourceDiagnosticStatus = "NON_PASS_DIAGNOSTIC_NOT_LIFECYCLE"

// nativeCatalogResourceSnapshot 只记录资源形状和脱敏身份，避免把路径、URL 或环境内容写入证据。
// 它仅用于 Windows ARM64 E2E 诊断，不参与生产运行时决策，也不改变非 Windows 行为。
type nativeCatalogResourceSnapshot struct {
	PID             int
	Start           string
	HandleCount     uint32
	PrivateBytes    uint64
	WorkingSet      uint64
	Threads         uint32
	TempFiles       int
	ActiveResponses int64
	Children        string
}

type nativeCatalogProcessMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

func nativeCatalog15x36ResourceSnapshot(pid int, root string, activeResponses int64) nativeCatalogResourceSnapshot {
	snapshot := nativeCatalogResourceSnapshot{PID: pid, ActiveResponses: activeResponses}
	if pid > 0 {
		snapshot.Start, _ = windowsGoplsProcessStartIdentity(pid)
		snapshot.HandleCount, snapshot.PrivateBytes, snapshot.WorkingSet = nativeCatalogProcessCounters(pid)
		snapshot.Threads = nativeCatalogThreadCount(pid)
	}
	snapshot.TempFiles = nativeCatalogTempFileCount(root)
	snapshot.Children = nativeCatalogChildShape(pid)
	return snapshot
}

func nativeCatalogProcessCounters(pid int) (uint32, uint64, uint64) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, 0, 0
	}
	defer windows.CloseHandle(handle)
	getHandleCount := syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessHandleCount")
	var handleCount uint32
	_, _, _ = getHandleCount.Call(uintptr(handle), uintptr(unsafe.Pointer(&handleCount)))
	getMemoryInfo := syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	counters := nativeCatalogProcessMemoryCounters{CB: uint32(unsafe.Sizeof(nativeCatalogProcessMemoryCounters{}))}
	_, _, _ = getMemoryInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(&counters)), uintptr(counters.CB))
	return handleCount, uint64(counters.PrivateUsage), uint64(counters.WorkingSetSize)
}

func nativeCatalogThreadCount(pid int) uint32 {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return 0
	}
	var count uint32
	for {
		if entry.OwnerProcessID == uint32(pid) {
			count++
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return count
}

func nativeCatalogChildShape(parentPID int) string {
	if parentPID <= 0 {
		return "none"
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "snapshot_error"
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return "none"
	}
	children := make([]string, 0, 4)
	for {
		if int(entry.ParentProcessID) == parentPID {
			name := windows.UTF16ToString(entry.ExeFile[:])
			lower := strings.ToLower(name)
			if lower == "clangd.exe" || lower == "tar.exe" || lower == "mcp-lsp.exe" {
				start, _ := windowsGoplsProcessStartIdentity(int(entry.ProcessID))
				children = append(children, fmt.Sprintf("%s:%d:%s", lower, entry.ProcessID, start))
			}
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	if len(children) == 0 {
		return "none"
	}
	sort.Strings(children)
	return strings.Join(children, ",")
}

func nativeCatalogTempFileCount(root string) int {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	count := 0
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry == nil || !entry.Type().IsRegular() {
			return walkErr
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".payload-") || strings.HasPrefix(name, ".ready-") || strings.HasSuffix(name, ".tmp") {
			count++
		}
		return nil
	})
	return count
}

func nativeCatalog15x36ResourceSnapshotLine(phase string, snapshot nativeCatalogResourceSnapshot) string {
	return fmt.Sprintf("phase=resource_snapshot;stage=%s;pid=%d;start=%s;handles=%d;private_bytes=%d;working_set=%d;threads=%d;temp_files=%d;active_http_responses=%d;children=%s", phase, snapshot.PID, snapshot.Start, snapshot.HandleCount, snapshot.PrivateBytes, snapshot.WorkingSet, snapshot.Threads, snapshot.TempFiles, snapshot.ActiveResponses, snapshot.Children)
}

func nativeCatalog15x36LogResourceSnapshot(t *testing.T, wirePath, phase string, pid int, root string, activeResponses int64) {
	t.Helper()
	snapshot := nativeCatalog15x36ResourceSnapshot(pid, root, activeResponses)
	line := nativeCatalog15x36ResourceSnapshotLine(phase, snapshot)
	t.Log(line)
	if err := nativeCatalog15x36WriteWire(wirePath, line); err != nil {
		t.Fatalf("write native catalog resource snapshot: %v", err)
	}
}
