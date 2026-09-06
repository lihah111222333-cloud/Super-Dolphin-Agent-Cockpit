package sqliteauthority

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// authorityPathIdentity pins the database and any existing SQLite sidecars to
// the file objects observed while the authority lease was acquired.  SQLite
// may create a previously absent WAL/SHM file during its first write, so an
// absent sidecar is intentionally an open set; an already existing sidecar is
// never allowed to change inode while the lease is held.
type authorityPathIdentity struct {
	main authorityFileIdentity
	wal  authorityFileIdentity
	shm  authorityFileIdentity
}

type authorityFileIdentity struct {
	present bool
	info    os.FileInfo
}

// validateIndependentPaths 校验 authority 私有文件系统；通用模式额外拒绝
// 与产品 SQLite/工作区共享物理身份，ExternalService 模式不触碰这些路径。
func validateIndependentPaths(config Config) (string, error) {
	if err := rejectAuthorityLeafSymlink(config.Path); err != nil {
		return "", err
	}
	if config.ExternalService {
		return validateExternalAuthorityPath(config.Path)
	}
	return validateGenericAuthorityPaths(config)
}

// validateExternalAuthorityPath 校验 standalone RecoveryCustody 的物理路径，
// 在获取目录锁前拒绝哨兵冲突、非本地文件系统和不私有的 SQLite 文件。
func validateExternalAuthorityPath(path string) (string, error) {
	authorityPath, err := physicalPath(path, true)
	if err != nil {
		return "", fmt.Errorf("%w: authority path", ErrInvalidConfig)
	}
	if err := rejectAuthorityLockSentinelInode(authorityPath); err != nil {
		return "", err
	}
	// External RecoveryCustody is an independent singleton authority.  A
	// private mode bit or inode check is not enough on NFS/SMB, where flock
	// and SQLite durability semantics are not the required authority proof.
	// Require a positively identified local filesystem before taking the
	// kernel lease; unknown or network filesystems fail closed.
	if err := securefs.CheckLocalFilesystem(filepath.Dir(authorityPath)); err != nil {
		return "", fmt.Errorf("%w: authority filesystem is not local", ErrInvalidConfig)
	}
	if err := validateAuthorityPrivateFilesystem(authorityPath); err != nil {
		return "", err
	}
	return authorityPath, nil
}

// validateGenericAuthorityPaths 校验通用调用方的 authority、产品库和工作区
// 物理路径，并在任何 SQLite 打开前完成独立性与私有文件合同检查。
func validateGenericAuthorityPaths(config Config) (string, error) {
	authorityPath, productPath, workspacePath, err := resolveIndependentPaths(config)
	if err != nil {
		return "", err
	}
	if err := rejectAuthorityLockSentinelInode(authorityPath); err != nil {
		return "", err
	}
	if err := validateDistinctPhysicalPaths(authorityPath, productPath, workspacePath); err != nil {
		return "", err
	}
	if err := validateAuthorityPrivateFilesystem(authorityPath); err != nil {
		return "", err
	}
	// Check the canonical authority path so aliases cannot bypass sidecar or
	// inode validation, while retaining the generic product/workspace checks.
	canonicalConfig := config
	canonicalConfig.Path = authorityPath
	if err := validateDistinctSQLiteFiles(canonicalConfig); err != nil {
		return "", err
	}
	return authorityPath, nil
}

// validateAuthorityPrivateFilesystem 要求 authority 主文件及已有 SQLite sidecar 均为
// owner-only 普通文件；缺失文件留给 Bootstrap/SQLite 创建，但任何已存在的符号链接、
// 特殊文件或 group/world 可读权限都在取得租约前 fail-fast。
func validateAuthorityPrivateFilesystem(path string) error {
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("%w: inspect authority parent directory", ErrInvalidConfig)
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("%w: authority parent is not a directory", ErrInvalidConfig)
	}
	if err := securefs.CheckPrivateOwnerOnly(parent, parentInfo); err != nil {
		return fmt.Errorf("%w: authority parent directory is not private", ErrInvalidConfig)
	}
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, statErr := os.Lstat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("%w: inspect authority SQLite path", ErrInvalidConfig)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: authority SQLite path must be a regular non-symlink file", ErrInvalidConfig)
		}
		if err := securefs.CheckPrivateOwnerOnly(candidate, info); err != nil {
			return fmt.Errorf("%w: authority SQLite path is not private", ErrInvalidConfig)
		}
	}
	return nil
}

// captureAuthorityPathIdentity 以不跟随符号链接的方式读取 authority 主库及
// SQLite sidecar 身份；已有条目必须满足私有普通文件合同，主库缺失仅供 fresh Bootstrap。
func captureAuthorityPathIdentity(path string) (authorityPathIdentity, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return authorityPathIdentity{}, fmt.Errorf("%w: authority path identity", ErrInvalidConfig)
	}
	main, err := captureAuthorityFileIdentity(path)
	if err != nil {
		return authorityPathIdentity{}, err
	}
	wal, err := captureAuthorityFileIdentity(path + "-wal")
	if err != nil {
		return authorityPathIdentity{}, err
	}
	shm, err := captureAuthorityFileIdentity(path + "-shm")
	if err != nil {
		return authorityPathIdentity{}, err
	}
	return authorityPathIdentity{main: main, wal: wal, shm: shm}, nil
}

// captureAuthorityFileIdentity 用 Lstat 检查单个 authority 文件，避免替换的符号
// 链接被误认为其指向的 SQLite 对象。
func captureAuthorityFileIdentity(path string) (authorityFileIdentity, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return authorityFileIdentity{}, nil
	}
	if err != nil {
		return authorityFileIdentity{}, fmt.Errorf("%w: inspect authority SQLite path", ErrInvalidConfig)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return authorityFileIdentity{}, fmt.Errorf("%w: authority SQLite path must be a regular non-symlink file", ErrInvalidConfig)
	}
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return authorityFileIdentity{}, fmt.Errorf("%w: authority SQLite path is not private", ErrInvalidConfig)
	}
	return authorityFileIdentity{present: true, info: info}, nil
}

// validateAuthorityPathIdentity compares current file objects with the lease
// snapshot.  allowMissingMain is used only after fresh Bootstrap has created
// the database; an existing main database must always retain its inode.
func validateAuthorityPathIdentity(path string, expected authorityPathIdentity, allowMissingMain bool) error {
	_, err := observeAuthorityPathIdentity(path, expected, allowMissingMain)
	return err
}

// observeAuthorityPathIdentity 读取当前文件对象并返回更新后的快照。主库 inode
// 始终固定；WAL/SHM 遵循 SQLite 生命周期，缺失会解除旧绑定，重新出现时绑定新对象。
func observeAuthorityPathIdentity(path string, expected authorityPathIdentity, allowMissingMain bool) (authorityPathIdentity, error) {
	current, err := captureAuthorityPathIdentity(path)
	if err != nil {
		return expected, errors.Join(ErrLeaseRejected, err)
	}
	observed := expected
	if err := validateAuthorityFileIdentity(path, expected.main, current.main, allowMissingMain); err != nil {
		return expected, err
	}
	if expected.main.present {
		observed.main = expected.main
	} else if current.main.present {
		observed.main = current.main
	}
	observed.wal, err = observeAuthoritySidecar(path+"-wal", expected.wal, current.wal)
	if err != nil {
		return expected, err
	}
	observed.shm, err = observeAuthoritySidecar(path+"-shm", expected.shm, current.shm)
	if err != nil {
		return expected, err
	}
	return observed, nil
}

// observeAuthoritySidecar 处理 WAL/SHM 的可重建生命周期：现存对象必须保持
// inode，SQLite 删除后再出现则作为新一代对象绑定；任意同时存在的替换直接拒绝。
func observeAuthoritySidecar(path string, expected, current authorityFileIdentity) (authorityFileIdentity, error) {
	if expected.present && current.present && !os.SameFile(expected.info, current.info) {
		return expected, fmt.Errorf("%w: authority SQLite sidecar identity changed at %s", ErrLeaseRejected, securefs.RedactPath(path))
	}
	if !current.present {
		return authorityFileIdentity{}, nil
	}
	return current, nil
}

// validateAuthorityFileIdentity 检查普通私有文件形状，并在快照中已有对象时
// 强制核对其精确 inode 身份。
func validateAuthorityFileIdentity(path string, expected, current authorityFileIdentity, allowCreate bool) error {
	if expected.present {
		if !current.present || !os.SameFile(expected.info, current.info) {
			return fmt.Errorf("%w: authority SQLite file identity changed at %s", ErrLeaseRejected, securefs.RedactPath(path))
		}
		return nil
	}
	if current.present && !allowCreate {
		return fmt.Errorf("%w: authority SQLite file appeared after lease validation at %s", ErrLeaseRejected, securefs.RedactPath(path))
	}
	return nil
}

// authorityLeaseIdentitySnapshot 返回租约持有的不可变 DB/WAL/SHM 快照；测试或
// 自定义租约退化为本地读取，使启动包装器仍能执行前后身份复核。
func authorityLeaseIdentitySnapshot(lease AuthorityLease, path string) (authorityPathIdentity, error) {
	if kernel, ok := lease.(*KernelAuthorityLease); ok {
		return kernel.authorityIdentitySnapshot(path)
	}
	return captureAuthorityPathIdentity(path)
}

// withValidatedAuthorityScope 将启动 open/bootstrap/takeover 包在 DB 前后身份
// 复核内，并让 KernelAuthorityLease 在释放内核锁前绑定启动后的快照。
func withValidatedAuthorityScope(
	ctx context.Context,
	lease AuthorityLease,
	scope AuthorityScope,
	path string,
	callback func() error,
) error {
	if lease == nil || callback == nil {
		return ErrLeaseRejected
	}
	if kernel, ok := lease.(*KernelAuthorityLease); ok {
		return kernel.withValidatedScopeAndIdentity(ctx, scope, path, callback)
	}
	expected, err := authorityLeaseIdentitySnapshot(lease, path)
	if err != nil {
		return err
	}
	wrapped := func() error {
		if err := validateAuthorityPathIdentity(path, expected, false); err != nil {
			return err
		}
		callbackErr := callback()
		if err := validateAuthorityPathIdentity(path, expected, true); err != nil {
			return errors.Join(callbackErr, err, ErrCommitOutcomeUnknown)
		}
		return callbackErr
	}
	// Non-kernel leases are test seams only.  Preserve their historical startup
	// call shape (one validation, no mutation callback) while still checking the
	// path object before and after the callback.
	if err := lease.Validate(ctx, scope); err != nil {
		return errors.Join(ErrLeaseRejected, err)
	}
	return wrapped()
}

// rejectAuthorityLeafSymlink 用 Lstat 拒绝可被 SQLite 跟随的 authority 数据库叶子符号链接。
func rejectAuthorityLeafSymlink(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: inspect authority path", ErrInvalidConfig)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: authority database leaf must not be a symlink", ErrInvalidConfig)
	}
	return nil
}

// rejectAuthorityLockSentinelInode 拒绝 authority 主库与内核锁哨兵复用同一文件对象。
// 路径名冲突由 Config.Validate 处理；这里补充 hardlink 等不同名称的 inode alias。
func rejectAuthorityLockSentinelInode(path string) error {
	sentinelPath := filepath.Join(filepath.Dir(path), kernelAuthorityLeaseSentinel)
	authorityInfo, authorityErr := os.Lstat(path)
	if authorityErr != nil && !errors.Is(authorityErr, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect authority database path", ErrInvalidConfig)
	}
	sentinelInfo, sentinelErr := os.Lstat(sentinelPath)
	if sentinelErr != nil && !errors.Is(sentinelErr, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect kernel lock sentinel", ErrInvalidConfig)
	}
	if authorityErr == nil && sentinelErr == nil && os.SameFile(authorityInfo, sentinelInfo) {
		return fmt.Errorf("%w: authority database shares inode with kernel lock sentinel", ErrInvalidConfig)
	}
	return nil
}

// resolveIndependentPaths 依次解析 authority、产品 SQLite 和工作区的物理路径。
func resolveIndependentPaths(config Config) (string, string, string, error) {
	authorityPath, err := physicalPath(config.Path, true)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: authority path", ErrInvalidConfig)
	}
	productPath, err := physicalPath(config.ProductSQLitePath, true)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: product SQLite path", ErrInvalidConfig)
	}
	workspacePath, err := physicalPath(config.WorkspaceRoot, false)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: workspace root", ErrInvalidConfig)
	}
	return authorityPath, productPath, workspacePath, nil
}

// validateDistinctPhysicalPaths 拒绝相同数据库路径、共享父目录以及落入工作区的 authority 路径。
func validateDistinctPhysicalPaths(authorityPath, productPath, workspacePath string) error {
	if authorityPath == productPath {
		return fmt.Errorf("%w: authority path is not independent", ErrInvalidConfig)
	}
	if filepath.Dir(authorityPath) == filepath.Dir(productPath) {
		return fmt.Errorf("%w: authority and product SQLite directories are not independent", ErrInvalidConfig)
	}
	if pathWithin(authorityPath, workspacePath) {
		return fmt.Errorf("%w: authority path is not independent", ErrInvalidConfig)
	}
	return nil
}

// validateDistinctSQLiteFiles 分别拒绝父目录 inode alias 与数据库文件 inode alias。
func validateDistinctSQLiteFiles(config Config) error {
	if err := validateDistinctSQLiteDirectories(config); err != nil {
		return err
	}
	return validateDistinctSQLiteFileInodes(config)
}

// validateDistinctSQLiteDirectories 拒绝 authority 与产品 SQLite 父目录共享物理 inode。
func validateDistinctSQLiteDirectories(config Config) error {
	authorityDirectoryInfo, authorityDirectoryErr := os.Stat(filepath.Dir(config.Path))
	productDirectoryInfo, productDirectoryErr := os.Stat(filepath.Dir(config.ProductSQLitePath))
	if authorityDirectoryErr == nil && productDirectoryErr == nil && os.SameFile(authorityDirectoryInfo, productDirectoryInfo) {
		return fmt.Errorf("%w: authority and product SQLite directories share an inode", ErrInvalidConfig)
	}
	if authorityDirectoryErr != nil {
		return fmt.Errorf("%w: inspect authority directory", ErrInvalidConfig)
	}
	if productDirectoryErr != nil {
		return fmt.Errorf("%w: inspect product SQLite directory", ErrInvalidConfig)
	}
	return nil
}

// validateDistinctSQLiteFileInodes 保持先 stat 两个数据库，再检查 inode 和各自 stat 错误的顺序。
func validateDistinctSQLiteFileInodes(config Config) error {
	authorityInfo, authorityErr := os.Stat(config.Path)
	productInfo, productErr := os.Stat(config.ProductSQLitePath)
	if authorityErr == nil && productErr == nil && os.SameFile(authorityInfo, productInfo) {
		return fmt.Errorf("%w: authority and product SQLite share an inode", ErrInvalidConfig)
	}
	if authorityErr != nil && !errors.Is(authorityErr, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect authority path", ErrInvalidConfig)
	}
	if productErr != nil && !errors.Is(productErr, os.ErrNotExist) {
		return fmt.Errorf("%w: inspect product SQLite path", ErrInvalidConfig)
	}
	return nil
}

// physicalPath 解析真实路径；仅在允许时接受缺失叶子并解析其已存在父目录。
func physicalPath(path string, allowMissingLeaf bool) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !allowMissingLeaf || !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

// pathWithin 判断 path 是否等于或位于 root 下，并拒绝跨根和父目录逃逸结果。
func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
