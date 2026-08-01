package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// candidateTestBinaryActionGraphMetrics keeps command-wall and action-duration
// observations separate: compile/link are sums, while critical is a time union.
type candidateTestBinaryActionGraphMetrics struct {
	compileActionMS       uint64
	linkActionMS          uint64
	compileCriticalWallMS uint64
}

type candidateTestBinaryAction struct {
	Mode      string
	TimeStart time.Time
	TimeDone  time.Time
}

func readCandidateTestBinaryActionGraph(path string) (candidateTestBinaryActionGraphMetrics, error) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 || len(data) > 64<<20 {
		return candidateTestBinaryActionGraphMetrics{}, errors.New("candidate test action graph is unreadable")
	}
	var actions []candidateTestBinaryAction
	if err := json.Unmarshal(data, &actions); err != nil || len(actions) == 0 {
		return candidateTestBinaryActionGraphMetrics{}, fmt.Errorf("decode candidate test action graph: %w", err)
	}
	var ranges [][2]time.Time
	var metrics candidateTestBinaryActionGraphMetrics
	for _, action := range actions {
		if action.TimeStart.IsZero() || action.TimeDone.IsZero() || action.TimeDone.Before(action.TimeStart) {
			return candidateTestBinaryActionGraphMetrics{}, errors.New("candidate test action graph timing is invalid")
		}
		milliseconds := uint64(action.TimeDone.Sub(action.TimeStart).Milliseconds())
		switch action.Mode {
		case "compile":
			metrics.compileActionMS += milliseconds
			ranges = append(ranges, [2]time.Time{action.TimeStart, action.TimeDone})
		case "link":
			metrics.linkActionMS += milliseconds
		}
	}
	for len(ranges) > 0 {
		best := 0
		for index := range ranges {
			if ranges[index][0].Before(ranges[best][0]) {
				best = index
			}
		}
		current := ranges[best]
		ranges = append(ranges[:best], ranges[best+1:]...)
		for changed := true; changed; {
			changed = false
			for index := 0; index < len(ranges); index++ {
				if ranges[index][0].After(current[1]) {
					continue
				}
				if ranges[index][1].After(current[1]) {
					current[1] = ranges[index][1]
				}
				ranges = append(ranges[:index], ranges[index+1:]...)
				changed = true
				break
			}
		}
		metrics.compileCriticalWallMS += uint64(current[1].Sub(current[0]).Milliseconds())
	}
	return metrics, nil
}
