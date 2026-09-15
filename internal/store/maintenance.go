package store

import "context"

// ReclaimableBytes 返回 SQLite 空闲页可通过 VACUUM 归还给文件系统的估算字节数。
// 空闲页本身可被 SQLite 后续写入复用，因此这里只作为空间整理提示。
func (db *DB) ReclaimableBytes(ctx context.Context) (int64, error) {
	if db == nil || db.read == nil {
		return 0, nil
	}
	var pageSize, freePages int64
	if err := db.read.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return 0, err
	}
	if err := db.read.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&freePages); err != nil {
		return 0, err
	}
	if pageSize <= 0 || freePages <= 0 {
		return 0, nil
	}
	return pageSize * freePages, nil
}

// Vacuum 压缩主数据库。写池只有一条连接，执行期间其它数据库写入会自然排队。
func (db *DB) Vacuum(ctx context.Context) error {
	if db == nil || db.write == nil {
		return nil
	}
	_, err := db.write.ExecContext(ctx, `VACUUM`)
	return err
}
