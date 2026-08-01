//go:build windows

package hiddenexec

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const jobObjectLimitKillOnJobClose = 0x00002000

const windowsStillActive = 259

type processMemoryCounters struct {
	Size                       uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

type windowsProcessTree struct {
	mu           sync.Mutex
	job          windows.Handle
	pid          int
	terminated   bool
	terminateErr error
	released     bool
	releaseErr   error
}

type jobProcessIDListHeader struct {
	numberOfAssignedProcesses uint32
	numberOfProcessIDsInList  uint32
	processIDList             [1]uintptr
}

// startProcessTree 先创建 Job，再以 CREATE_SUSPENDED 启动并绑定子进程，最后恢复唯一初始线程。
// 因为用户代码只会在 AssignProcessToJobObject 成功后执行，子进程没有 Start→Assign 逃逸窗口。
func startProcessTree(cmd *exec.Cmd) (*ProcessTree, error) {
	if cmd == nil {
		return nil, errors.New("start Windows process tree: command is nil")
	}
	if cmd.Process != nil {
		return nil, errors.New("start Windows process tree: command is already started")
	}
	job, err := createKillOnCloseJob()
	if err != nil {
		return nil, err
	}
	if cmd.SysProcAttr == nil {
		configureCommand(cmd)
	}
	cmd.SysProcAttr.CreationFlags |= createSuspended
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(
			fmt.Errorf("start suspended language-server process: %w", err),
			closeWindowsHandle(job, "close unassigned language-server Job Object"),
		)
	}
	owner := &windowsProcessTree{job: job, pid: cmd.Process.Pid}
	if err := assignProcessToJob(owner.job, owner.pid); err != nil {
		cleanupErr := cleanupUnassignedSuspendedProcess(cmd, owner.job)
		return nil, errors.Join(err, cleanupErr)
	}
	if err := resumeUniqueProcessThread(owner.pid); err != nil {
		cleanupErr := cleanupAssignedSuspendedProcess(cmd, owner)
		return nil, errors.Join(fmt.Errorf("resume language-server process: %w", err), cleanupErr)
	}
	return &ProcessTree{controller: owner}, nil
}

func createKillOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create language-server Job Object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: jobObjectLimitKillOnJobClose,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		return 0, errors.Join(
			fmt.Errorf("configure language-server Job Object: %w", err),
			closeWindowsHandle(job, "close unconfigured language-server Job Object"),
		)
	}
	return job, nil
}

func assignProcessToJob(job windows.Handle, pid int) error {
	process, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("open suspended language-server process for Job Object: %w", err)
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	closeErr := closeWindowsHandle(process, "close suspended language-server process handle")
	if assignErr != nil {
		assignErr = fmt.Errorf("assign suspended language-server process to Job Object: %w", assignErr)
	}
	return errors.Join(assignErr, closeErr)
}

// resumeUniqueProcessThread 只恢复 CREATE_SUSPENDED 产生的唯一初始线程；数量异常时拒绝执行。
func resumeUniqueProcessThread(pid int) (retErr error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("snapshot suspended language-server threads: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, closeWindowsHandle(snapshot, "close suspended thread snapshot"))
	}()
	threadIDs, err := processThreadIDs(snapshot, pid)
	if err != nil {
		return err
	}
	if len(threadIDs) != 1 {
		return fmt.Errorf("suspended language-server process %d has %d threads; want exactly 1", pid, len(threadIDs))
	}
	thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, threadIDs[0])
	if err != nil {
		return fmt.Errorf("open suspended language-server initial thread: %w", err)
	}
	previousSuspendCount, resumeErr := windows.ResumeThread(thread)
	closeErr := closeWindowsHandle(thread, "close language-server initial thread handle")
	if resumeErr != nil {
		resumeErr = fmt.Errorf("resume language-server initial thread: %w", resumeErr)
	} else if previousSuspendCount != 1 {
		resumeErr = fmt.Errorf("resume language-server initial thread: previous suspend count = %d, want 1", previousSuspendCount)
	}
	return errors.Join(resumeErr, closeErr)
}

// processThreadIDs 从单次线程快照中枚举目标进程的全部线程，枚举中断时直接失败。
func processThreadIDs(snapshot windows.Handle, pid int) ([]uint32, error) {
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("read first suspended language-server thread: %w", err)
	}
	threadIDs := make([]uint32, 0, 1)
	for {
		if entry.OwnerProcessID == uint32(pid) {
			threadIDs = append(threadIDs, entry.ThreadID)
		}
		err := windows.Thread32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("enumerate suspended language-server threads: %w", err)
		}
	}
	return threadIDs, nil
}

func cleanupUnassignedSuspendedProcess(cmd *exec.Cmd, job windows.Handle) error {
	killErr := cmd.Process.Kill()
	if processTreeProcessGone(killErr) {
		killErr = nil
	}
	waitErr := cleanupProcessWaitError(cmd.Wait())
	return errors.Join(
		killErr,
		waitErr,
		closeWindowsHandle(job, "close unassigned language-server Job Object"),
	)
}

func cleanupAssignedSuspendedProcess(cmd *exec.Cmd, owner *windowsProcessTree) error {
	terminateErr := owner.terminate()
	releaseErr := owner.release()
	waitErr := cleanupProcessWaitError(cmd.Wait())
	return errors.Join(terminateErr, releaseErr, waitErr)
}

func cleanupProcessWaitError(err error) error {
	var exitErr *exec.ExitError
	if err == nil || errors.Is(err, os.ErrProcessDone) || errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func closeWindowsHandle(handle windows.Handle, action string) error {
	if handle == 0 {
		return nil
	}
	if err := windows.CloseHandle(handle); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func (p *windowsProcessTree) terminate() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.terminated {
		return p.terminateErr
	}
	p.terminated = true
	if p.released || p.job == 0 {
		return p.releaseErr
	}
	p.terminateErr = terminateJobProcessTree(p.job)
	return p.terminateErr
}

func (p *windowsProcessTree) release() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return p.releaseErr
	}
	p.releaseErr = closeWindowsHandle(p.job, "close language-server Job Object")
	if p.releaseErr != nil {
		return p.releaseErr
	}
	p.job = 0
	p.released = true
	return nil
}

// rssBytes 仅按当前 Job 成员汇总工作集，拒绝退回不完整的父子 PID 图。
func (p *windowsProcessTree) rssBytes() (uint64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released || p.job == 0 {
		return 0, errors.New("language-server Job Object is released")
	}
	processIDs, err := queryJobProcessIDs(p.job)
	if err != nil {
		return 0, err
	}
	var total uint64
	for _, pid := range processIDs {
		rss, rssErr := windowsProcessRSSBytes(pid)
		if processTreeProcessGone(rssErr) {
			continue
		}
		if rssErr != nil {
			return 0, fmt.Errorf("read Job member %d RSS: %w", pid, rssErr)
		}
		total += rss
	}
	if total == 0 {
		return 0, fmt.Errorf("language-server Job Object for root %d has no readable RSS", p.pid)
	}
	return total, nil
}

// queryJobProcessIDs 读取完整 Job 成员列表；缓冲不足时按内核报告扩容并重试。
func queryJobProcessIDs(job windows.Handle) ([]uint32, error) {
	const initialCapacity = 8
	capacity := initialCapacity
	headerSize := int(unsafe.Offsetof(jobProcessIDListHeader{}.processIDList))
	processIDSize := int(unsafe.Sizeof(uintptr(0)))
	for {
		buffer := make([]byte, headerSize+capacity*processIDSize)
		header := (*jobProcessIDListHeader)(unsafe.Pointer(&buffer[0]))
		err := windows.QueryInformationJobObject(
			job,
			windows.JobObjectBasicProcessIdList,
			uintptr(unsafe.Pointer(&buffer[0])),
			uint32(len(buffer)),
			nil,
		)
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			nextCapacity, capacityErr := nextJobProcessListCapacity(capacity, header.numberOfAssignedProcesses)
			if capacityErr != nil {
				return nil, capacityErr
			}
			capacity = nextCapacity
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("query language-server Job Object members: %w", err)
		}
		assigned := int(header.numberOfAssignedProcesses)
		if assigned > capacity {
			capacity = assigned
			continue
		}
		return decodeJobProcessIDs(buffer, header, headerSize, capacity)
	}
}

// nextJobProcessListCapacity 计算下一次查询容量，并在整数扩容溢出前失败。
func nextJobProcessListCapacity(current int, assigned uint32) (int, error) {
	if uint64(assigned) > uint64(current) {
		return int(assigned), nil
	}
	maxInt := int(^uint(0) >> 1)
	if current > maxInt/2 {
		return 0, fmt.Errorf("grow Job Object member buffer beyond int capacity: current=%d", current)
	}
	return current * 2, nil
}

// decodeJobProcessIDs 校验 Job 成员计数并把本机指针宽度的 PID 列表转成 uint32。
func decodeJobProcessIDs(
	buffer []byte,
	header *jobProcessIDListHeader,
	headerSize int,
	capacity int,
) ([]uint32, error) {
	count := int(header.numberOfProcessIDsInList)
	assigned := int(header.numberOfAssignedProcesses)
	if count > capacity || count > assigned {
		return nil, fmt.Errorf(
			"invalid Job Object member counts: assigned=%d listed=%d capacity=%d",
			header.numberOfAssignedProcesses,
			header.numberOfProcessIDsInList,
			capacity,
		)
	}
	if count < assigned {
		return nil, fmt.Errorf(
			"incomplete Job Object member list: assigned=%d listed=%d capacity=%d",
			assigned,
			count,
			capacity,
		)
	}
	processIDValues := unsafe.Slice(
		(*uintptr)(unsafe.Add(unsafe.Pointer(&buffer[0]), headerSize)),
		count,
	)
	processIDs := make([]uint32, count)
	for i, processID := range processIDValues {
		if processID > uintptr(^uint32(0)) {
			return nil, fmt.Errorf("Job Object member PID %d exceeds uint32", processID)
		}
		processIDs[i] = uint32(processID)
	}
	return processIDs, nil
}

func processAlive(pid int) (alive bool, retErr error) {
	if pid <= 1 {
		return false, nil
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		retErr = errors.Join(retErr, closeWindowsHandle(handle, "close process liveness handle"))
	}()
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, err
	}
	return exitCode == windowsStillActive, nil
}

func processStartIdentity(pid int) (identity string, retErr error) {
	if pid <= 1 {
		return "", errors.New("process PID must be greater than 1")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer func() {
		retErr = errors.Join(retErr, closeWindowsHandle(handle, "close process identity handle"))
	}()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return "", fmt.Errorf("read process creation time: %w", err)
	}
	return strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10), nil
}

func processTreeRSSBytes(int) (uint64, error) {
	return 0, errors.New("Windows process-tree RSS requires an explicit ProcessTree owner")
}

func processRSSBytes(pid int) (uint64, error) {
	if pid <= 1 {
		return 0, errors.New("process PID must be greater than 1")
	}
	return windowsProcessRSSBytes(uint32(pid))
}

func windowsProcessRSSBytes(pid uint32) (rss uint64, retErr error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ,
		false,
		pid,
	)
	if err != nil {
		return 0, err
	}
	defer func() {
		retErr = errors.Join(retErr, closeWindowsHandle(handle, "close process RSS handle"))
	}()
	counters := processMemoryCounters{Size: uint32(unsafe.Sizeof(processMemoryCounters{}))}
	getProcessMemoryInfo := windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
	result, _, callErr := getProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.Size),
	)
	if result == 0 {
		return 0, callErr
	}
	return uint64(counters.WorkingSetSize), nil
}

// killProcessTree 只服务未显式持有 ProcessTree owner 的命令；transport 必须优先调用 owner。
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 1 {
		return errors.New("refusing to kill language-server PID <= 1")
	}
	return taskkillProcessTree(cmd, pid)
}

func terminateJobProcessTree(job windows.Handle) error {
	jobErr := windows.TerminateJobObject(job, 1)
	if processTreeProcessGone(jobErr) {
		return nil
	}
	return fmt.Errorf("terminate language-server Job Object: %w", jobErr)
}

// taskkillProcessTree 为没有 Job Object 的旧命令执行兼容性进程树回收。
func taskkillProcessTree(cmd *exec.Cmd, pid int) error {
	output, err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).CombinedOutput()
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	killErr := cmd.Process.Kill()
	if processTreeProcessGone(killErr) {
		return nil
	}
	return errors.Join(
		fmt.Errorf("taskkill language-server tree: %w: %s", err, strings.TrimSpace(string(output))),
		killErr,
	)
}

func processTreeProcessGone(err error) bool {
	return err == nil ||
		errors.Is(err, os.ErrProcessDone) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_NOT_FOUND)
}
