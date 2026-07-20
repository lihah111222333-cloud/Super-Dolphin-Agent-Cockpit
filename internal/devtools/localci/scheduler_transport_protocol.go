package localci

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	schedulerProtocolVersion      = 2
	schedulerMaxFrameBytes        = 1 << 20
	schedulerMaxRequestIDBytes    = 128
	schedulerMinRequestIDBytes    = 16
	schedulerReplayWindowCapacity = 4096

	schedulerMethodEnqueue            = "enqueue"
	schedulerMethodReserve            = "reserve"
	schedulerMethodComplete           = "complete"
	schedulerMethodState              = "state"
	schedulerMethodSnapshot           = "snapshot"
	schedulerMethodReportShardFailure = "report_shard_failure"
	schedulerMethodCompleteGroup      = "complete_group"
	schedulerMethodCancelGroup        = "cancel_group"
)

var (
	// ErrSchedulerTransport 表示 owner socket 连接或 I/O 失败。
	ErrSchedulerTransport = errors.New("scheduler transport failed")
	// ErrSchedulerProtocol 表示 peer 违反版本化 framing 或 JSON 契约。
	ErrSchedulerProtocol = errors.New("scheduler protocol violation")
	// ErrSchedulerReplay 表示 request ID 已在当前 owner 的有界窗口内使用。
	ErrSchedulerReplay = errors.New("scheduler request replayed")
)

type schedulerWireRequest struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id"`
	DaemonKey string          `json:"daemon_key"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
}

type schedulerWireResponse struct {
	Version   int                 `json:"version"`
	RequestID string              `json:"request_id"`
	Result    json.RawMessage     `json:"result,omitempty"`
	Error     *schedulerWireError `json:"error,omitempty"`
}

type schedulerWireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type schedulerEmptyParams struct{}

type schedulerEnqueueParams struct {
	Request WorkloadRequest `json:"request"`
}

type schedulerCompleteParams struct {
	WorkloadID string         `json:"workload_id"`
	Status     WorkloadStatus `json:"status"`
}

type schedulerGroupParams struct {
	WorkloadID    string         `json:"workload_id"`
	GroupIdentity string         `json:"group_identity"`
	Status        WorkloadStatus `json:"status"`
}

type schedulerShardFailureParams struct {
	WorkloadID    string `json:"workload_id"`
	GroupIdentity string `json:"group_identity"`
	ShardIdentity string `json:"shard_identity"`
}

type schedulerShardFailureResult struct {
	CancelShardIdentities []string `json:"cancel_shard_identities"`
}

type schedulerStateParams struct {
	WorkloadID string `json:"workload_id"`
}

type schedulerReserveResult struct {
	Reservations []WorkloadReservation `json:"reservations"`
}

type schedulerStateResult struct {
	Status WorkloadStatus `json:"status"`
}

type schedulerSnapshotResult struct {
	Snapshot SchedulerSnapshot `json:"snapshot"`
}

func readSchedulerFrame(reader io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length == 0 || length > schedulerMaxFrameBytes {
		return nil, fmt.Errorf("%w: frame length %d is outside 1..%d", ErrSchedulerProtocol, length, schedulerMaxFrameBytes)
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, fmt.Errorf("%w: read frame payload: %w", ErrSchedulerProtocol, err)
	}
	return payload, nil
}

// writeSchedulerFrame 编码并完整写入一条受长度限制的 frame。
func writeSchedulerFrame(writer io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: encode frame: %w", ErrSchedulerProtocol, err)
	}
	if len(payload) == 0 || len(payload) > schedulerMaxFrameBytes {
		return fmt.Errorf("%w: encoded frame length %d is outside 1..%d", ErrSchedulerProtocol, len(payload), schedulerMaxFrameBytes)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(payload)))
	frame := make([]byte, 0, len(header)+len(payload))
	frame = append(frame, header[:]...)
	frame = append(frame, payload...)
	written, err := io.Copy(writer, bytes.NewReader(frame))
	if err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	if written != int64(len(frame)) {
		return fmt.Errorf("write frame: wrote %d bytes, want %d", written, len(frame))
	}
	return nil
}

func decodeStrictSchedulerJSON(payload []byte, target any) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: JSON payload is empty", ErrSchedulerProtocol)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode JSON: %w", ErrSchedulerProtocol, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrSchedulerProtocol)
		}
		return fmt.Errorf("%w: decode trailing JSON: %w", ErrSchedulerProtocol, err)
	}
	return nil
}

func decodeStrictSchedulerObject(payload []byte, target any) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return fmt.Errorf("%w: params must be one JSON object", ErrSchedulerProtocol)
	}
	return decodeStrictSchedulerJSON(payload, target)
}

// validateSchedulerWireRequest 在 params 解码前锁定版本、身份与 method。
func validateSchedulerWireRequest(request schedulerWireRequest, daemonKey string) error {
	if request.Version != schedulerProtocolVersion {
		return fmt.Errorf("%w: version %d is unsupported", ErrSchedulerProtocol, request.Version)
	}
	if err := validateSchedulerRequestID(request.RequestID); err != nil {
		return err
	}
	if request.DaemonKey == "" || request.DaemonKey != daemonKey {
		return fmt.Errorf("%w: daemon identity key mismatch", ErrSchedulerProtocol)
	}
	if !validSchedulerMethod(request.Method) {
		return fmt.Errorf("%w: method %q is unsupported", ErrSchedulerProtocol, request.Method)
	}
	if len(request.Params) == 0 {
		return fmt.Errorf("%w: params are required", ErrSchedulerProtocol)
	}
	return nil
}

// validateSchedulerRequestID 限制 replay key 的长度与字符集。
func validateSchedulerRequestID(requestID string) error {
	if len(requestID) < schedulerMinRequestIDBytes || len(requestID) > schedulerMaxRequestIDBytes {
		return fmt.Errorf("%w: request ID length is outside %d..%d", ErrSchedulerProtocol, schedulerMinRequestIDBytes, schedulerMaxRequestIDBytes)
	}
	if strings.TrimSpace(requestID) != requestID {
		return fmt.Errorf("%w: request ID must be canonical", ErrSchedulerProtocol)
	}
	for _, character := range requestID {
		if !validSchedulerRequestIDCharacter(character) {
			return fmt.Errorf("%w: request ID contains an invalid character", ErrSchedulerProtocol)
		}
	}
	return nil
}

// validSchedulerRequestIDCharacter 只接受可跨 JSON 与日志安全传递的 ASCII 字符。
func validSchedulerRequestIDCharacter(character rune) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
		(character >= '0' && character <= '9') || character == '-' || character == '_'
}

func validSchedulerMethod(method string) bool {
	switch method {
	case schedulerMethodEnqueue, schedulerMethodReserve, schedulerMethodComplete,
		schedulerMethodState, schedulerMethodSnapshot, schedulerMethodReportShardFailure,
		schedulerMethodCompleteGroup, schedulerMethodCancelGroup:
		return true
	default:
		return false
	}
}

// schedulerWireErrorFor 将内部错误压缩为稳定且不泄露根因的 wire error。
func schedulerWireErrorFor(err error) *schedulerWireError {
	switch {
	case errors.Is(err, ErrSchedulerReplay):
		return &schedulerWireError{Code: "replay", Message: "request ID was already used"}
	case errors.Is(err, ErrSchedulerProtocol):
		return &schedulerWireError{Code: "protocol", Message: "request violates scheduler protocol"}
	case errors.Is(err, ErrInvalidSchedulerInput):
		return &schedulerWireError{Code: "invalid_input", Message: "scheduler input is invalid"}
	case errors.Is(err, ErrSchedulerState):
		return &schedulerWireError{Code: "invalid_state", Message: "scheduler state rejects the operation"}
	case errors.Is(err, ErrWorkloadNotFound):
		return &schedulerWireError{Code: "not_found", Message: "workload was not found"}
	case errors.Is(err, ErrSchedulerPersistence):
		return &schedulerWireError{Code: "persistence", Message: "scheduler persistence failed"}
	case errors.Is(err, ErrSchedulerClosed):
		return &schedulerWireError{Code: "closed", Message: "scheduler owner is closed"}
	default:
		return &schedulerWireError{Code: "internal", Message: "scheduler owner failed"}
	}
}

// schedulerErrorFromWire 把稳定 wire code 恢复为可 errors.Is 的 facade 错误。
func schedulerErrorFromWire(wireError *schedulerWireError) error {
	if wireError == nil || strings.TrimSpace(wireError.Code) == "" || strings.TrimSpace(wireError.Message) == "" {
		return fmt.Errorf("%w: malformed remote error", ErrSchedulerProtocol)
	}
	sentinel, ok := schedulerSentinelForWireCode(wireError.Code)
	if !ok {
		return fmt.Errorf("%w: unknown remote error code %q", ErrSchedulerProtocol, wireError.Code)
	}
	return fmt.Errorf("%w: %s", sentinel, wireError.Message)
}

// schedulerSentinelForWireCode 显式登记全部可公开 wire error code。
func schedulerSentinelForWireCode(code string) (error, bool) {
	switch code {
	case "replay":
		return ErrSchedulerReplay, true
	case "protocol":
		return ErrSchedulerProtocol, true
	case "invalid_input":
		return ErrInvalidSchedulerInput, true
	case "invalid_state":
		return ErrSchedulerState, true
	case "not_found":
		return ErrWorkloadNotFound, true
	case "persistence":
		return ErrSchedulerPersistence, true
	case "closed":
		return ErrSchedulerClosed, true
	case "internal":
		return ErrSchedulerTransport, true
	default:
		return nil, false
	}
}
