package heteroci

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// ManifestFileOperation 表示清单文件加载失败所属的安全操作阶段。
type ManifestFileOperation string

const (
	// ManifestFileOperationPath 表示清单文件路径校验阶段。
	ManifestFileOperationPath ManifestFileOperation = "path"
	// ManifestFileOperationOpen 表示清单文件打开阶段。
	ManifestFileOperationOpen ManifestFileOperation = "open"
	// ManifestFileOperationStat 表示清单文件属性检查阶段。
	ManifestFileOperationStat ManifestFileOperation = "stat"
	// ManifestFileOperationDecode 表示清单内容解码阶段。
	ManifestFileOperationDecode ManifestFileOperation = "decode"
	// ManifestFileOperationClose 表示清单文件关闭阶段。
	ManifestFileOperationClose ManifestFileOperation = "close"
)

// ManifestFileError 表示清单文件加载失败，并保留可检查的底层错误链。
type ManifestFileError struct {
	operation ManifestFileOperation
	cause     error
}

// Operation 返回清单文件加载失败所属的安全操作阶段。
func (err *ManifestFileError) Operation() ManifestFileOperation {
	if err == nil {
		return ""
	}
	return err.operation
}

// Error 返回不含路径、payload 或身份信息的稳定错误文本。
func (err *ManifestFileError) Error() string {
	if err == nil {
		return "heterogeneous CI manifest file operation failed"
	}
	return string(err.operation) + " heterogeneous CI manifest file failed"
}

// Unwrap 返回底层错误，供 errors.Is/errors.As 检查根因。
func (err *ManifestFileError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// LoadManifestFile 从唯一显式的绝对路径读取常规文件，并交由 DecodeManifest 严格校验。
func LoadManifestFile(path string) (Manifest, error) {
	if err := validateManifestFilePath(path); err != nil {
		return Manifest{}, safeManifestFileOperationError(ManifestFileOperationPath, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, safeManifestFileOperationError(ManifestFileOperationOpen, err)
	}

	info, err := file.Stat()
	if err != nil {
		return finishManifestFileLoad(
			Manifest{},
			safeManifestFileOperationError(ManifestFileOperationStat, err),
			file.Close,
		)
	}
	if !info.Mode().IsRegular() {
		return finishManifestFileLoad(
			Manifest{},
			safeManifestFileOperationError(
				ManifestFileOperationStat,
				errors.New("heterogeneous CI manifest file is not regular"),
			),
			file.Close,
		)
	}

	manifest, decodeErr := DecodeManifest(file)
	if decodeErr != nil {
		decodeErr = safeManifestFileOperationError(ManifestFileOperationDecode, decodeErr)
	}
	return finishManifestFileLoad(manifest, decodeErr, file.Close)
}

// finishManifestFileLoad 恰好关闭一次清单文件，并合并操作错误与关闭错误。
func finishManifestFileLoad(
	manifest Manifest,
	operationErr error,
	closeFile func() error,
) (Manifest, error) {
	closeErr := closeFile()
	if closeErr != nil {
		closeErr = safeManifestFileOperationError(ManifestFileOperationClose, closeErr)
	}
	if operationErr != nil || closeErr != nil {
		return Manifest{}, errors.Join(operationErr, closeErr)
	}
	return manifest, nil
}

// validateManifestFilePath 校验清单路径必须是显式、绝对且规范的安全路径。
func validateManifestFilePath(path string) error {
	if path == "" {
		return errors.New("load heterogeneous CI manifest file path is required")
	}
	if path != strings.TrimSpace(path) {
		return errors.New("load heterogeneous CI manifest file path must not have surrounding whitespace")
	}
	if strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return errors.New("load heterogeneous CI manifest file path must not contain control characters")
	}
	if !filepath.IsAbs(path) {
		return errors.New("load heterogeneous CI manifest file path must be absolute")
	}
	if filepath.Clean(path) != path {
		return errors.New("load heterogeneous CI manifest file path must be clean")
	}
	return nil
}

// safeManifestFileOperationError 保留底层文件错误，同时隐藏敏感路径、payload 与身份文本。
func safeManifestFileOperationError(operation ManifestFileOperation, cause error) *ManifestFileError {
	return &ManifestFileError{operation: operation, cause: cause}
}
