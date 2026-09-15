package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"litepan/internal/domain"
)

// tsLayout 与 SQLite CURRENT_TIMESTAMP 文本格式一致（UTC）。
const tsLayout = "2006-01-02 15:04:05"

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// parseTS 把可空的时间文本解析为 time.Time，NULL/空值返回零值。
func parseTS(ns sql.NullString) time.Time {
	if !ns.Valid || ns.String == "" {
		return time.Time{}
	}
	raw := strings.TrimSpace(ns.String)
	if i := strings.Index(raw, " m="); i > 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	for _, layout := range []string{
		tsLayout,
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05 -0700 MST",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

// tsValue 把 time.Time 转为可写入的值，零值写 NULL。
func tsValue(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.UTC().Format(tsLayout)
}

// wrapDB 把底层数据库错误归一为结构化 AppError。
func wrapDB(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Errf(domain.CodeNotFound)
	}
	return domain.Wrap(domain.CodeInternal, err)
}
