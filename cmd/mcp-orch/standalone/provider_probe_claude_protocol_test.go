package standalone

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func writeClaudeControlFixture(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return writeProbeFixture(t, "#!/bin/sh\nexport ORCH_CLAUDE_CONTROL_HELPER=1\nexec "+strconv.Quote(executable)+" -test.run=^TestClaudeControlProbeHelper$ -- \"$@\"\n")
}

// TestClaudeControlProbeHelper 检查真实 CLI argv 与请求字段，再动态回传关联 ID。
func TestClaudeControlProbeHelper(t *testing.T) {
	if os.Getenv("ORCH_CLAUDE_CONTROL_HELPER") != "1" {
		return
	}
	session, mode := claudeControlHelperArguments()
	requestID := readClaudeControlHelperRequest(session)
	if mode == "cancel" {
		time.Sleep(10 * time.Second)
		os.Exit(45)
	}
	if _, err := fmt.Fprintf(os.Stdout, "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":%q,\"response\":{\"commands\":[],\"models\":[],\"extension\":{\"ok\":true}}}}\n", requestID); err != nil {
		os.Exit(46)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		os.Exit(47)
	}
	os.Exit(0)
}

func TestClaudeProviderProbeControlContract(t *testing.T) {
	t.Run("shutdown-exit", TestClaudeProbeExpectedShutdownExitContract)
	t.Run("process", TestProbeConfiguredProvidersClaudeStreamJSON)
	t.Run("result-rejected", TestProbeConfiguredProvidersClaudeRejectsResultFrame)
	t.Run("request-field-chain", testClaudeProbeRequestFields)
	t.Run("response-field-chain", testClaudeProbeResponseFields)
	t.Run("negative-frames", testClaudeProbeNegativeFrames)
	t.Run("short-write", func(t *testing.T) {
		if err := writeClaudeProbeInitialize(shortClaudeProbeWriter{}, "request"); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("short write: %v", err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		requireUnixProbe(t)
		dir := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		cfg := probeConfig(dir, Provider{Name: "claude", Executable: writeClaudeControlFixture(t), Home: dir, Models: []string{"cancel"}})
		start := time.Now()
		if _, err := ProbeProviders(ctx, cfg); !errors.Is(err, ErrProviderProbe) {
			t.Fatalf("cancel accepted: %v", err)
		}
		if time.Since(start) > 4*time.Second {
			t.Fatal("cancel did not reclaim probe promptly")
		}
	})
}

type shortClaudeProbeWriter struct{}

func (shortClaudeProbeWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func testClaudeProbeRequestFields(t *testing.T) {
	var encoded bytes.Buffer
	if err := writeClaudeProbeInitialize(&encoded, "request-one-hot"); err != nil {
		t.Fatal(err)
	}
	var actual map[string]json.RawMessage
	if err := json.Unmarshal(encoded.Bytes(), &actual); err != nil {
		t.Fatal(err)
	}
	checkClaudeProbeFields(t, reflect.TypeOf(claudeProbeRequest{}), actual)
	if string(actual["type"]) != `"control_request"` || string(actual["request_id"]) != `"request-one-hot"` {
		t.Fatal("request identity not propagated")
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(actual["request"], &nested); err != nil {
		t.Fatal(err)
	}
	checkClaudeProbeFields(t, reflect.TypeOf(claudeProbeInitialize{}), nested)
	if string(nested["subtype"]) != `"initialize"` {
		t.Fatal("initialize not propagated")
	}
}

func checkClaudeProbeFields(t *testing.T, producer reflect.Type, actual map[string]json.RawMessage) {
	t.Helper()
	if len(actual) != producer.NumField() {
		t.Fatalf("field coverage mismatch: %s", producer.Name())
	}
	for i := 0; i < producer.NumField(); i++ {
		field := producer.Field(i)
		if _, ok := actual[field.Tag.Get("json")]; !ok {
			t.Fatalf("unmapped production field %s.%s", producer.Name(), field.Name)
		}
	}
}

func testClaudeProbeResponseFields(t *testing.T) {
	valid := claudeProbeResponse{Type: "control_response", Response: claudeProbeResult{Subtype: "success", RequestID: "request", Response: json.RawMessage(`{"extension":[]}`)}}
	assert := func(t *testing.T, v claudeProbeResponse, wantSuccess bool) {
		t.Helper()
		frame, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := readClaudeProbeInitialize(bytes.NewReader(frame), "request"); (err == nil) != wantSuccess {
			t.Fatalf("response validation = %v, want success %v", err, wantSuccess)
		}
	}
	assert(t, valid, true)
	for _, nested := range []bool{false, true} {
		typ := reflect.TypeOf(valid)
		if nested {
			typ = reflect.TypeOf(valid.Response)
		}
		for i := 0; i < typ.NumField(); i++ {
			t.Run(typ.Name()+"/"+typ.Field(i).Name, func(t *testing.T) {
				mutated := valid
				target := reflect.ValueOf(&mutated).Elem()
				if nested {
					target = target.FieldByName("Response")
				}
				target.Field(i).SetZero()
				assert(t, mutated, false)
			})
		}
	}
}

func testClaudeProbeNegativeFrames(t *testing.T) {
	valid := `{"type":"control_response","response":{"subtype":"success","request_id":"request","response":{}}}`
	cases := map[string]string{
		"empty": "", "bad-json": "{", "null": "null", "assistant": `{"type":"assistant"}`, "system": `{"type":"system","subtype":"init"}`,
		"error": strings.Replace(valid, `"success"`, `"error"`, 1), "foreign": strings.Replace(valid, `"request"`, `"foreign"`, 1),
		"null-payload": strings.Replace(valid, `"response":{}`, `"response":null`, 1), "array-payload": strings.Replace(valid, `"response":{}`, `"response":[]`, 1),
		"duplicate-type":       strings.Replace(valid, `"type":`, `"type":"control_response","type":`, 1),
		"duplicate-id":         strings.Replace(valid, `"request_id":`, `"request_id":"request","request_id":`, 1),
		"duplicate-payload":    strings.Replace(valid, `"response":{}`, `"response":{"models":[],"models":[]}`, 1),
		"duplicate-escaped-id": strings.Replace(valid, `"request_id":`, `"request_\u0069d":"request","request_id":`, 1),
		"error-detail":         `{"type":"control_response","response":{"subtype":"error","request_id":"request","error":"sensitive fixture detail"}}`,
		"unknown-envelope":     strings.Replace(valid, `"type":`, `"extra":true,"type":`, 1),
		"trailing":             valid + `{}`, "oversize": strings.Repeat("x", 1<<20),
	}
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			if err := readClaudeProbeInitialize(strings.NewReader(frame), "request"); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func claudeControlHelperArguments() (string, string) {
	session, mode := "", ""
	for i, arg := range os.Args {
		if i+1 < len(os.Args) {
			if arg == "--session-id" {
				session = os.Args[i+1]
			}
			if arg == "--model" {
				mode = os.Args[i+1]
			}
		}
	}
	if _, err := uuid.Parse(session); err != nil {
		os.Exit(41)
	}
	return session, mode
}

func readClaudeControlHelperRequest(session string) string {
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		os.Exit(42)
	}
	var request struct {
		Type    string `json:"type"`
		ID      string `json:"request_id"`
		Request struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || request.Type != "control_request" || request.Request.Subtype != "initialize" || request.ID == session {
		os.Exit(43)
	}
	if _, err := uuid.Parse(request.ID); err != nil {
		os.Exit(44)
	}
	return request.ID
}
