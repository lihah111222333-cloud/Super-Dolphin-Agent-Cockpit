package cron

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/creachadair/jrpc2"

	platformrpc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
)

func TestCronListHandlerRequiresCursorAndMapsPage(t *testing.T) {
	var captured ListJobsPageParams
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)), &fakeStore{
		listPageFn: func(_ context.Context, params ListJobsPageParams) (JobRecordPage, error) {
			captured = params
			return JobRecordPage{Jobs: []JobRecord{}, NextCursor: "next", HasMore: true}, nil
		},
	})
	handler := listHandler(svc)

	if _, err := handler(context.Background(), cronListParams{Limit: 1}); err == nil {
		t.Fatal("missing cursor was accepted")
	} else {
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != jrpc2.Code(platformrpc.CodeInvalidParams) {
			t.Fatalf("missing cursor error = %T %v, want invalid params", err, err)
		}
	}

	cursor := ""
	got, err := handler(context.Background(), cronListParams{Limit: 2, Cursor: &cursor})
	if err != nil {
		t.Fatalf("list page: %v", err)
	}
	if captured != (ListJobsPageParams{Limit: 2, Cursor: ""}) {
		t.Fatalf("captured params = %+v", captured)
	}
	if got.Jobs == nil || got.NextCursor != "next" || !got.HasMore {
		t.Fatalf("response = %+v", got)
	}
}
