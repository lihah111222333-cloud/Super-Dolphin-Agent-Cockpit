package pidregistry

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	// filePrefix 和 fileSuffix 定义 PID registry 文件命名。
	filePrefix = "super-agent-pids-"
	fileSuffix = ".json"
	// registryFilePerm 限制 PID registry 只允许当前用户读写。
	registryFilePerm = 0o600
	// orphanKillGrace 是 SIGTERM 后等待进程自行退出的时间。
	orphanKillGrace = 3 * time.Second
)

// registryDir 返回 PID registry 文件所在目录。
// Unix 下通常是 /tmp，重启会自然清理；Windows 下使用当前用户的临时目录。
func registryDir() string {
	return os.TempDir()
}

// ChildInfo 描述一个由当前应用登记的子进程。
type ChildInfo struct {
	PID       int               `json:"pid"`
	Kind      string            `json:"kind"`
	StartedAt string            `json:"started_at"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// registryFile 是 PID registry 的磁盘 JSON 结构。
type registryFile struct {
	AppPID                      int         `json:"app_pid"`
	Nonce                       string      `json:"nonce"`
	CreatedAt                   string      `json:"created_at"`
	ParentExecutableFingerprint string      `json:"parent_executable_fingerprint"`
	Children                    []ChildInfo `json:"children"`
}

// Registry 跟踪子进程 PID，用于应用异常退出后的清理。
// 每个应用进程独占一个以 app PID 命名的 registry 文件。
type Registry struct {
	mu                          sync.Mutex
	appPID                      int
	path                        string
	nonce                       string
	createdAt                   string
	parentExecutableFingerprint string
	children                    map[int]ChildInfo
}

// New 为当前进程创建 PID registry。
func New() *Registry {
	pid := os.Getpid()
	return &Registry{
		appPID:   pid,
		path:     registryPath(pid),
		children: make(map[int]ChildInfo),
	}
}

// Register 登记子进程并立即持久化，确保崩溃后仍可回收。
// 旧调用面保留无返回值；需要 fail-fast 的启动路径必须使用 RegisterChecked。
func (r *Registry) Register(pid int, kind string, meta map[string]string) {
	_ = r.RegisterChecked(pid, kind, meta)
}

// RegisterChecked 登记子进程并返回持久化错误。
// 持久化失败会回滚本次登记，调用方必须停止刚启动的子进程。
func (r *Registry) RegisterChecked(pid int, kind string, meta map[string]string) error {
	if r == nil || pid <= 1 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.children[pid] = ChildInfo{
		PID:       pid,
		Kind:      kind,
		StartedAt: time.Now().Format(time.RFC3339),
		Meta:      meta,
	}
	if err := r.persist(); err != nil {
		delete(r.children, pid)
		return err
	}
	return nil
}

// Unregister 在子进程正常退出时移除登记并持久化。
func (r *Registry) Unregister(pid int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.children, pid)
	_ = r.persist()
}

// Close 删除当前应用的 registry 文件，表示正常关闭无需后续清理。
func (r *Registry) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = os.Remove(r.path)
	_ = os.Remove(r.path + ".tmp")
}

// staleOrphan 是从过期 registry 文件中发现的仍存活子进程。
type staleOrphan struct {
	pid  int
	kind string
}

// CleanupStale 清理死亡应用遗留的子进程，并返回最终确认退出的数量。
// 流程先发送 SIGTERM，再等待固定 grace，最后强制结束仍存活的进程。
func CleanupStale() int {
	return CleanupStaleWithProtectedPIDs(nil)
}

// CleanupStaleWithProtectedPIDs 清理过期 registry，同时跳过受保护 PID。
// 调用方应传入当前 runtime 进程树和祖先进程，避免误杀正在执行清理的活跃 runtime。
func CleanupStaleWithProtectedPIDs(protectedPIDs map[int]struct{}) int {
	staleFiles := findStaleRegistryFiles()
	if len(staleFiles) == 0 {
		return 0
	}

	orphans := collectStaleOrphans(staleFiles, protectedPIDs)
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

// collectStaleOrphans 从过期 registry 文件中收集仍存活且未受保护的子进程。
func collectStaleOrphans(staleFiles []staleFile, protectedPIDs map[int]struct{}) []staleOrphan {
	var orphans []staleOrphan
	for _, sf := range staleFiles {
		for _, child := range sf.Children {
			if child.PID <= 1 || !isProcessAlive(child.PID) {
				continue
			}
			if _, protected := protectedPIDs[child.PID]; protected {
				continue
			}
			orphans = append(orphans, staleOrphan{pid: child.PID, kind: child.Kind})
		}
	}
	return orphans
}

// sigtermOrphans 向孤儿进程发送 SIGTERM 或平台等价信号，并返回成功发信号的进程。
func sigtermOrphans(orphans []staleOrphan) []staleOrphan {
	sigtermed := make([]staleOrphan, 0, len(orphans))
	for _, o := range orphans {
		if err := sendSIGTERM(o.pid); err != nil {
			if !isNoSuchProcessErr(err) {
				pkglogger.Warn("pidregistry: SIGTERM failed",
					"pid", o.pid, "kind", o.kind, "error", err)
			}
			continue
		}
		sigtermed = append(sigtermed, o)
	}
	return sigtermed
}

// waitForOrphanExit 轮询等待已发 SIGTERM 的进程退出，直到 grace 到期。
func waitForOrphanExit(sigtermed []staleOrphan) {
	deadline := time.Now().Add(orphanKillGrace)
	for time.Now().Before(deadline) {
		if allProcessesGone(sigtermed) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// allProcessesGone 判断所有候选进程是否都已经退出。
func allProcessesGone(orphans []staleOrphan) bool {
	for _, o := range orphans {
		if isProcessAlive(o.pid) {
			return false
		}
	}
	return true
}

// sigkillSurvivors 强制结束 grace 后仍存活的进程，并返回确认退出数量。
func sigkillSurvivors(sigtermed []staleOrphan) int {
	killed := 0
	for _, o := range sigtermed {
		if !isProcessAlive(o.pid) {
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

// persist 通过临时文件加 rename 原子写入 registry。
// 写入失败必须返回给调用方，不能让新子进程在无 registry 保护下继续运行。
func (r *Registry) persist() error {
	if err := r.ensureProvenance(); err != nil {
		err = fmt.Errorf("pidregistry: prepare registry provenance: %w", err)
		pkglogger.Warn("pidregistry: provenance failed", "error", err)
		return err
	}
	data := registryFile{
		AppPID:                      r.appPID,
		Nonce:                       r.nonce,
		CreatedAt:                   r.createdAt,
		ParentExecutableFingerprint: r.parentExecutableFingerprint,
		Children:                    make([]ChildInfo, 0, len(r.children)),
	}
	for _, child := range r.children {
		data.Children = append(data.Children, child)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		err = fmt.Errorf("pidregistry: marshal registry: %w", err)
		pkglogger.Warn("pidregistry: marshal failed", "error", err)
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, raw, registryFilePerm); err != nil {
		err = fmt.Errorf("pidregistry: write registry: %w", err)
		pkglogger.Warn("pidregistry: write failed", "error", err)
		return err
	}
	if err := os.Chmod(tmp, registryFilePerm); err != nil {
		err = fmt.Errorf("pidregistry: chmod registry: %w", err)
		pkglogger.Warn("pidregistry: chmod failed", "error", err)
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, r.path); err != nil {
		err = fmt.Errorf("pidregistry: rename registry: %w", err)
		pkglogger.Warn("pidregistry: rename failed", "error", err)
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ensureProvenance 为本进程 registry 初始化只写一次的来源元数据。
func (r *Registry) ensureProvenance() error {
	if strings.TrimSpace(r.nonce) == "" {
		nonce, err := newRegistryNonce()
		if err != nil {
			return err
		}
		r.nonce = nonce
	}
	if strings.TrimSpace(r.createdAt) == "" {
		r.createdAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if strings.TrimSpace(r.parentExecutableFingerprint) == "" {
		fingerprint, err := executableFingerprint()
		if err != nil {
			return err
		}
		r.parentExecutableFingerprint = fingerprint
	}
	return nil
}

// newRegistryNonce 生成写入 registry 文件的随机 nonce。
func newRegistryNonce() (string, error) {
	var nonce [16]byte
	if _, err := cryptorand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return hex.EncodeToString(nonce[:]), nil
}

// executableFingerprint 计算当前可执行文件内容的 SHA-256 指纹。
func executableFingerprint() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	file, err := os.Open(exe)
	if err != nil {
		return "", fmt.Errorf("open executable: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash executable: %w", err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// registryPath 返回指定 app PID 对应的 registry 文件路径。
func registryPath(appPID int) string {
	return filepath.Join(registryDir(), filePrefix+strconv.Itoa(appPID)+fileSuffix)
}

// staleFile 是过期 registry 文件及其已解析内容。
type staleFile struct {
	path string
	registryFile
}

// findStaleRegistryFiles 查找 app 进程已死亡的 registry 文件。
func findStaleRegistryFiles() []staleFile {
	pattern := filepath.Join(registryDir(), filePrefix+"*"+fileSuffix)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}

	myPID := os.Getpid()
	var stale []staleFile
	for _, path := range matches {
		if sf, ok := readTrustedStaleRegistryFile(path, myPID); ok {
			stale = append(stale, sf)
		}
	}
	return stale
}

// readTrustedStaleRegistryFile 读取并验证单个 registry 文件是否可信且已过期。
func readTrustedStaleRegistryFile(path string, myPID int) (staleFile, bool) {
	info, err := os.Stat(path)
	if err != nil || !trustedRegistryFileInfo(path, info) {
		return staleFile{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return staleFile{}, false
	}
	var rf registryFile
	if err := json.Unmarshal(data, &rf); err != nil {
		// 损坏的 registry 文件无法恢复，直接删除以免后续反复扫描。
		_ = os.Remove(path)
		return staleFile{}, false
	}
	if !trustedRegistryFileProvenance(path, rf, myPID) {
		return staleFile{}, false
	}
	return staleFile{path: path, registryFile: rf}, true
}

// trustedRegistryFileProvenance 校验 registry JSON 与文件名、nonce 和活跃 AppPID 一致性。
func trustedRegistryFileProvenance(path string, rf registryFile, myPID int) bool {
	filenamePID, ok := ParsePIDFromFilename(filepath.Base(path))
	if !ok || filenamePID != rf.AppPID {
		return false
	}
	if strings.TrimSpace(rf.Nonce) == "" {
		return false
	}
	if rf.AppPID == myPID {
		return false
	}
	return !isProcessAlive(rf.AppPID)
}

// trustedRegistryFileInfo 校验 registry 文件权限和 owner 是否可信。
func trustedRegistryFileInfo(path string, info os.FileInfo) bool {
	if info.IsDir() {
		return false
	}
	if info.Mode().Perm()&0o022 != 0 {
		return false
	}
	return registryFileOwnedByCurrentUser(path, info)
}

// cleanupStaleFiles 删除已经处理完的过期 registry 文件。
func cleanupStaleFiles(files []staleFile) {
	for _, sf := range files {
		_ = os.Remove(sf.path)
	}
}

// RegistryFilesMatchingKind 从过期 registry 文件中返回指定 kind 的 PID。
// 这是 orphan sweeper 兼容旧 app-server 追踪方式的补充入口。
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

// HasStaleFiles 判断是否存在死亡应用留下的 PID registry 文件。
func HasStaleFiles() bool {
	return len(findStaleRegistryFiles()) > 0
}

// StaleChildCount 统计所有过期 registry 文件中仍存活的子进程数量。
func StaleChildCount() int {
	staleFiles := findStaleRegistryFiles()
	count := 0
	for _, sf := range staleFiles {
		for _, child := range sf.Children {
			if child.PID > 1 && isProcessAlive(child.PID) {
				count++
			}
		}
	}
	return count
}

// StaleAppPIDs 返回过期 registry 对应的死亡 app PID，用于日志诊断。
func StaleAppPIDs() []int {
	staleFiles := findStaleRegistryFiles()
	pids := make([]int, 0, len(staleFiles))
	for _, sf := range staleFiles {
		pids = append(pids, sf.AppPID)
	}
	return pids
}

// ParsePIDFromFilename 从 registry 文件名解析 app PID。
func ParsePIDFromFilename(name string) (int, bool) {
	name = filepath.Base(name)
	if !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
		return 0, false
	}
	pidStr := name[len(filePrefix) : len(name)-len(fileSuffix)]
	pid, err := strconv.Atoi(pidStr)
	return pid, err == nil && pid > 0
}
