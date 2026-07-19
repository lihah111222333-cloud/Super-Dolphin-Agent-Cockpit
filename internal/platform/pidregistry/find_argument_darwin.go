//go:build darwin

package pidregistry

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func processIDs() ([]int, error) {
	processes, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("pidregistry: list Darwin processes: %w", err)
	}
	pids := make([]int, 0, len(processes))
	for _, process := range processes {
		if process.Proc.P_pid > 1 {
			pids = append(pids, int(process.Proc.P_pid))
		}
	}
	return pids, nil
}

// processArguments 从 Darwin 内核读取指定 PID 的原始 argv。
func processArguments(pid int) ([]string, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EINVAL) {
			return nil, ErrStableProcessNotFound
		}
		return nil, fmt.Errorf("pidregistry: read Darwin process arguments: %w", err)
	}
	arguments, err := parseDarwinProcargs2(raw)
	if err != nil {
		return nil, fmt.Errorf("pidregistry: parse Darwin process arguments: %w", err)
	}
	return arguments, nil
}

// parseDarwinProcargs2 严格跳过 executable 与 padding，只返回 argc 个 argv。
func parseDarwinProcargs2(raw []byte) ([]string, error) {
	argc, payload, err := parseDarwinProcargsHeader(raw)
	if err != nil {
		return nil, err
	}
	cursor, err := darwinArgvOffset(payload)
	if err != nil {
		return nil, err
	}
	return parseDarwinArgv(payload, cursor, argc)
}

func parseDarwinProcargsHeader(raw []byte) (int, []byte, error) {
	const nativeIntBytes = 4
	if len(raw) < nativeIntBytes {
		return 0, nil, errors.New("procargs2 argc is truncated")
	}
	argcRaw := binary.NativeEndian.Uint32(raw[:nativeIntBytes])
	if argcRaw == 0 || uint64(argcRaw) > uint64(^uint(0)>>1) {
		return 0, nil, errors.New("procargs2 argc is invalid")
	}
	argc := int(argcRaw)
	if argc > len(raw)-nativeIntBytes {
		return 0, nil, errors.New("procargs2 argc exceeds payload")
	}
	return argc, raw[nativeIntBytes:], nil
}

func darwinArgvOffset(payload []byte) (int, error) {
	executableEnd := bytes.IndexByte(payload, 0)
	if executableEnd <= 0 {
		return 0, errors.New("procargs2 executable is missing or truncated")
	}
	cursor := executableEnd + 1
	for cursor < len(payload) && payload[cursor] == 0 {
		cursor++
	}
	return cursor, nil
}

// parseDarwinArgv 消费恰好 argc 个 NUL 终止字符串，忽略后续环境区。
func parseDarwinArgv(payload []byte, cursor, argc int) ([]string, error) {
	arguments := make([]string, 0, argc)
	for index := range argc {
		if cursor >= len(payload) {
			return nil, errors.New("procargs2 argv is truncated")
		}
		end := bytes.IndexByte(payload[cursor:], 0)
		if end < 0 {
			return nil, errors.New("procargs2 argv is truncated")
		}
		argument := string(payload[cursor : cursor+end])
		if index == 0 && argument == "" {
			return nil, errors.New("procargs2 argv[0] is empty")
		}
		arguments = append(arguments, argument)
		cursor += end + 1
	}
	return arguments, nil
}
