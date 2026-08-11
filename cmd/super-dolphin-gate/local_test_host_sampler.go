package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

const (
	localHostSampleInterval     = 5 * time.Second
	localHostSampleCount        = 7
	localHostCommandTimeout     = 5 * time.Second
	localHostSysctlPath         = "/usr/sbin/sysctl"
	localHostMemoryPressurePath = "/usr/bin/memory_pressure"
	localHostTopPath            = "/usr/bin/top"
	localHostGiB                = uint64(1024 * 1024 * 1024)
	// localHostCPUPercentageRoundingTolerance accepts only the error introduced
	// when Darwin top renders three independently rounded percentages.
	localHostCPUPercentageRoundingTolerance = 0.1
)

var (
	localHostCPULinePattern    = regexp.MustCompile(`^CPU usage: ([0-9]+(?:\.[0-9]+)?)% user, ([0-9]+(?:\.[0-9]+)?)% sys, ([0-9]+(?:\.[0-9]+)?)% idle$`)
	localHostMemoryLinePattern = regexp.MustCompile(`^System-wide memory free percentage: ([0-9]+(?:\.[0-9]+)?)%$`)
)

type localHostCommandRunner func(context.Context, string, ...string) (string, error)

type localHostAdmissionSamplerDependencies struct {
	platform    string
	run         localHostCommandRunner
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
	withTimeout func(context.Context, time.Duration) (context.Context, context.CancelFunc)
}

// newProductionLocalHostAdmissionSampler 只构造回调；调度器在 local MISS 后调用它时才读取宿主机。
func newProductionLocalHostAdmissionSampler() gatecontract.LocalHostAdmissionSampler {
	return newLocalHostAdmissionSampler(localHostAdmissionSamplerDependencies{
		platform:    runtime.GOOS,
		run:         runLocalHostSystemCommand,
		now:         time.Now,
		sleep:       sleepForLocalHostSample,
		withTimeout: context.WithTimeout,
	})
}

func newLocalHostAdmissionSampler(dependencies localHostAdmissionSamplerDependencies) gatecontract.LocalHostAdmissionSampler {
	return func(ctx context.Context) (gatecontract.LocalHostAdmission, error) {
		return sampleLocalHostAdmission(ctx, dependencies, localHostSampleCount)
	}
}

func sampleLocalHostAdmission(ctx context.Context, dependencies localHostAdmissionSamplerDependencies, sampleCount int) (gatecontract.LocalHostAdmission, error) {
	if err := validateLocalHostSamplerDependencies(ctx, dependencies, sampleCount); err != nil {
		return gatecontract.LocalHostAdmission{}, err
	}
	logicalCPU, totalMemoryBytes, freeMemoryPercent, err := readLocalHostCapacity(ctx, dependencies)
	if err != nil {
		return gatecontract.LocalHostAdmission{}, err
	}
	samples, err := collectLocalHostCPUSamples(ctx, dependencies, sampleCount)
	if err != nil {
		return gatecontract.LocalHostAdmission{}, err
	}
	averageBusy, err := weightedLocalHostCPUBusy(samples)
	if err != nil {
		return gatecontract.LocalHostAdmission{}, err
	}
	availableCPU, availableMemoryGiB := localHostAvailableCapacity(logicalCPU, totalMemoryBytes, freeMemoryPercent, averageBusy)
	return gatecontract.BuildLocalHostAdmissionFromSamples(samples, availableCPU, availableMemoryGiB)
}

// validateLocalHostSamplerDependencies 在 Darwin 上启动严格本机准入采样前校验上下文、最少采样数和依赖；
// 任一条件不满足立即失败，禁止回退为宽松或跨平台采样。
func validateLocalHostSamplerDependencies(ctx context.Context, dependencies localHostAdmissionSamplerDependencies, sampleCount int) error {
	if ctx == nil {
		return errors.New("local host sampler context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if dependencies.platform != "darwin" {
		return fmt.Errorf("local host admission sampling is only supported on darwin, got %q", dependencies.platform)
	}
	if sampleCount < localHostSampleCount {
		return fmt.Errorf("local host CPU sampler returned insufficient samples: requires at least %d", localHostSampleCount)
	}
	if dependencies.run == nil || dependencies.now == nil || dependencies.sleep == nil || dependencies.withTimeout == nil {
		return errors.New("local host sampler dependencies are required")
	}
	return nil
}

// readLocalHostCapacity 严格采集 Darwin 的逻辑 CPU、物理总内存与 memory_pressure 空闲百分比；
// 任一命令失败或输出无效即返回错误，防止以不完整容量放行。
func readLocalHostCapacity(ctx context.Context, dependencies localHostAdmissionSamplerDependencies) (float64, uint64, float64, error) {
	logicalCPUOutput, err := runLocalHostCommand(ctx, dependencies, localHostSysctlPath, "-n", "hw.logicalcpu")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read local host logical CPU: %w", err)
	}
	logicalCPU, err := parsePositiveLocalHostUint(logicalCPUOutput, "logical CPU")
	if err != nil {
		return 0, 0, 0, err
	}
	totalMemoryOutput, err := runLocalHostCommand(ctx, dependencies, localHostSysctlPath, "-n", "hw.memsize")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read local host memory: %w", err)
	}
	totalMemory, err := parsePositiveLocalHostUint(totalMemoryOutput, "total memory")
	if err != nil {
		return 0, 0, 0, err
	}
	memoryPressureOutput, err := runLocalHostCommand(ctx, dependencies, localHostMemoryPressurePath, "-Q")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read local host memory pressure: %w", err)
	}
	freePercent, err := parseLocalHostFreeMemoryPercent(memoryPressureOutput)
	if err != nil {
		return 0, 0, 0, err
	}
	return float64(logicalCPU), totalMemory, freePercent, nil
}

func collectLocalHostCPUSamples(ctx context.Context, dependencies localHostAdmissionSamplerDependencies, sampleCount int) ([]gatecontract.LocalHostCPUSample, error) {
	samples := make([]gatecontract.LocalHostCPUSample, 0, sampleCount)
	for index := range sampleCount {
		busy, err := readLocalHostCPUBusy(ctx, dependencies)
		if err != nil {
			return nil, err
		}
		samples = append(samples, gatecontract.LocalHostCPUSample{At: dependencies.now().UTC(), BusyPercent: busy})
		if index == sampleCount-1 {
			continue
		}
		if err := dependencies.sleep(ctx, localHostSampleInterval); err != nil {
			return nil, fmt.Errorf("wait for local host CPU sample: %w", err)
		}
	}
	return samples, nil
}

func readLocalHostCPUBusy(ctx context.Context, dependencies localHostAdmissionSamplerDependencies) (float64, error) {
	output, err := runLocalHostCommand(ctx, dependencies, localHostTopPath, "-l", "1", "-n", "0")
	if err != nil {
		return 0, fmt.Errorf("read local host CPU: %w", err)
	}
	return parseLocalHostCPUBusy(output)
}

func runLocalHostCommand(ctx context.Context, dependencies localHostAdmissionSamplerDependencies, command string, args ...string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	commandCtx, cancel := dependencies.withTimeout(ctx, localHostCommandTimeout)
	defer cancel()
	output, err := dependencies.run(commandCtx, command, args...)
	if err != nil {
		return "", fmt.Errorf("run %s: %w", command, err)
	}
	if err := commandCtx.Err(); err != nil {
		return "", err
	}
	return output, nil
}

func parsePositiveLocalHostUint(output, name string) (uint64, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(output), 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("local host %s output is invalid", name)
	}
	return value, nil
}

// parseLocalHostFreeMemoryPercent 仅从 memory_pressure 的唯一目标行提取 (0, 100] 空闲百分比；
// 缺失、重复、非有限或越界值均立即失败，避免把未知内存状态当作可用容量。
func parseLocalHostFreeMemoryPercent(output string) (float64, error) {
	match, err := exactlyOneLocalHostOutputMatch(output, "System-wide memory free percentage:", localHostMemoryLinePattern)
	if err != nil {
		return 0, fmt.Errorf("local host memory_pressure output: %w", err)
	}
	value, err := strconv.ParseFloat(match[1], 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 || value > 100 {
		return 0, errors.New("local host memory_pressure output contains an invalid free percentage")
	}
	return value, nil
}

// parseLocalHostCPUBusy 仅接受 top 唯一 CPU usage 行中受约束的 user、system、idle 百分比并计算忙碌度；
// 非数值、越界或总和偏差均立即失败，杜绝宽松 CPU 估计。
func parseLocalHostCPUBusy(output string) (float64, error) {
	match, err := exactlyOneLocalHostOutputMatch(output, "CPU usage:", localHostCPULinePattern)
	if err != nil {
		return 0, fmt.Errorf("local host CPU output: %w", err)
	}
	user, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, errors.New("local host CPU output contains an invalid user value")
	}
	system, err := strconv.ParseFloat(match[2], 64)
	if err != nil {
		return 0, errors.New("local host CPU output contains an invalid system value")
	}
	idle, err := strconv.ParseFloat(match[3], 64)
	if err != nil {
		return 0, errors.New("local host CPU output contains an invalid idle value")
	}
	if !validLocalHostPercentage(user) || !validLocalHostPercentage(system) || !validLocalHostPercentage(idle) || math.Abs(user+system+idle-100) > localHostCPUPercentageRoundingTolerance {
		return 0, errors.New("local host CPU output percentages are invalid")
	}
	return user + system, nil
}

func exactlyOneLocalHostOutputMatch(output, prefix string, pattern *regexp.Regexp) ([]string, error) {
	var matches [][]string
	for line := range strings.SplitSeq(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		match := pattern.FindStringSubmatch(line)
		if match == nil {
			return nil, errors.New("unexpected format")
		}
		matches = append(matches, match)
	}
	if len(matches) != 1 {
		return nil, errors.New("expected exactly one matching line")
	}
	return matches[0], nil
}

func validLocalHostPercentage(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func weightedLocalHostCPUBusy(samples []gatecontract.LocalHostCPUSample) (float64, error) {
	if len(samples) < 2 {
		return 0, errors.New("local host CPU sampler returned insufficient samples")
	}
	var busyDuration, totalDuration float64
	for index := 1; index < len(samples); index++ {
		interval := samples[index].At.Sub(samples[index-1].At).Seconds()
		if interval <= 0 {
			return 0, errors.New("local host CPU sampler timestamps are invalid")
		}
		busyDuration += samples[index].BusyPercent * interval
		totalDuration += interval
	}
	return busyDuration / totalDuration, nil
}

func localHostAvailableCapacity(logicalCPU float64, totalMemoryBytes uint64, freeMemoryPercent, averageBusy float64) (float64, float64) {
	availableCPU := logicalCPU * (1 - averageBusy/100)
	availableMemoryGiB := float64(totalMemoryBytes) / float64(localHostGiB) * freeMemoryPercent / 100
	return availableCPU, availableMemoryGiB
}

func runLocalHostSystemCommand(ctx context.Context, command string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, command, args...).Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

func sleepForLocalHostSample(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
