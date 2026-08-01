package testtiming

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	maxNameBytes = 1024
	// LogPrefix 固定每个 test/subtest 的普通文本耗时记录前缀。
	LogPrefix = "SUPER_DOLPHIN_CI_TEST_TIMING "
)

// Status 是 go test -json 的单测试终态。
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusSkip Status = "skip"
)

// Timing 保存一个顶层测试或子测试的实际耗时。
type Timing struct {
	Name       string `json:"name"`
	Status     Status `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

type jsonEvent struct {
	Action  string  `json:"Action"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// EventWriter 将 go test JSON 事件还原为普通日志，并独立保存测试级耗时。
type EventWriter struct {
	mu          sync.Mutex
	destination io.Writer
	pending     []byte
	timings     []Timing
	seen        map[string]struct{}
	terminalErr error
}

// NewEventWriter 构造一个 fail-fast 的 go test JSON 流处理器。
func NewEventWriter(destination io.Writer) *EventWriter {
	return &EventWriter{destination: destination, seen: make(map[string]struct{})}
}

// Write 消费完整的换行分隔 go test JSON 事件。
func (writer *EventWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.terminalErr != nil {
		return 0, writer.terminalErr
	}
	writer.pending = append(writer.pending, data...)
	for {
		end := bytes.IndexByte(writer.pending, '\n')
		if end < 0 {
			return len(data), nil
		}
		line := slices.Clone(writer.pending[:end+1])
		writer.pending = append(writer.pending[:0], writer.pending[end+1:]...)
		if err := writer.consumeLine(line); err != nil {
			writer.terminalErr = err
			return 0, err
		}
	}
}

// consumeLine 转发普通输出，并把终态 JSON 事件记录为唯一测试耗时。
func (writer *EventWriter) consumeLine(line []byte) error {
	trimmed := bytes.TrimSuffix(line, []byte{'\n'})
	if !bytes.HasPrefix(trimmed, []byte{'{'}) {
		_, err := writer.destination.Write(line)
		return err
	}
	var event jsonEvent
	if err := json.Unmarshal(trimmed, &event); err != nil {
		return fmt.Errorf("decode go test timing event: %w", err)
	}
	if event.Output != "" {
		if _, err := io.WriteString(writer.destination, event.Output); err != nil {
			return err
		}
	}
	if event.Test == "" || !isTerminalStatus(Status(event.Action)) {
		return nil
	}
	timing, err := timingFromEvent(event)
	if err != nil {
		return err
	}
	if _, duplicate := writer.seen[timing.Name]; duplicate {
		return fmt.Errorf("go test timing %q has duplicate terminal events", timing.Name)
	}
	writer.seen[timing.Name] = struct{}{}
	writer.timings = append(writer.timings, timing)
	_, err = fmt.Fprintf(
		writer.destination,
		"%sname=%s status=%s duration_ms=%d\n",
		LogPrefix,
		timing.Name,
		timing.Status,
		timing.DurationMS,
	)
	return err
}

// timingFromEvent 将 go test 终态事件转换为有界毫秒耗时。
func timingFromEvent(event jsonEvent) (Timing, error) {
	duration := event.Elapsed * 1000
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 || duration > float64(math.MaxInt64) {
		return Timing{}, errors.New("go test timing duration is invalid")
	}
	timing := Timing{
		Name:       event.Test,
		Status:     Status(event.Action),
		DurationMS: max(1, int64(math.Round(duration))),
	}
	if err := Validate(timing); err != nil {
		return Timing{}, err
	}
	return timing, nil
}

// Close 拒绝末尾不完整的 JSON 事件。
func (writer *EventWriter) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.terminalErr != nil {
		return writer.terminalErr
	}
	if len(writer.pending) == 0 {
		return nil
	}
	if bytes.HasPrefix(writer.pending, []byte{'{'}) {
		return errors.New("go test timing stream ended with an incomplete JSON event")
	}
	_, err := writer.destination.Write(writer.pending)
	writer.pending = nil
	return err
}

// Timings 返回当前完整终态事件的只读副本。
func (writer *EventWriter) Timings() []Timing {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return slices.Clone(writer.timings)
}

// Validate 校验一条测试级耗时记录。
func Validate(timing Timing) error {
	if timing.Name == "" || len(timing.Name) > maxNameBytes ||
		!utf8.ValidString(timing.Name) || strings.TrimSpace(timing.Name) != timing.Name ||
		len(strings.Fields(timing.Name)) != 1 || strings.ContainsRune(timing.Name, 0) {
		return errors.New("go test timing name is invalid")
	}
	if !isTerminalStatus(timing.Status) {
		return errors.New("go test timing status is invalid")
	}
	if timing.DurationMS <= 0 {
		return errors.New("go test timing duration must be positive")
	}
	return nil
}

// ValidateList 校验有界、无重复的完整测试级耗时集合。
func ValidateList(timings []Timing, maxCount int) error {
	if maxCount <= 0 || len(timings) > maxCount {
		return errors.New("go test timing count is invalid")
	}
	seen := make(map[string]struct{}, len(timings))
	for _, timing := range timings {
		if err := Validate(timing); err != nil {
			return err
		}
		if _, duplicate := seen[timing.Name]; duplicate {
			return fmt.Errorf("go test timing %q is duplicated", timing.Name)
		}
		seen[timing.Name] = struct{}{}
	}
	return nil
}

func isTerminalStatus(status Status) bool {
	switch status {
	case StatusPass, StatusFail, StatusSkip:
		return true
	default:
		return false
	}
}
