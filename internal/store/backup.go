package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BackupCounts 是备份确认页需要的业务数量摘要。
type BackupCounts struct {
	Accounts int `json:"accounts"`
	Tasks    int `json:"tasks"`
}

// SnapshotTo 通过 SQLite VACUUM INTO 创建一致性单文件快照。
// 调用方必须传入尚不存在的目标文件。
func (db *DB) SnapshotTo(ctx context.Context, destination string) error {
	if db == nil || db.write == nil {
		return fmt.Errorf("database is not open")
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve snapshot destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("snapshot destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat snapshot destination: %w", err)
	}
	if _, err := db.write.ExecContext(ctx, `VACUUM main INTO ?`, abs); err != nil {
		return fmt.Errorf("create sqlite snapshot: %w", err)
	}
	if err := os.Chmod(abs, 0o600); err != nil {
		return fmt.Errorf("secure sqlite snapshot: %w", err)
	}
	return nil
}

// SchemaVersion 返回当前数据库已应用的最高迁移版本。
func (db *DB) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := db.read.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// SupportedSchemaVersion 返回当前程序能迁移到的最高 schema 版本。
func SupportedSchemaVersion() (int, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if len(migrations) == 0 {
		return 0, nil
	}
	return migrations[len(migrations)-1].version, nil
}

// IntegrityCheck 执行 SQLite 完整性校验。
func (db *DB) IntegrityCheck(ctx context.Context) error {
	rows, err := db.read.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return fmt.Errorf("sqlite integrity check: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("read sqlite integrity result: %w", err)
		}
		if result != "ok" {
			return fmt.Errorf("sqlite integrity check failed: %s", result)
		}
	}
	return rows.Err()
}

// SanitizePortableBackup 清除只属于当前运行环境的临时状态。
// 保留账号、凭据和可重启的业务任务配置。
func (db *DB) SanitizePortableBackup(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	statements := []string{
		`DELETE FROM upload_tasks`,
		`DELETE FROM offline_download_tasks`,
		`DELETE FROM notifications`,
		`DELETE FROM automation_runs`,
		`DELETE FROM strm_remote_dir_cache`,
		`UPDATE strm_tasks SET scanned_count=0, generated_count=0, updated_count=0, removed_count=0, last_scan=NULL, last_scan_status='', error_message=''`,
		`UPDATE media_organize_tasks SET status='idle', last_run_at=NULL, last_run_result=''`,
		`UPDATE fuse_mounts SET state='unmounted', last_error=''`,
		`UPDATE cache_retention_tasks SET file_count=0, last_refresh=NULL, last_refresh_status='', last_duration_ms=0, last_api_calls=0, last_skip_calls=0, last_scanned_dirs=0, error_message='', last_run_config_fp=''`,
		`UPDATE automation_rules SET next_run_at='', last_run_at='', last_run_status='', last_run_message=''`,
		`UPDATE api_keys SET last_used_at=NULL`,
		`DELETE FROM configs WHERE key IN ('admin_temp_password_hash','admin_temp_password_expires_at','admin_temp_password_last_reset_at','log_error_ack_at')`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sanitize portable backup: %w", err)
		}
	}
	return tx.Commit()
}

// BackupCounts 返回完整备份中的账号和任务数量。
func (db *DB) BackupCounts(ctx context.Context) (BackupCounts, error) {
	var result BackupCounts
	if err := db.read.QueryRowContext(ctx, `SELECT COUNT(1) FROM cloud_accounts`).Scan(&result.Accounts); err != nil {
		return BackupCounts{}, fmt.Errorf("count backup accounts: %w", err)
	}
	tables := []string{
		"strm_tasks",
		"media_organize_tasks",
		"fuse_mounts",
		"cache_retention_tasks",
		"automation_rules",
	}
	for _, table := range tables {
		var count int
		if err := db.read.QueryRowContext(ctx, `SELECT COUNT(1) FROM `+table).Scan(&count); err != nil {
			return BackupCounts{}, fmt.Errorf("count %s: %w", table, err)
		}
		result.Tasks += count
	}
	return result, nil
}

// ReplaceConfigs 在 staging 数据库内原子替换指定配置键。
// remove 先删除，values 再写入；两者均由备份恢复层控制范围。
func (db *DB) ReplaceConfigs(ctx context.Context, remove []string, values map[string]string) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	sort.Strings(remove)
	for _, key := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM configs WHERE key=?`, key); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("delete config %s: %w", key, err)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO configs(key,value,updated_at) VALUES(?,?,CURRENT_TIMESTAMP)
			 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`,
			key, values[key],
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("replace config %s: %w", key, err)
		}
	}
	return tx.Commit()
}
