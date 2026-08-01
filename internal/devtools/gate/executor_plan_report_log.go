package gate

import (
	"bytes"
	"sync"
	"unicode/utf8"
)

type boundedPlanLog struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

// newBoundedPlanLog 创建只保留末尾诊断窗口的并发安全日志。
func newBoundedPlanLog(limit int) *boundedPlanLog { return &boundedPlanLog{limit: limit} }

// Write 保留输入长度语义，并以固定内存保留最新日志字节。
func (log *boundedPlanLog) Write(value []byte) (int, error) {
	log.mu.Lock()
	defer log.mu.Unlock()
	written := len(value)
	if log.limit <= 0 {
		return written, nil
	}
	if len(value) >= log.limit {
		log.data = append(log.data[:0], value[len(value)-log.limit:]...)
		return written, nil
	}
	overflow := len(log.data) + len(value) - log.limit
	if overflow > 0 {
		copy(log.data, log.data[overflow:])
		log.data = log.data[:len(log.data)-overflow]
	}
	log.data = append(log.data, value...)
	return written, nil
}

// Bytes 返回最多固定行数的最新 UTF-8 诊断窗口。
func (log *boundedPlanLog) Bytes() []byte {
	log.mu.Lock()
	defer log.mu.Unlock()
	data := normalizePlanLogText(log.data, log.limit)
	return tailPlanLogLines(data, executorPlanMaxLogLines)
}

// normalizePlanLogText 将任意子进程输出转成有界、无 NUL 的 UTF-8 诊断文本。
func normalizePlanLogText(data []byte, limit int) []byte {
	data = bytes.ToValidUTF8(data, []byte("\uFFFD"))
	data = bytes.ReplaceAll(data, []byte{0}, []byte(`\x00`))
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	for len(data) > 0 && !utf8.RuneStart(data[0]) {
		data = data[1:]
	}
	return data
}

// tailPlanLogLines 返回不超过给定行数的日志尾部。
func tailPlanLogLines(data []byte, limit int) []byte {
	if len(data) == 0 || limit <= 0 {
		return nil
	}
	lineCount := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		lineCount++
	}
	drop := lineCount - limit
	if drop <= 0 {
		return data
	}
	for index, value := range data {
		if value != '\n' {
			continue
		}
		drop--
		if drop == 0 {
			return data[index+1:]
		}
	}
	return data
}
