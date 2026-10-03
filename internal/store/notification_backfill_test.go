package store

import (
	"context"
	"testing"
)

// TestNotificationMergeBackfillMigration 迁移 0049 要对**存量**重复行就地合并：
// 老库里那 8 条一模一样的「安全保护阻止清理」升级后不该继续各占一行，
// 否则「合并」只对新产生的通知生效，用户第一眼看到的还是一屏重复。
//
// 手工建出「0049 之前」的库形态（删掉 0049 的两列、撤掉版本号），
// 种进未读重复行，再跑一次 Migrate。
func TestNotificationMergeBackfillMigration(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)

	for _, ddl := range []string{
		`ALTER TABLE notifications DROP COLUMN count`,
		`ALTER TABLE notifications DROP COLUMN first_at`,
		`DELETE FROM schema_migrations WHERE version = 49`,
	} {
		if _, err := db.write.ExecContext(ctx, ddl); err != nil {
			t.Skipf("无法退回 0049 之前的形态（%v）；本用例需要 SQLite 的 DROP COLUMN 支持", err)
		}
	}

	// 4 条同键未读 + 1 条同键已读 + 1 条正文不同的未读。
	seed := []struct {
		msg    string
		isRead int
		at     string
	}{
		{"保护正文", 0, "2026-10-02 13:11:18"},
		{"保护正文", 0, "2026-10-02 20:00:46"},
		{"保护正文", 0, "2026-10-02 20:02:55"},
		{"保护正文", 0, "2026-10-02 23:05:50"},
		{"保护正文", 1, "2026-10-02 12:00:00"}, // 已读：不参与合并
		{"情况变了", 0, "2026-10-02 21:00:00"}, // 正文不同：不参与合并
	}
	for _, row := range seed {
		if _, err := db.write.ExecContext(ctx,
			`INSERT INTO notifications(level, category, title, message, account_id, ref_id, is_read, created_at)
			 VALUES ('warning','strm_scan_warn','STRM 扫描安全保护阻止清理',?,1,7,?,?)`,
			row.msg, row.isRead, row.at); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	var rows, total int
	if err := db.write.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(count),0) FROM notifications WHERE is_read = 0`).
		Scan(&rows, &total); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != 2 {
		t.Fatalf("未读应合并成 2 行（保护 4 条并成 1 行 + 正文不同 1 行），实际 %d 行", rows)
	}
	if total != 5 {
		t.Fatalf("count 合计应为 5（4 次保护 + 1 次情况变了），实际 %d", total)
	}

	// 已读那条要原样留着，且 count=1。
	var readCount int
	if err := db.write.QueryRowContext(ctx,
		`SELECT count FROM notifications WHERE is_read = 1`).Scan(&readCount); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if readCount != 1 {
		t.Fatalf("已读行的 count 应为 1，实际 %d", readCount)
	}

	// 合并后那一行：count=4，first_at 取最早、created_at 取最晚。
	//
	// ⚠️ 比的是**库里的文本**：这两列声明成 TIMESTAMP，驱动读到 string 时会先
	// 解析成 time.Time 再按 RFC3339 输出，直接 Scan(&string) 拿到的是
	// `2026-10-02T13:11:18Z` 而不是库里的 `2026-10-02 13:11:18`。
	// 外面套一层 strftime 让它以表达式返回，才是列里的原样文本。
	var merged int
	var firstAt, createdAt string
	if err := db.write.QueryRowContext(ctx,
		`SELECT count,
		        strftime('%Y-%m-%d %H:%M:%S', first_at),
		        strftime('%Y-%m-%d %H:%M:%S', created_at)
		   FROM notifications
		  WHERE is_read = 0 AND message = '保护正文'`).Scan(&merged, &firstAt, &createdAt); err != nil {
		t.Fatalf("merged row: %v", err)
	}
	if merged != 4 {
		t.Fatalf("合并行 count 应为 4，实际 %d", merged)
	}
	if firstAt != "2026-10-02 13:11:18" {
		t.Fatalf("first_at 应取最早，实际 %q", firstAt)
	}
	if createdAt != "2026-10-02 23:05:50" {
		t.Fatalf("created_at 应取最晚，实际 %q", createdAt)
	}
}
