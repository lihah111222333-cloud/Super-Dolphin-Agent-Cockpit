package heteroci

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// LinuxCapacityFacts 保存 Linux Docker 与 cgroup v2 的容量事实及已预留资源。
type LinuxCapacityFacts struct {
	DockerLogicalCPUs int64
	DockerMemoryBytes int64
	CgroupCPUMax      string
	CgroupMemoryMax   string
	Reserved          ResourceCapacity
}

// DeriveLinuxCapacity 严格求出 Docker 与 cgroup 约束交集及预留后的可用容量。
func DeriveLinuxCapacity(facts LinuxCapacityFacts) (ResourceCapacity, ResourceCapacity, error) {
	if err := validateLinuxCapacityFacts(facts); err != nil {
		return ResourceCapacity{}, ResourceCapacity{}, err
	}
	total, err := deriveLinuxTotalCapacity(facts)
	if err != nil {
		return ResourceCapacity{}, ResourceCapacity{}, err
	}
	if err := validateLinuxReservedAgainstTotal(facts.Reserved, total); err != nil {
		return ResourceCapacity{}, ResourceCapacity{}, err
	}
	return total, subtractLinuxReserved(total, facts.Reserved), nil
}

// validateLinuxCapacityFacts 校验 Docker 与预留容量的基础事实。
func validateLinuxCapacityFacts(facts LinuxCapacityFacts) error {
	if facts.DockerLogicalCPUs <= 0 {
		return errors.New("linux Docker logical CPU count must be positive")
	}
	if facts.DockerMemoryBytes <= 0 {
		return errors.New("linux Docker memory capacity must be positive")
	}
	return validateLinuxReservedCapacity(facts.Reserved)
}

// deriveLinuxTotalCapacity 解析 CPU、内存约束并构造总容量。
func deriveLinuxTotalCapacity(facts LinuxCapacityFacts) (ResourceCapacity, error) {
	cpuMilli, err := deriveLinuxCPUMilli(facts)
	if err != nil {
		return ResourceCapacity{}, err
	}
	memoryBytes, err := deriveLinuxMemoryBytes(facts)
	if err != nil {
		return ResourceCapacity{}, err
	}
	maxConcurrency, err := deriveLinuxMaxConcurrency(cpuMilli)
	if err != nil {
		return ResourceCapacity{}, err
	}
	return ResourceCapacity{CPUMilli: cpuMilli, MemoryBytes: memoryBytes, MaxConcurrency: maxConcurrency}, nil
}

// deriveLinuxCPUMilli 计算 Docker 与 cgroup cpu.max 的严格 CPU 交集。
func deriveLinuxCPUMilli(facts LinuxCapacityFacts) (int64, error) {
	dockerCPUMilli, ok := checkedPositiveMultiply(facts.DockerLogicalCPUs, 1000)
	if !ok {
		return 0, errors.New("linux Docker logical CPU millicore capacity overflows int64")
	}
	cgroupCPUMilli, err := parseLinuxCPUMax(facts.CgroupCPUMax)
	if err != nil {
		return 0, fmt.Errorf("parse Linux cgroup cpu.max: %w", err)
	}
	if cgroupCPUMilli != 0 && cgroupCPUMilli < dockerCPUMilli {
		dockerCPUMilli = cgroupCPUMilli
	}
	if dockerCPUMilli <= 0 {
		return 0, errors.New("linux effective CPU millicore capacity must be positive")
	}
	return dockerCPUMilli, nil
}

// deriveLinuxMemoryBytes 计算 Docker 与 cgroup memory.max 的严格内存交集。
func deriveLinuxMemoryBytes(facts LinuxCapacityFacts) (int64, error) {
	cgroupMemoryBytes, err := parseLinuxMemoryMax(facts.CgroupMemoryMax)
	if err != nil {
		return 0, fmt.Errorf("parse Linux cgroup memory.max: %w", err)
	}
	if cgroupMemoryBytes != 0 && cgroupMemoryBytes < facts.DockerMemoryBytes {
		facts.DockerMemoryBytes = cgroupMemoryBytes
	}
	if facts.DockerMemoryBytes <= 0 {
		return 0, errors.New("linux effective memory capacity must be positive")
	}
	return facts.DockerMemoryBytes, nil
}

// deriveLinuxMaxConcurrency 将 CPU 毫核向上取整为可表示的并发上限。
func deriveLinuxMaxConcurrency(cpuMilli int64) (int, error) {
	maxConcurrency64 := cpuMilli / 1000
	if cpuMilli%1000 != 0 {
		maxConcurrency64++
	}
	maxInt := int64(^uint(0) >> 1)
	if maxConcurrency64 <= 0 || maxConcurrency64 > maxInt {
		return 0, errors.New("linux effective maximum concurrency cannot be represented as int")
	}
	return int(maxConcurrency64), nil
}

func validateLinuxReservedCapacity(reserved ResourceCapacity) error {
	if reserved.CPUMilli < 0 || reserved.MemoryBytes < 0 || reserved.MaxConcurrency < 0 {
		return errors.New("linux reserved capacity must be non-negative")
	}
	return nil
}

// validateLinuxReservedAgainstTotal 确保预留资源不超过推导出的总容量。
func validateLinuxReservedAgainstTotal(reserved, total ResourceCapacity) error {
	if reserved.CPUMilli > total.CPUMilli || reserved.MemoryBytes > total.MemoryBytes || reserved.MaxConcurrency > total.MaxConcurrency {
		return errors.New("linux reserved capacity exceeds total capacity")
	}
	return nil
}

// subtractLinuxReserved 从总容量扣除已预留资源。
func subtractLinuxReserved(total, reserved ResourceCapacity) ResourceCapacity {
	return ResourceCapacity{
		CPUMilli:       total.CPUMilli - reserved.CPUMilli,
		MemoryBytes:    total.MemoryBytes - reserved.MemoryBytes,
		MaxConcurrency: total.MaxConcurrency - reserved.MaxConcurrency,
	}
}

// parseLinuxCPUMax 严格解析 cgroup v2 的 cpu.max 两字段。
func parseLinuxCPUMax(raw string) (int64, error) {
	fields, err := strictLinuxFields(raw, 2)
	if err != nil {
		return 0, err
	}
	period, err := parseLinuxPositiveInt(fields[1], "cpu.max period")
	if err != nil {
		return 0, err
	}
	if fields[0] == "max" {
		return 0, nil
	}
	quota, err := parseLinuxPositiveInt(fields[0], "cpu.max quota")
	if err != nil {
		return 0, err
	}
	if quota > math.MaxInt64/1000 {
		return 0, errors.New("cpu.max quota times one thousand overflows int64")
	}
	milli := quota * 1000 / period
	if milli <= 0 {
		return 0, errors.New("cpu.max quota produces no positive millicore capacity")
	}
	return milli, nil
}

func parseLinuxMemoryMax(raw string) (int64, error) {
	fields, err := strictLinuxFields(raw, 1)
	if err != nil {
		return 0, err
	}
	if fields[0] == "max" {
		return 0, nil
	}
	return parseLinuxPositiveInt(fields[0], "memory.max limit")
}

func strictLinuxFields(raw string, want int) ([]string, error) {
	if raw == "" {
		return nil, errors.New("value is empty")
	}
	raw = strings.TrimSuffix(raw, "\n")
	if strings.ContainsAny(raw, "\r\n") {
		return nil, errors.New("value contains an invalid line break")
	}
	fields := strings.Fields(raw)
	if len(fields) != want {
		return nil, fmt.Errorf("value must contain exactly %d field(s), got %d", want, len(fields))
	}
	return fields, nil
}

// parseLinuxPositiveInt 严格解析无符号十进制正整数并检查溢出。
func parseLinuxPositiveInt(token, field string) (int64, error) {
	if token == "" {
		return 0, fmt.Errorf("%s is empty", field)
	}
	value := int64(0)
	for index := 0; index < len(token); index++ {
		digit := token[index]
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("%s must be an unsigned decimal integer", field)
		}
		digitValue := int64(digit - '0')
		if value > (math.MaxInt64-digitValue)/10 {
			return 0, fmt.Errorf("%s overflows int64", field)
		}
		value = value*10 + digitValue
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be positive", field)
	}
	return value, nil
}

func checkedPositiveMultiply(left, right int64) (int64, bool) {
	if left <= 0 || right <= 0 || left > math.MaxInt64/right {
		return 0, false
	}
	return left * right, true
}
