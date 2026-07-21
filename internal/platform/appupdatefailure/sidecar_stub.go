//go:build !darwin

package appupdatefailure

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

// Begin 在非 Darwin 平台明确返回不支持。
func Begin(string, string) error { return ErrUnsupported }

// Fail 在非 Darwin 平台明确返回不支持。
func Fail(string, string, contract.RecoveryFailure) error { return ErrUnsupported }

// ReadFailure 在非 Darwin 平台明确返回不支持。
func ReadFailure(string) (contract.RecoveryFailure, bool, error) {
	return contract.RecoveryFailure{}, false, ErrUnsupported
}

// Clear 在非 Darwin 平台明确返回不支持。
func Clear(string, string) error { return ErrUnsupported }

// InvalidateAll 在非 Darwin 平台明确返回不支持。
func InvalidateAll(string) error { return ErrUnsupported }
