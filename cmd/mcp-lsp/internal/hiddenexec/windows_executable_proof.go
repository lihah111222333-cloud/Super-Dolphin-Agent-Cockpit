//go:build windows

package hiddenexec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsBootstrapPathBufferSize = 32768
	windowsOpenReparsePoint        = 0x00200000
)

type windowsGoplsBrokerExecutableProof struct {
	path               string
	sha256             string
	volumeSerialNumber uint32
	fileID             uint64
}

// attestCurrentWindowsGoplsBrokerExecutable 取得当前进程镜像的最终文件身份与内容摘要。
func attestCurrentWindowsGoplsBrokerExecutable() (windowsGoplsBrokerExecutableProof, error) {
	return attestWindowsGoplsBrokerExecutable(windows.CurrentProcess())
}

// attestWindowsGoplsBrokerExecutable 从进程镜像路径打开同一最终文件并建立证明。
func attestWindowsGoplsBrokerExecutable(process windows.Handle) (windowsGoplsBrokerExecutableProof, error) {
	path, err := windowsBootstrapProcessImagePath(process)
	if err != nil {
		return windowsGoplsBrokerExecutableProof{}, err
	}
	return attestWindowsGoplsBrokerExecutableFile(path)
}

// attestWindowsGoplsBrokerExecutableFile 通过单一文件句柄绑定 final path、FileID 与 SHA-256。
func attestWindowsGoplsBrokerExecutableFile(path string) (windowsGoplsBrokerExecutableProof, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windowsGoplsBrokerExecutableProof{}, fmt.Errorf("encode Windows gopls broker executable path: %w", err)
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windowsOpenReparsePoint, 0)
	if err != nil {
		return windowsGoplsBrokerExecutableProof{}, fmt.Errorf("open Windows gopls broker executable: %w", err)
	}
	return attestOpenWindowsGoplsBrokerExecutable(handle)
}

// attestOpenWindowsGoplsBrokerExecutable 从同一打开句柄读取文件身份并流式计算摘要。
func attestOpenWindowsGoplsBrokerExecutable(handle windows.Handle) (windowsGoplsBrokerExecutableProof, error) {
	proof, err := inspectOpenWindowsGoplsBrokerExecutable(handle)
	if err != nil {
		return proof, errors.Join(err, closeWindowsHandle(handle, "close rejected Windows gopls broker executable"))
	}
	file := os.NewFile(uintptr(handle), proof.path)
	if file == nil {
		return proof, errors.Join(errors.New("adopt Windows gopls broker executable handle"), closeWindowsHandle(handle, "close unadopted Windows gopls broker executable"))
	}
	digest := sha256.New()
	_, hashErr := io.Copy(digest, file)
	closeErr := file.Close()
	if hashErr != nil {
		hashErr = fmt.Errorf("hash Windows gopls broker executable: %w", hashErr)
	}
	if hashErr != nil || closeErr != nil {
		return proof, errors.Join(hashErr, closeErr)
	}
	proof.sha256 = hex.EncodeToString(digest.Sum(nil))
	return proof, nil
}

// inspectOpenWindowsGoplsBrokerExecutable 读取 Windows broker 可执行文件的最终路径与文件索引。
func inspectOpenWindowsGoplsBrokerExecutable(handle windows.Handle) (windowsGoplsBrokerExecutableProof, error) {
	path, err := windowsBootstrapFinalPath(handle)
	if err != nil {
		return windowsGoplsBrokerExecutableProof{}, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return windowsGoplsBrokerExecutableProof{}, fmt.Errorf("read Windows gopls broker executable file identity: %w", err)
	}
	invalidAttributes := uint32(windows.FILE_ATTRIBUTE_DIRECTORY | windows.FILE_ATTRIBUTE_REPARSE_POINT)
	name := filepath.Base(path)
	allowedName := windowsGoplsBrokerDeliveryNameAllowed(name)
	if info.FileAttributes&invalidAttributes != 0 || !filepath.IsAbs(path) || !allowedName {
		return windowsGoplsBrokerExecutableProof{}, errors.New("Windows gopls broker bootstrap executable must be a regular .exe file")
	}
	return windowsGoplsBrokerExecutableProof{path: filepath.Clean(path), volumeSerialNumber: info.VolumeSerialNumber, fileID: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)}, nil
}

// windowsGoplsBrokerDeliveryNameAllowed 只要求 Windows broker 交付物是 .exe；最终路径、文件身份和 SHA-256 验证仍构成完整信任边界。
func windowsGoplsBrokerDeliveryNameAllowed(name string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(name)), ".exe")
}

// requireSameWindowsGoplsBrokerExecutable 拒绝路径、文件索引或内容摘要的任一漂移。
func requireSameWindowsGoplsBrokerExecutable(selfProof, childProof windowsGoplsBrokerExecutableProof) error {
	same := strings.EqualFold(filepath.Clean(selfProof.path), filepath.Clean(childProof.path)) && selfProof.volumeSerialNumber == childProof.volumeSerialNumber && selfProof.fileID == childProof.fileID && selfProof.sha256 == childProof.sha256
	if !same {
		return errors.New("started Windows gopls broker bootstrap executable identity changed")
	}
	return nil
}

// windowsBootstrapProcessImagePath 通过受限查询句柄读取进程实际镜像路径。
func windowsBootstrapProcessImagePath(process windows.Handle) (string, error) {
	buffer := make([]uint16, windowsBootstrapPathBufferSize)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return "", fmt.Errorf("query Windows gopls broker process image: %w", err)
	}
	if size == 0 || size >= uint32(len(buffer)) {
		return "", errors.New("Windows gopls broker process image path length is invalid")
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

// windowsBootstrapFinalPath 通过打开句柄解析最终 DOS 路径并移除 Win32 扩展前缀。
func windowsBootstrapFinalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, windowsBootstrapPathBufferSize)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", fmt.Errorf("resolve final Windows gopls broker executable path: %w", err)
	}
	if length == 0 || length >= uint32(len(buffer)) {
		return "", errors.New("final Windows gopls broker executable path exceeds the fixed buffer")
	}
	path := windows.UTF16ToString(buffer[:length])
	if suffix, ok := strings.CutPrefix(path, `\\?\UNC\`); ok {
		path = `\\` + suffix
	} else {
		path, _ = strings.CutPrefix(path, `\\?\`)
	}
	return filepath.Clean(path), nil
}

// windowsBootstrapProcessInJob 查询指定精确进程句柄是否仍属于任意 Job。
func windowsBootstrapProcessInJob(process windows.Handle) (bool, error) {
	var inJob int32
	procedure := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	result, _, callErr := procedure.Call(uintptr(process), 0, uintptr(unsafe.Pointer(&inJob)))
	if result == 0 {
		return false, windowsBootstrapCallError("query Windows process Job membership", callErr)
	}
	return inJob != 0, nil
}

// windowsBootstrapProcessStartIdentity 从精确句柄读取防 PID 复用的创建时间 token。
func windowsBootstrapProcessStartIdentity(process windows.Handle) (string, error) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &creation, &exit, &kernel, &user); err != nil {
		return "", fmt.Errorf("read Windows gopls broker bootstrap start identity: %w", err)
	}
	value := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	return strconv.FormatUint(value, 10), nil
}

// windowsBootstrapCallError 保留 Win32 调用错误，并拒绝零 errno 伪装成功。
func windowsBootstrapCallError(action string, callErr error) error {
	if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
		return errors.New(action + ": unknown Win32 error")
	}
	return fmt.Errorf("%s: %w", action, callErr)
}
