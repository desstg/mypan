package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

const maxReadConns = 4

// DB 在 SQLite WAL 模式下使用单写、多读连接池。
type DB struct {
	write *sql.DB
	read  *sql.DB
}

// Options 控制底层 SQLite 的打开方式。
type Options struct {
	Path   string // SQLite 文件路径（Memory 为 false 时使用）
	Memory bool   // true 时使用共享缓存内存库，供测试使用
}

var memSeq atomic.Int64

// Open 按 Options 建立读写双池并完成连通性检查。
func Open(ctx context.Context, opts Options) (*DB, error) {
	dsn := buildDSN(opts)

	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open write pool: %w", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	write.SetConnMaxIdleTime(0)

	read, err := sql.Open("sqlite", dsn)
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open read pool: %w", err)
	}
	read.SetMaxOpenConns(maxReadConns)
	read.SetMaxIdleConns(maxReadConns)
	read.SetConnMaxIdleTime(0)

	// 先在写池建立连接：内存共享库需至少一条活动连接维持其存在。
	if err := write.PingContext(ctx); err != nil {
		_ = write.Close()
		_ = read.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &DB{write: write, read: read}, nil
}

func buildDSN(opts Options) string {
	if opts.Memory {
		name := "litepan_mem_" + strconv.FormatInt(memSeq.Add(1), 10)
		return "file:" + name + "?mode=memory&cache=shared" +
			"&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)"
	}
	return "file:" + opts.Path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(on)&_pragma=synchronous(NORMAL)"
}

func (db *DB) Close() error {
	e1 := db.read.Close()
	e2 := db.write.Close()
	if e1 != nil {
		return e1
	}
	return e2
}
