package cron

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

const maxCronListLimit int32 = 100

type cronListCursor struct {
	Version   int    `json:"v"`
	CreatedAt int64  `json:"c"`
	ID        string `json:"i"`
}

// ListJobsPage 使用不可变的 created_at/id keyset 读取有限页和一条前瞻记录。
// Cursor 强制规范编码，避免同一数据库边界产生多个等价表示。
func (s *cronJobQueryStore) ListJobsPage(ctx context.Context, p ListJobsPageParams) (JobPage, error) {
	if p.Limit <= 0 || p.Limit > maxCronListLimit {
		return JobPage{}, wrap(ErrInvalidListLimit, "list_jobs_page")
	}
	cursor, err := decodeCronListCursor(p.Cursor)
	if err != nil {
		return JobPage{}, wrap(err, "list_jobs_page")
	}
	rows, err := s.q.ListCronJobsPage(ctx, sqlc.ListCronJobsPageParams{
		CursorCreatedAt: cursor.CreatedAt,
		CursorID:        cursor.ID,
		LimitPlusOne:    int64(p.Limit) + 1,
	})
	if err != nil {
		return JobPage{}, wrap(err, "list_jobs_page")
	}
	hasMore := len(rows) > int(p.Limit)
	if hasMore {
		rows = rows[:p.Limit]
	}
	page := JobPage{Jobs: make([]Job, len(rows)), HasMore: hasMore, NextCursor: ""}
	for i, row := range rows {
		page.Jobs[i] = fromCronJob(row)
	}
	if hasMore && len(rows) > 0 {
		page.NextCursor, err = encodeCronListCursor(cronListCursor{Version: 1, CreatedAt: rows[len(rows)-1].CreatedAt, ID: rows[len(rows)-1].ID})
		if err != nil {
			return JobPage{}, wrap(err, "list_jobs_page")
		}
	}
	return page, nil
}

// encodeCronListCursor 校验并编码唯一规范的分页 cursor。
func encodeCronListCursor(cursor cronListCursor) (string, error) {
	if cursor.Version != 1 || cursor.CreatedAt <= 0 || strings.TrimSpace(cursor.ID) == "" || cursor.ID != strings.TrimSpace(cursor.ID) {
		return "", ErrInvalidListCursor
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("cron: encode list cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

// decodeCronListCursor 只接受空首页标记或唯一规范的非空 cursor。
func decodeCronListCursor(raw string) (cronListCursor, error) {
	if raw == "" {
		return cronListCursor{CreatedAt: math.MaxInt64, ID: "\U0010FFFF"}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return cronListCursor{}, fmt.Errorf("%w: %v", ErrInvalidListCursor, err)
	}
	var cursor cronListCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return cronListCursor{}, fmt.Errorf("%w: %v", ErrInvalidListCursor, err)
	}
	canonical, err := encodeCronListCursor(cursor)
	if err != nil || raw != canonical {
		return cronListCursor{}, ErrInvalidListCursor
	}
	return cursor, nil
}
