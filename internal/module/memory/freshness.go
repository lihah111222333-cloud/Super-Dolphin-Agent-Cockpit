package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func memoryAgeDays(now, updatedAt time.Time) int {
	if updatedAt.IsZero() {
		return -1
	}
	loc := now.Location()
	if loc == nil {
		loc = time.UTC
	}
	nowDay := time.Date(now.In(loc).Year(), now.In(loc).Month(), now.In(loc).Day(), 0, 0, 0, 0, loc)
	savedDay := time.Date(updatedAt.In(loc).Year(), updatedAt.In(loc).Month(), updatedAt.In(loc).Day(), 0, 0, 0, 0, loc)
	if savedDay.After(nowDay) {
		return 0
	}
	return int(nowDay.Sub(savedDay).Hours() / 24)
}

func memoryAge(now, updatedAt time.Time) string {
	switch days := memoryAgeDays(now, updatedAt); {
	case days < 0:
		return ""
	case days == 0:
		return "today"
	case days == 1:
		return "yesterday"
	case days == 2:
		return "2 days ago"
	default:
		return fmt.Sprintf("%d days ago", days)
	}
}

func memoryFreshnessText(now, updatedAt time.Time) string {
	if memoryAgeDays(now, updatedAt) <= 1 {
		return ""
	}
	age := memoryAge(now, updatedAt)
	if age == "" {
		age = "some time ago"
	}
	return "This memory was saved " + age + ", so it may not reflect live state. File or line references may be outdated; verify the current code before relying on it."
}

func memoryHeader(now time.Time, entry MemoryEntry) string {
	path := memoryDisplayPath(entry)
	switch memoryAgeDays(now, entry.UpdatedAt) {
	case 0:
		return "Memory (saved today): " + path + ":"
	case 1:
		return "Memory (saved yesterday): " + path + ":"
	}
	if warning := memoryFreshnessText(now, entry.UpdatedAt); warning != "" {
		return warning + "\n\nMemory: " + path + ":"
	}
	return "Memory: " + path + ":"
}

func memoryDisplayPath(entry MemoryEntry) string {
	if path := strings.TrimSpace(filepath.ToSlash(entry.FilePath)); path != "" {
		return path
	}
	if name := strings.TrimSpace(entry.Frontmatter.Name); name != "" {
		return name
	}
	if base := strings.TrimSpace(strings.TrimSuffix(filepath.Base(entry.FilePath), filepath.Ext(entry.FilePath))); base != "" {
		return base
	}
	return "memory note"
}
