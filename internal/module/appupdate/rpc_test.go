package appupdate

import (
	"context"
	"encoding/json"
	"testing"

	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

type stubUpdateService struct {
	checkResult    CheckResult
	downloadResult DownloadResult
	installResult  InstallResult

	called []string
	err    error
}

func (s *stubUpdateService) Check(context.Context) (CheckResult, error) {
	s.called = append(s.called, "check")
	return s.checkResult, s.err
}

func (s *stubUpdateService) Download(context.Context) (DownloadResult, error) {
	s.called = append(s.called, "download")
	return s.downloadResult, s.err
}

func (s *stubUpdateService) Install(context.Context) (InstallResult, error) {
	s.called = append(s.called, "install")
	return s.installResult, s.err
}

func (s *stubUpdateService) InstallLatest(context.Context) (InstallResult, error) {
	s.called = append(s.called, "installLatest")
	return s.installResult, s.err
}

func TestUpdateRPCMethodsDispatch(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{method: "app/update/check", want: "check"},
		{method: "app/update/download", want: "download"},
		{method: "app/update/install", want: "install"},
		{method: "app/update/installLatest", want: "installLatest"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			svc := &stubUpdateService{
				checkResult: CheckResult{Available: true, Version: "1.2.3"},
				downloadResult: DownloadResult{
					StagedManifestPath: "/tmp/selected-update.json",
					DMGPath:            "/tmp/update.dmg",
					Version:            "1.2.3",
				},
				installResult: InstallResult{Started: true, Helper: "/tmp/helper"},
			}
			server := platformrpc.NewServer(platformrpc.Params{Config: &platformconfig.Config{RPCAddr: "127.0.0.1:0"}})
			server.Register(NewHandlers(svc).Handlers)

			raw, err := server.Dispatch(context.Background(), tt.method, json.RawMessage(`{}`))
			if err != nil {
				t.Fatalf("Dispatch(%s) error = %v", tt.method, err)
			}
			if len(svc.called) != 1 || svc.called[0] != tt.want {
				t.Fatalf("called = %#v, want %s", svc.called, tt.want)
			}
			if len(raw) == 0 || string(raw) == "null" {
				t.Fatalf("Dispatch(%s) response = %s, want object", tt.method, raw)
			}
		})
	}
}
