//go:build darwin

package appupdatefailure

import (
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

const (
	maxSize      = 4096
	statePending = "pending"
	stateFailure = "failure"
)

var errGenerationMissing = errors.New("app update pre-journal generation does not match an active attempt")

type record struct {
	Version       int    `json:"version"`
	Generation    string `json:"generation"`
	State         string `json:"state"`
	Code          string `json:"code"`
	Retryable     bool   `json:"retryable"`
	Action        string `json:"action"`
	TransactionID string `json:"transaction_id"`
}

func pendingRecord(generation string) record {
	return record{Version: Version, Generation: generation, State: statePending}
}

func failureRecord(generation string, failure contract.RecoveryFailure) record {
	return record{Version: Version, Generation: generation, State: stateFailure, Code: failure.Code, Retryable: failure.Retryable, Action: string(failure.Action), TransactionID: failure.TransactionID}
}

// validateRecord 校验版本、代际、状态和恢复字段的完整一致性。
func validateRecord(value record) error {
	if value.Version != Version || validateGeneration(value.Generation) != nil || value.TransactionID != "" {
		return errors.New("app update failure sidecar record is invalid")
	}
	if value.State == statePending {
		if value.Code != "" || value.Retryable || value.Action != "" {
			return errors.New("app update failure sidecar pending record is inconsistent")
		}
		return nil
	}
	if value.State != stateFailure {
		return errors.New("app update failure sidecar state is unsupported")
	}
	return validateFailure(contract.RecoveryFailure{Code: value.Code, Retryable: value.Retryable, Action: contract.RecoveryAction(value.Action), TransactionID: value.TransactionID})
}
