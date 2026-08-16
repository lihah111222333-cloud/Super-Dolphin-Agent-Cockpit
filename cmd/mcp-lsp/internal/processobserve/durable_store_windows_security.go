//go:build windows

package processobserve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsOpenReparsePoint 是 WinBase.h 中打开重解析点本身的 0x00200000 标志。
const windowsOpenReparsePoint uint32 = 0x00200000

// windowsDurableFullControlMask 对应 WinNT.h 的 FILE_ALL_ACCESS。
const windowsDurableFullControlMask windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

// openWindowsDurableRootHandle 逐级验证目录组件并打开持久化根句柄。
func openWindowsDurableRootHandle(path string) (windows.Handle, error) {
	volumeRoot := filepath.VolumeName(path) + string(os.PathSeparator)
	relative, err := filepath.Rel(volumeRoot, path)
	if err != nil || relative == "." {
		return windows.InvalidHandle, errors.New("durable observation root must be below a volume root")
	}
	currentPath := volumeRoot
	for component := range strings.SplitSeq(relative, string(os.PathSeparator)) {
		if component == "" || component == "." || component == ".." {
			return windows.InvalidHandle, errors.New("durable observation root contains an unsafe component")
		}
		currentPath = filepath.Join(currentPath, component)
		_, err := ensureWindowsDirectoryComponent(currentPath)
		if err != nil {
			return windows.InvalidHandle, err
		}
		if err := requireWindowsDirectoryPath(currentPath); err != nil {
			return windows.InvalidHandle, err
		}
	}
	handle, err := openWindowsDirectory(path, true)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("open durable observation root %s: %w", path, err)
	}
	return handle, nil
}

// ensureWindowsDirectoryComponent 创建缺失目录并立即设置私有 DACL。
func ensureWindowsDirectoryComponent(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("inspect durable observation root component: %w", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("create durable observation root component: %w", err)
	}
	createdInfo, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect created durable observation root component: %w", err)
	}
	if createdInfo.Mode()&os.ModeSymlink != 0 || !createdInfo.IsDir() {
		return false, fmt.Errorf("created durable observation root component is not a real directory: %s", path)
	}
	if err := hardenWindowsPrivateDirectory(path); err != nil {
		return false, err
	}
	return true, nil
}

func requireWindowsDirectoryPath(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return fmt.Errorf("inspect durable observation root component %s: %w", path, err)
	}
	if attributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("durable observation root component is not a real directory: %s", path)
	}
	return nil
}

func openWindowsDirectory(path string, readSecurity bool) (windows.Handle, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	access := uint32(windows.FILE_READ_ATTRIBUTES | windows.SYNCHRONIZE)
	if readSecurity {
		access |= windows.READ_CONTROL
	}
	return windows.CreateFile(
		pointer,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windowsOpenReparsePoint,
		0,
	)
}

// hardenWindowsPrivateDirectory 为目录设置当前用户与 SYSTEM 的受保护全控 DACL。
func hardenWindowsPrivateDirectory(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("read current Windows user SID: %w", err)
	}
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + sid + ")",
	)
	if err != nil {
		return fmt.Errorf("build durable observation directory DACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read durable observation directory DACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		return fmt.Errorf("set durable observation directory DACL: %w", err)
	}
	return nil
}

func requireWindowsPrivateDirectory(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	info, err := requireWindowsDirectory(handle)
	if err != nil {
		return windows.ByHandleFileInformation{}, err
	}
	if err := requireWindowsPrivateDescriptor(handle, true); err != nil {
		return windows.ByHandleFileInformation{}, err
	}
	return info, nil
}

func requireWindowsDirectory(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	info, err := windowsFileInformation(handle)
	if err != nil {
		return windows.ByHandleFileInformation{}, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return windows.ByHandleFileInformation{}, errors.New("durable observation root component is not a real directory")
	}
	return info, nil
}

// requireWindowsPrivateRegularFile 验证句柄指向受限大小的单链接私有常规文件。
func requireWindowsPrivateRegularFile(handle windows.Handle, maximumSize uint64) (windows.ByHandleFileInformation, error) {
	info, err := windowsFileInformation(handle)
	if err != nil {
		return windows.ByHandleFileInformation{}, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 ||
		info.NumberOfLinks != 1 {
		return windows.ByHandleFileInformation{}, errors.New("durable observation file must be a private regular file with one link")
	}
	size := uint64(info.FileSizeHigh)<<32 | uint64(info.FileSizeLow)
	if maximumSize > 0 && size > maximumSize {
		return windows.ByHandleFileInformation{}, errors.New("durable observation file exceeds size limit")
	}
	if err := requireWindowsPrivateDescriptor(handle, false); err != nil {
		return windows.ByHandleFileInformation{}, err
	}
	return info, nil
}

func windowsFileInformation(handle windows.Handle) (windows.ByHandleFileInformation, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return info, fmt.Errorf("inspect durable observation filesystem object: %w", err)
	}
	return info, nil
}

func requireWindowsPrivateDescriptor(handle windows.Handle, directory bool) error {
	descriptor, err := windows.GetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read durable observation security descriptor: %w", err)
	}
	user, err := requireWindowsDescriptorOwner(descriptor)
	if err != nil {
		return err
	}
	dacl, err := requireWindowsDescriptorDACL(descriptor, directory)
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("resolve Windows local-system SID: %w", err)
	}
	return validateWindowsPrivateACEsForObject(dacl, user, system, directory)
}

func requireWindowsDescriptorOwner(descriptor *windows.SECURITY_DESCRIPTOR) (*windows.SID, error) {
	owner, _, err := descriptor.Owner()
	if err != nil {
		return nil, fmt.Errorf("read durable observation owner: %w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read current Windows user SID: %w", err)
	}
	if owner == nil || !owner.Equals(user.User.Sid) {
		return nil, errors.New("durable observation filesystem object owner does not match current user")
	}
	return user.User.Sid, nil
}

// requireWindowsDescriptorDACL 读取 DACL，并要求私有目录禁用父级继承。
func requireWindowsDescriptorDACL(descriptor *windows.SECURITY_DESCRIPTOR, protected bool) (*windows.ACL, error) {
	if protected {
		control, _, err := descriptor.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			return nil, errors.New("durable observation directory DACL is not protected")
		}
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return nil, errors.New("durable observation filesystem object DACL is unavailable")
	}
	return dacl, nil
}

// validateWindowsPrivateACEsForObject 按目录或继承叶子文件语义校验全部 ACE。
func validateWindowsPrivateACEsForObject(dacl *windows.ACL, user, system *windows.SID, directory bool) error {
	userAllowed := false
	systemAllowed := false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		ace, err := requireWindowsDurableACE(dacl, index, directory)
		if err != nil {
			return err
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user):
			userAllowed = true
		case sid.Equals(system):
			systemAllowed = true
		default:
			return errors.New("durable observation DACL grants access to another SID")
		}
	}
	if !userAllowed || !systemAllowed {
		return errors.New("durable observation DACL is missing current-user or local-system access")
	}
	return nil
}

// requireWindowsDurableACE 校验单个 ACE 的类型、全控掩码和对象继承标志。
func requireWindowsDurableACE(dacl *windows.ACL, index uint32, directory bool) (*windows.ACCESS_ALLOWED_ACE, error) {
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, index, &ace); err != nil {
		return nil, fmt.Errorf("read durable observation DACL entry: %w", err)
	}
	if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		return nil, errors.New("durable observation DACL contains an unsupported entry")
	}
	if ace.Mask&windowsDurableFullControlMask != windowsDurableFullControlMask {
		return nil, errors.New("durable observation DACL entry does not grant full control")
	}
	if !validWindowsDurableACEFlags(ace.Header.AceFlags, directory) {
		return nil, errors.New("durable observation DACL entry has unsafe inheritance flags")
	}
	return ace, nil
}

func validWindowsDurableACEFlags(flags uint8, directory bool) bool {
	if directory {
		return flags == windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE
	}
	return flags == windows.INHERITED_ACE
}

func openWindowsDurableFile(path string, access, creation, attributes uint32) (windows.Handle, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	return windows.CreateFile(
		pointer,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		creation,
		attributes,
		0,
	)
}

func windowsPathNotExist(err error) bool {
	return errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}
