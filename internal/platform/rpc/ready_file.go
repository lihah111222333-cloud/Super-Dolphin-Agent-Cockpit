package rpc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	controlRPCReadyFileEnv = "SUPER_DOLPHIN_RPC_READY_FILE"
	rpcReadyProcessRoleEnv = "SUPER_DOLPHIN_PROCESS_ROLE"
	rpcReadyEntrypointEnv  = "SUPER_DOLPHIN_ENTRYPOINT"
	rpcReadyDevEntrypoint  = "SUPER_DOLPHIN_TRUSTED_DEV_ENTRYPOINT"
)

type rpcReadyEvent struct {
	Event           string    `json:"event"`
	RPCAddr         string    `json:"rpc_addr"`
	SessionTokenEnv string    `json:"session_token_env"`
	PID             int       `json:"pid"`
	ProcessRole     string    `json:"process_role"`
	Entrypoint      string    `json:"entrypoint"`
	EmittedAt       time.Time `json:"emitted_at"`
}

func inheritedCanonicalControlRPCSessionToken() bool {
	return strings.TrimSpace(os.Getenv(controlRPCSessionTokenEnv)) != ""
}

func maybePublishRPCReadyFile(rpcAddr string, inheritedCanonicalSessionToken bool) error {
	path := rpcReadyFilePath()
	if path == "" {
		return nil
	}
	if err := requireRPCReadyFileInheritedSessionToken(inheritedCanonicalSessionToken); err != nil {
		return err
	}
	return publishRPCReadyFile(path, rpcAddr, time.Now())
}

func requireRPCReadyFileInheritedSessionToken(inheritedCanonicalSessionToken bool) error {
	if rpcReadyFilePath() == "" || inheritedCanonicalSessionToken {
		return nil
	}
	return ErrInvalidState(fmt.Sprintf("%s requires inherited canonical %s", controlRPCReadyFileEnv, controlRPCSessionTokenEnv))
}

func publishRPCReadyFile(path string, rpcAddr string, emittedAt time.Time) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return ErrInvalidState(fmt.Sprintf("%s is empty", controlRPCReadyFileEnv))
	}
	if !filepath.IsAbs(path) {
		return ErrInvalidState(fmt.Sprintf("%s must be an absolute path: %s", controlRPCReadyFileEnv, path))
	}
	payload, err := json.Marshal(rpcReadyPayload(rpcAddr, emittedAt))
	if err != nil {
		return ErrInvalidState(fmt.Sprintf("marshal rpc ready file: %v", err))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ErrInvalidState(fmt.Sprintf("create rpc ready file parent directory: %v", err))
	}
	return writeAtomicRPCReadyFile(path, append(payload, '\n'))
}

func rpcReadyPayload(rpcAddr string, emittedAt time.Time) rpcReadyEvent {
	return rpcReadyEvent{
		Event:           "core.ready",
		RPCAddr:         strings.TrimSpace(rpcAddr),
		SessionTokenEnv: controlRPCSessionTokenEnv,
		PID:             os.Getpid(),
		ProcessRole:     strings.TrimSpace(os.Getenv(rpcReadyProcessRoleEnv)),
		Entrypoint:      rpcReadyEntrypoint(),
		EmittedAt:       emittedAt.UTC(),
	}
}

func rpcReadyEntrypoint() string {
	if entrypoint := strings.TrimSpace(os.Getenv(rpcReadyEntrypointEnv)); entrypoint != "" {
		return entrypoint
	}
	return strings.TrimSpace(os.Getenv(rpcReadyDevEntrypoint))
}

func rpcReadyFilePath() string {
	return strings.TrimSpace(os.Getenv(controlRPCReadyFileEnv))
}

// writeAtomicRPCReadyFile 先写同目录临时文件，再用 rename 发布 ready-file。
func writeAtomicRPCReadyFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return ErrInvalidState(fmt.Sprintf("create rpc ready temp file: %v", err))
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return ErrInvalidState(fmt.Sprintf("write rpc ready temp file: %v", err))
	}
	if err := tmp.Close(); err != nil {
		return ErrInvalidState(fmt.Sprintf("close rpc ready temp file: %v", err))
	}
	if err := os.Rename(tmpName, path); err != nil {
		return ErrInvalidState(fmt.Sprintf("rename rpc ready file: %v", err))
	}
	removeTemp = false
	return nil
}
