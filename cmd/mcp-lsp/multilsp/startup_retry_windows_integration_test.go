//go:build windows

package multilsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

const windows122RealHelperEnv = "MCP_LSP_WINDOWS122_REAL_TRANSPORT_HELPER"

func TestWindows122RealTransportRotation(t *testing.T) {
	if mode := os.Getenv(windows122RealHelperEnv); mode != "" {
		windows122RealTransportHelper(mode)
		return
	}

	first, err := NewClientWithOptions(Options{
		Binary: os.Args[0],
		Args:   []string{"-test.run=^TestWindows122RealTransportRotation$", "-test.count=1"},
		Env:    []string{windows122RealHelperEnv + "=first"},
	})
	if err != nil {
		t.Fatalf("create first real transport: %v", err)
	}
	firstConcrete, ok := concreteClient(first)
	if !ok || firstConcrete.transport == nil || firstConcrete.transport.cmd == nil || firstConcrete.transport.cmd.Process == nil {
		t.Fatal("first real transport has no concrete process")
	}
	oldPID := firstConcrete.transport.cmd.Process.Pid
	oldStart, err := hiddenexec.ProcessStartIdentity(oldPID)
	if err != nil {
		t.Fatalf("capture first process identity: %v", err)
	}
	wrapped := &windows122WrappedClient{underlying: first}

	var restartCount, cleanupCount int
	var replacement Client
	got, err := initializeClientWithWindows122Retry(
		context.Background(), wrapped,
		func(candidate Client) error {
			concrete, ok := concreteClient(candidate)
			if !ok {
				return fmt.Errorf("real test wrapper did not expose concrete client")
			}
			return concrete.Initialize(context.Background(), "")
		},
		func() (Client, error) {
			restartCount++
			replacement, err = NewClientWithOptions(Options{
				Binary: os.Args[0],
				Args:   []string{"-test.run=^TestWindows122RealTransportRotation$", "-test.count=1"},
				Env:    []string{windows122RealHelperEnv + "=second"},
			})
			return replacement, err
		},
		func(candidate Client) error {
			cleanupCount++
			concrete, ok := concreteClient(candidate)
			if !ok {
				return fmt.Errorf("cleanup candidate did not expose concrete client")
			}
			if err := concrete.Close(); err != nil {
				return err
			}
			if firstConcrete.transport.cmd.ProcessState == nil || !firstConcrete.transport.cmd.ProcessState.Exited() {
				return fmt.Errorf("old process PID %d did not exit before replacement", oldPID)
			}
			deadline := time.Now().Add(2 * time.Second)
			for {
				current, identityErr := hiddenexec.ProcessStartIdentity(oldPID)
				if identityErr != nil || current != oldStart {
					break
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("old process PID %d/start identity remained present", oldPID)
				}
				time.Sleep(10 * time.Millisecond)
			}
			return nil
		},
	)
	if err != nil {
		if replacement != nil {
			_ = closeWindows122TestClient(replacement)
		}
		t.Fatalf("real transport rotation: %v", err)
	}
	if got != replacement || restartCount != 1 || cleanupCount != 1 {
		if replacement != nil {
			_ = closeWindows122TestClient(replacement)
		}
		t.Fatalf("got=%p replacement=%p restart=%d cleanup=%d, want replacement and 1/1", got, replacement, restartCount, cleanupCount)
	}
	if err := closeWindows122TestClient(replacement); err != nil {
		t.Fatalf("close replacement transport: %v", err)
	}
}

func closeWindows122TestClient(candidate Client) error {
	concrete, ok := concreteClient(candidate)
	if !ok {
		return fmt.Errorf("test client does not expose concrete client")
	}
	return concrete.Close()
}

func windows122RealTransportHelper(mode string) {
	if mode == "first" {
		reader := bufio.NewReader(os.Stdin)
		_, _ = reader.ReadString('\n')
		_, _ = io.WriteString(os.Stderr, "clangd: The data area passed to a system call is too small.\n")
		os.Exit(0)
	}
	if mode != "second" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	request, err := windows122ReadMessage(reader)
	if err != nil {
		return
	}
	var envelope struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(request, &envelope) != nil || len(envelope.ID) == 0 {
		return
	}
	response := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: "2.0", ID: envelope.ID, Result: json.RawMessage(`{"capabilities":{}}`)}
	payload, _ := json.Marshal(response)
	_, _ = fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(payload), payload)
	// The helper only needs to prove initialize transport success; the test closes
	// the replacement exact owner after the response is consumed.
	os.Exit(0)
}

func windows122ReadMessage(reader *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
			length, err = strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, err
			}
		}
	}
	if length < 0 || length > 1<<20 {
		return nil, fmt.Errorf("invalid helper content length %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(payload), nil
}
