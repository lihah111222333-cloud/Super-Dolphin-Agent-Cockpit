//go:build !windows

package sharedfilefs

// filesystemPathCaseFold 固定非 Windows 文件系统路径比较采用大小写敏感语义。
const filesystemPathCaseFold = false
