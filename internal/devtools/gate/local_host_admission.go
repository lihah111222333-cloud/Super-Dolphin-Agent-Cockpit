package gate

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// LocalHostCPUSample 是一个按时间间隔统计的 CPU busy 观测；它只用于准入审计，不进入 PASS key。
type LocalHostCPUSample struct {
	At          time.Time
	BusyPercent float64
}

// LocalHostAdmissionSampler 由 CLI 注入，确保命中复用不采样、不消耗主机资源。
type LocalHostAdmissionSampler func(context.Context) (LocalHostAdmission, error)

// ValidateLocalHostAdmissionObservation 校验可审计 CPU 窗口与容量；平均占用超过 70% 仍是可记录但不可准入的观测。
func ValidateLocalHostAdmissionObservation(admission LocalHostAdmission) error {
	if err := validateLocalHostWindow(admission); err != nil {
		return err
	}
	if err := validateLocalHostBusyAverage(admission.CPUBusyAveragePercent); err != nil {
		return err
	}
	return validateLocalHostCapacity(admission.AvailableCPU, admission.AvailableMemoryGiB)
}

// BuildLocalHostAdmissionFromSamples 以至少 7 个间隔快照构造确定性的 30 秒观测，生产采样与单测均可复用。
func BuildLocalHostAdmissionFromSamples(samples []LocalHostCPUSample, availableCPU, availableMemoryGiB float64) (LocalHostAdmission, error) {
	if len(samples) < int(cicontract.LocalHostMinimumCPUSamples) {
		return LocalHostAdmission{}, errors.New("local host CPU sampler returned insufficient samples")
	}
	ordered := append([]LocalHostCPUSample(nil), samples...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].At.Before(ordered[right].At) })
	if err := validateLocalHostCPUSamples(ordered); err != nil {
		return LocalHostAdmission{}, err
	}
	start, end := ordered[0].At.UTC(), ordered[len(ordered)-1].At.UTC()
	if end.Sub(start) < time.Duration(cicontract.LocalHostCPUWindowMS)*time.Millisecond {
		return LocalHostAdmission{}, errors.New("local host CPU sampler window is shorter than 30 seconds")
	}
	if err := validateLocalHostCapacity(availableCPU, availableMemoryGiB); err != nil {
		return LocalHostAdmission{}, err
	}
	var busyDuration, totalDuration float64
	for index := 1; index < len(ordered); index++ {
		interval := ordered[index].At.Sub(ordered[index-1].At).Seconds()
		busyDuration += ordered[index].BusyPercent * interval
		totalDuration += interval
	}
	if totalDuration <= 0 {
		return LocalHostAdmission{}, errors.New("local host CPU sampler intervals are invalid")
	}
	average := busyDuration / totalDuration
	return LocalHostAdmission{
		Allowed:               average <= cicontract.LocalHostCPUBusyLimitPercent,
		AvailableCPU:          availableCPU,
		AvailableMemoryGiB:    availableMemoryGiB,
		CPUWindowStart:        start,
		CPUWindowEnd:          end,
		CPUSampleCount:        len(ordered),
		CPUBusyAveragePercent: average,
	}, nil
}

// SampleLocalHostAdmission 调用注入 sampler；scheduler 在 namespace lookup 后才调用，因此 exact hit 零采样。
func SampleLocalHostAdmission(ctx context.Context, sampler LocalHostAdmissionSampler) (LocalHostAdmission, error) {
	if ctx == nil || sampler == nil {
		return LocalHostAdmission{}, errors.New("local host admission sampler is required")
	}
	admission, err := sampler(ctx)
	if err != nil {
		return LocalHostAdmission{}, fmt.Errorf("sample local host admission: %w", err)
	}
	if err := ValidateLocalHostAdmissionObservation(admission); err != nil {
		return LocalHostAdmission{}, err
	}
	return admission, nil
}

func localHostCPUHardAdmitted(admission LocalHostAdmission) bool {
	return ValidateLocalHostAdmissionObservation(admission) == nil && admission.CPUBusyAveragePercent <= cicontract.LocalHostCPUBusyLimitPercent
}

// validateLocalHostWindow 校验窗口覆盖至少 30 秒且采样数量足够。
func validateLocalHostWindow(admission LocalHostAdmission) error {
	if admission.CPUWindowStart.IsZero() || admission.CPUWindowEnd.IsZero() || admission.CPUWindowEnd.Before(admission.CPUWindowStart) {
		return errors.New("local host CPU observation window is invalid")
	}
	if admission.CPUWindowEnd.Sub(admission.CPUWindowStart) < time.Duration(cicontract.LocalHostCPUWindowMS)*time.Millisecond {
		return errors.New("local host CPU observation window is shorter than 30 seconds")
	}
	if admission.CPUSampleCount < int(cicontract.LocalHostMinimumCPUSamples) {
		return errors.New("local host CPU observation has insufficient samples")
	}
	return nil
}

func validateLocalHostBusyAverage(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return errors.New("local host CPU busy average is outside 0..100")
	}
	return nil
}

// validateLocalHostCapacity 校验本机可用 CPU 与内存为有限正数。
func validateLocalHostCapacity(availableCPU, availableMemoryGiB float64) error {
	if math.IsNaN(availableCPU) || math.IsInf(availableCPU, 0) || availableCPU <= 0 || math.IsNaN(availableMemoryGiB) || math.IsInf(availableMemoryGiB, 0) || availableMemoryGiB <= 0 {
		return errors.New("local host CPU sampler capacity is invalid")
	}
	return nil
}

// validateLocalHostCPUSamples 校验 interval snapshot 时间递增且 busy 值有限。
func validateLocalHostCPUSamples(samples []LocalHostCPUSample) error {
	for index, sample := range samples {
		if sample.At.IsZero() || math.IsNaN(sample.BusyPercent) || math.IsInf(sample.BusyPercent, 0) || sample.BusyPercent < 0 || sample.BusyPercent > 100 {
			return errors.New("local host CPU sampler returned an invalid sample")
		}
		if index > 0 && !sample.At.After(samples[index-1].At) {
			return errors.New("local host CPU sampler timestamps are not increasing")
		}
	}
	return nil
}
