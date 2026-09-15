package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		idx := strings.IndexByte(e.Name(), '_')
		if idx <= 0 {
			return nil, fmt.Errorf("bad migration name %q (want NNNN_name.sql)", e.Name())
		}
		v, err := strconv.Atoi(e.Name()[:idx])
		if err != nil {
			return nil, fmt.Errorf("bad migration version %q: %w", e.Name(), err)
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// Migrate 比对 schema_migrations 后顺序应用未执行的迁移，每个迁移在独立事务内执行。
func (db *DB) Migrate(ctx context.Context) error {
	if _, err := db.write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}

	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		tx, err := db.write.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range splitStatements(m.sql) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply %s: %w", m.name, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version) VALUES (?)`, m.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record %s: %w", m.name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) EnsureUploadTaskCrossColumns(ctx context.Context) error {
	exists, err := tableExists(ctx, db, "upload_tasks")
	if err != nil || !exists {
		return err
	}
	columns, err := tableColumns(ctx, db, "upload_tasks")
	if err != nil {
		return err
	}
	need := []struct {
		name string
		ddl  string
	}{
		{name: "source_type", ddl: `ALTER TABLE upload_tasks ADD COLUMN source_type TEXT NOT NULL DEFAULT ''`},
		{name: "source_account_id", ddl: `ALTER TABLE upload_tasks ADD COLUMN source_account_id INTEGER NOT NULL DEFAULT 0`},
		{name: "source_account_name", ddl: `ALTER TABLE upload_tasks ADD COLUMN source_account_name TEXT NOT NULL DEFAULT ''`},
		{name: "source_driver_type", ddl: `ALTER TABLE upload_tasks ADD COLUMN source_driver_type TEXT NOT NULL DEFAULT ''`},
		{name: "source_file_id", ddl: `ALTER TABLE upload_tasks ADD COLUMN source_file_id TEXT NOT NULL DEFAULT ''`},
		{name: "rel_path", ddl: `ALTER TABLE upload_tasks ADD COLUMN rel_path TEXT NOT NULL DEFAULT ''`},
		{name: "rel_dir", ddl: `ALTER TABLE upload_tasks ADD COLUMN rel_dir TEXT NOT NULL DEFAULT ''`},
		{name: "phase", ddl: `ALTER TABLE upload_tasks ADD COLUMN phase TEXT NOT NULL DEFAULT ''`},
		{name: "downloaded_bytes", ddl: `ALTER TABLE upload_tasks ADD COLUMN downloaded_bytes INTEGER NOT NULL DEFAULT 0`},
	}
	for _, item := range need {
		if columns[item.name] {
			continue
		}
		if _, err := db.write.ExecContext(ctx, item.ddl); err != nil {
			return fmt.Errorf("repair upload_tasks.%s: %w", item.name, err)
		}
	}
	return nil
}

func appliedVersions(ctx context.Context, db *DB) (map[int]bool, error) {
	rows, err := db.write.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func tableExists(ctx context.Context, db *DB, name string) (bool, error) {
	var count int
	if err := db.write.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, name,
	).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func tableColumns(ctx context.Context, db *DB, table string) (map[string]bool, error) {
	rows, err := db.write.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			defaultV  any
			primaryKV int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultV, &primaryKV); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// splitStatements 按分号拆分迁移脚本（迁移 SQL 内不含分号字面量）。
func splitStatements(script string) []string {
	var out []string
	for _, s := range strings.Split(script, ";") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
