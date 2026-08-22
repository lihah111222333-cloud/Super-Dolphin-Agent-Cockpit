package remotecicli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remoteportable"
)

// currentPortableEngineFacts 从正在运行的独立 binary 和运行时计算 actual engine identity。
func currentPortableEngineFacts() (remoteportable.PortableEngineFacts, error) {
	executable, err := os.Executable()
	if err != nil {
		return remoteportable.PortableEngineFacts{}, errors.New("resolve current engine binary failed")
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil || validateAbsolutePathComponents(executable, false) != nil {
		return remoteportable.PortableEngineFacts{}, errors.New("current engine binary path is invalid")
	}
	binaryDigest, err := hashStableEngineBinary(executable)
	if err != nil {
		return remoteportable.PortableEngineFacts{}, err
	}
	protocolDigest, err := remoteportable.PortableBindingProtocolABIDigest()
	if err != nil {
		return remoteportable.PortableEngineFacts{}, err
	}
	toolchainDigest := digestBytes([]byte("remote-ci-toolchain/v1\x00" + runtime.Version()))
	return remoteportable.PortableEngineFacts{BinarySHA256: binaryDigest, ProtocolABIDigest: protocolDigest, ToolchainDigest: toolchainDigest, Capabilities: []string{"go"}}, nil
}

// hashStableEngineBinary 对独立 binary 做 bounded handle read，并核对前后 identity。
func hashStableEngineBinary(path string) (string, error) {
	const maxEngineBinaryBytes = 512 << 20
	file, openedInfo, err := openEngineBinary(path, maxEngineBinaryBytes)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	count, copyErr := io.Copy(hash, io.LimitReader(file, maxEngineBinaryBytes+1))
	finalInfo, finalStatErr := file.Stat()
	closeErr := file.Close()
	if err := validateEngineBinaryRead(count, openedInfo.Size(), copyErr, finalStatErr, closeErr, openedInfo, finalInfo); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// openEngineBinary 打开并冻结独立 binary 的 regular-file handle 与初始身份。
func openEngineBinary(path string, maxBytes int64) (*os.File, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.New("current engine binary is not regular")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("current engine binary cannot be opened")
	}
	openedInfo, err := file.Stat()
	if err != nil || !sameFileIdentity(info, openedInfo) || openedInfo.Size() > maxBytes {
		_ = file.Close()
		return nil, nil, errors.New("current engine binary identity changed before hashing")
	}
	return file, openedInfo, nil
}

// validateEngineBinaryRead 校验独立 binary 的 bounded read、size 和最终 handle identity。
func validateEngineBinaryRead(count, size int64, copyErr, statErr, closeErr error, opened, final os.FileInfo) error {
	if copyErr != nil || statErr != nil || closeErr != nil {
		return errors.New("current engine binary changed during hashing")
	}
	if count != size || !sameFileIdentity(opened, final) {
		return errors.New("current engine binary changed during hashing")
	}
	return nil
}
