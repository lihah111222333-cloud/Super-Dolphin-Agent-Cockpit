// Package shared 定义跨模块共享的基础 DTO，包括通用错误哨兵值、输入类型和事件头。
package shared

import "errors"

// 通用错误哨兵值，供各模块统一使用，避免重复定义。
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidState  = errors.New("invalid state transition")
	ErrRequired      = errors.New("required field missing")
)
