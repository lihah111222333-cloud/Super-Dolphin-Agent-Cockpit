package eci

import (
	"encoding/json"
	"fmt"
	"time"
)

// UnmarshalJSON 接受 ECI 对非适用终态时间返回的空字符串，并严格解析所有非空时间。
func (group *ContainerGroup) UnmarshalJSON(data []byte) error {
	type containerGroupAlias ContainerGroup
	wire := struct {
		*containerGroupAlias
		CreationTime  string `json:"CreationTime"`
		SucceededTime string `json:"SucceededTime"`
		FailedTime    string `json:"FailedTime"`
	}{containerGroupAlias: (*containerGroupAlias)(group)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	parsed, err := parseECITimestamp("CreationTime", wire.CreationTime)
	if err != nil {
		return err
	}
	group.CreationTime = parsed
	parsed, err = parseECITimestamp("SucceededTime", wire.SucceededTime)
	if err != nil {
		return err
	}
	group.SucceededTime = parsed
	parsed, err = parseECITimestamp("FailedTime", wire.FailedTime)
	if err != nil {
		return err
	}
	group.FailedTime = parsed
	return nil
}

// UnmarshalJSON 接受运行中或等待中容器尚未产生的起止时间，并严格解析所有非空时间。
func (state *ContainerState) UnmarshalJSON(data []byte) error {
	type containerStateAlias ContainerState
	wire := struct {
		*containerStateAlias
		StartTime  string `json:"StartTime"`
		FinishTime string `json:"FinishTime"`
	}{containerStateAlias: (*containerStateAlias)(state)}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	parsed, err := parseECITimestamp("ContainerState.StartTime", wire.StartTime)
	if err != nil {
		return err
	}
	state.StartTime = parsed
	parsed, err = parseECITimestamp("ContainerState.FinishTime", wire.FinishTime)
	if err != nil {
		return err
	}
	state.FinishTime = parsed
	return nil
}

// parseECITimestamp 将非适用的空时间保留为零值，并拒绝非空畸形时间。
func parseECITimestamp(field string, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ECI %s: %w", field, err)
	}
	return parsed, nil
}
