package strmscrape

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"litepan/internal/domain"

	_ "modernc.org/sqlite"
)

const indexSchemaVersion = "2"

// TaskIndexPath 返回任务刮削索引库路径：data/strmscrape/{task_id}.sqlite
func TaskIndexPath(dataDir string, taskID int64) string {
	return filepath.Join(strings.TrimSpace(dataDir), "strmscrape", strconv.FormatInt(taskID, 10)+".sqlite")
}

// RemoveTaskIndex 删除任务索引及其 WAL/SHM（任务删除时调用）。
func RemoveTaskIndex(dataDir string, taskID int64) {
	base := TaskIndexPath(dataDir, taskID)
	for _, p := range []string{base, base + "-wal", base + "-shm"} {
		_ = os.Remove(p)
	}
}

func (s *Service) indexPath(taskID int64) string {
	return TaskIndexPath(s.dataDir, taskID)
}

func (s *Service) withTaskIndexLock(taskID int64, fn func() error) error {
	v, _ := s.indexLocks.LoadOrStore(taskID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}

func openTaskIndexDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := "file:" + filepath.ToSlash(abs) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := ensureIndexSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func ensureIndexSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS meta (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS items (
  id TEXT PRIMARY KEY,
  rel_dir TEXT NOT NULL DEFAULT '',
  strm_name TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  year INTEGER,
  media_type TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  has_nfo INTEGER NOT NULL DEFAULT 0,
  has_poster INTEGER NOT NULL DEFAULT 0,
  has_pending INTEGER NOT NULL DEFAULT 0,
  manual_done INTEGER NOT NULL DEFAULT 0,
  tmdb_id TEXT NOT NULL DEFAULT '',
  poster_rel TEXT NOT NULL DEFAULT '',
  folder_name TEXT NOT NULL DEFAULT '',
  file_count INTEGER NOT NULL DEFAULT 0,
  ep_local INTEGER NOT NULL DEFAULT 0,
  ep_tmdb INTEGER NOT NULL DEFAULT 0,
  ep_scraped INTEGER NOT NULL DEFAULT 0,
  tv_state TEXT NOT NULL DEFAULT '',
  added_at TEXT NOT NULL DEFAULT ''
);
`)
	return err
}

func (s *Service) indexFileExists(taskID int64) bool {
	st, err := os.Stat(s.indexPath(taskID))
	return err == nil && !st.IsDir()
}

func readIndexMeta(db *sql.DB, key string) (string, bool) {
	var v string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return "", false
	}
	return v, true
}

func writeIndexMeta(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO meta(key, value) VALUES(?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func itemPosterRel(root string, g workGroup, mediaType string) string {
	if !workHasPoster(g, mediaType) {
		return ""
	}
	return filepath.ToSlash(relUnder(root, workPosterFile(g, mediaType)))
}

func posterURLFromRel(taskID int64, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return ""
	}
	return fmt.Sprintf("/api/admin/strm-scrape/poster?strm_task_id=%d&rel=%s", taskID, pathEscape(rel))
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// RebuildIndex 扫盘重建该任务索引。
func (s *Service) RebuildIndex(ctx context.Context, strmTaskID int64) error {
	if strmTaskID <= 0 {
		return domain.Errorf(domain.CodeValidation, "strm_task_id 无效")
	}
	return s.withTaskIndexLock(strmTaskID, func() error {
		return s.rebuildIndexLocked(ctx, strmTaskID)
	})
}

func (s *Service) rebuildIndexLocked(ctx context.Context, strmTaskID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, root, err := s.resolveTask(ctx, strmTaskID)
	if err != nil {
		return err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return err
	}
	st, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.Errorf(domain.CodeValidation, "STRM 输出目录不存在：%s", root)
		}
		return err
	}
	if !st.IsDir() {
		return domain.Errorf(domain.CodeValidation, "STRM 输出目录无效：%s", root)
	}
	works, err := scanWorks(root)
	if err != nil {
		return err
	}
	works = filterWorksByScope(works, s.GetScope(strmTaskID).ExcludedDirs)
	items := make([]Item, 0, len(works))
	rels := make([]string, 0, len(works))
	for _, g := range works {
		if err := ctx.Err(); err != nil {
			return err
		}
		it := buildItem(strmTaskID, root, g)
		items = append(items, it)
		rels = append(rels, itemPosterRel(root, g, it.MediaType))
	}

	db, err := openTaskIndexDB(s.indexPath(strmTaskID))
	if err != nil {
		return err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM items`); err != nil {
		return err
	}
	for i, it := range items {
		if err := upsertItemTx(tx, it, rels[i]); err != nil {
			return err
		}
	}
	if err := writeIndexMeta(tx, "schema", indexSchemaVersion); err != nil {
		return err
	}
	if err := writeIndexMeta(tx, "root", root); err != nil {
		return err
	}
	if err := writeIndexMeta(tx, "built_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertItemTx(tx *sql.Tx, it Item, posterRel string) error {
	var year any
	if it.Year != nil {
		year = *it.Year
	}
	_, err := tx.Exec(`
INSERT INTO items (
  id, rel_dir, strm_name, title, year, media_type, status,
  has_nfo, has_poster, has_pending, manual_done, tmdb_id, poster_rel, folder_name,
  file_count, ep_local, ep_tmdb, ep_scraped, tv_state, added_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  rel_dir=excluded.rel_dir,
  strm_name=excluded.strm_name,
  title=excluded.title,
  year=excluded.year,
  media_type=excluded.media_type,
  status=excluded.status,
  has_nfo=excluded.has_nfo,
  has_poster=excluded.has_poster,
  has_pending=excluded.has_pending,
  manual_done=excluded.manual_done,
  tmdb_id=excluded.tmdb_id,
  poster_rel=excluded.poster_rel,
  folder_name=excluded.folder_name,
  file_count=excluded.file_count,
  ep_local=excluded.ep_local,
  ep_tmdb=excluded.ep_tmdb,
  ep_scraped=excluded.ep_scraped,
  tv_state=excluded.tv_state,
  added_at=excluded.added_at
`, it.ID, it.RelDir, it.StrmName, it.Title, year, it.MediaType, it.Status,
		boolToInt(it.HasNFO), boolToInt(it.HasPoster), boolToInt(it.HasPending), boolToInt(it.ManualDone),
		it.TMDBID, posterRel, it.FolderName, it.FileCount, it.EpLocal, it.EpTMDB,
		it.EpScraped, it.TVState, it.AddedAt)
	return err
}

func (s *Service) upsertIndexItem(ctx context.Context, strmTaskID int64, root string, g workGroup) {
	_ = s.withTaskIndexLock(strmTaskID, func() error {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if !s.indexFileExists(strmTaskID) {
			return s.rebuildIndexLocked(ctx, strmTaskID)
		}
		db, err := openTaskIndexDB(s.indexPath(strmTaskID))
		if err != nil {
			return err
		}
		defer db.Close()
		if stored, ok := readIndexMeta(db, "root"); ok && stored != "" {
			storedAbs := stored
			if abs, err := filepath.Abs(stored); err == nil {
				storedAbs = abs
			}
			if storedAbs != root {
				return s.rebuildIndexLocked(ctx, strmTaskID)
			}
		}
		it := buildItem(strmTaskID, root, g)
		rel := itemPosterRel(root, g, it.MediaType)
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := upsertItemTx(tx, it, rel); err != nil {
			return err
		}
		return tx.Commit()
	})
}

func buildItemListWhere(query ItemListQuery) (string, []any) {
	clauses := make([]string, 0, 4)
	args := make([]any, 0, 8)
	if query.Keyword != "" {
		kw := "%" + strings.ToLower(query.Keyword) + "%"
		clauses = append(clauses, `(LOWER(title) LIKE ? OR LOWER(folder_name) LIKE ? OR LOWER(strm_name) LIKE ?)`)
		args = append(args, kw, kw, kw)
	}
	if query.Status != "" {
		clauses = append(clauses, `status = ?`)
		args = append(args, query.Status)
	}
	if query.MediaType != "" {
		clauses = append(clauses, `media_type = ?`)
		args = append(args, query.MediaType)
	}
	if query.TVState != "" {
		clauses = append(clauses, `tv_state = ?`)
		args = append(args, query.TVState)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func itemListOrderBy(sort ItemListSort) string {
	switch sort {
	case ItemListSortTitleAsc:
		return "ORDER BY title COLLATE NOCASE ASC, added_at DESC"
	case ItemListSortYearDesc:
		return "ORDER BY CASE WHEN year IS NULL THEN 1 ELSE 0 END ASC, year DESC, title COLLATE NOCASE ASC"
	case ItemListSortYearAsc:
		return "ORDER BY CASE WHEN year IS NULL THEN 1 ELSE 0 END ASC, year ASC, title COLLATE NOCASE ASC"
	case ItemListSortAddedAsc:
		return "ORDER BY added_at ASC, title COLLATE NOCASE ASC"
	default:
		return "ORDER BY added_at DESC, title COLLATE NOCASE ASC"
	}
}

func scanIndexItems(rows *sql.Rows, strmTaskID int64) ([]Item, error) {
	out := make([]Item, 0, 128)
	for rows.Next() {
		var it Item
		var year sql.NullInt64
		var hasNFO, hasPoster, hasPending, manualDone int
		var posterRel string
		if err := rows.Scan(
			&it.ID, &it.RelDir, &it.StrmName, &it.Title, &year, &it.MediaType, &it.Status,
			&hasNFO, &hasPoster, &hasPending, &manualDone, &it.TMDBID, &posterRel, &it.FolderName,
			&it.FileCount, &it.EpLocal, &it.EpTMDB, &it.EpScraped, &it.TVState, &it.AddedAt,
		); err != nil {
			return nil, err
		}
		it.HasNFO = hasNFO != 0
		it.HasPoster = hasPoster != 0
		it.HasPending = hasPending != 0
		it.ManualDone = manualDone != 0
		if year.Valid {
			y := int(year.Int64)
			it.Year = &y
		}
		it.PosterURL = posterURLFromRel(strmTaskID, posterRel)
		out = append(out, it)
	}
	return out, rows.Err()
}

func readIndexStats(db *sql.DB) (ItemListStats, error) {
	var stats ItemListStats
	err := db.QueryRow(`
SELECT
  COUNT(*),
  SUM(CASE WHEN status = 'ok' THEN 1 ELSE 0 END),
  SUM(CASE WHEN status = 'miss' THEN 1 ELSE 0 END),
  SUM(CASE WHEN status = 'doubt' THEN 1 ELSE 0 END)
FROM items
`).Scan(&stats.Total, &stats.OK, &stats.Miss, &stats.Doubt)
	return stats, err
}

func (s *Service) listIndexItems(strmTaskID int64, query ItemListQuery) (ItemListResult, error) {
	db, err := openTaskIndexDB(s.indexPath(strmTaskID))
	if err != nil {
		return ItemListResult{}, err
	}
	defer db.Close()

	stats, err := readIndexStats(db)
	if err != nil {
		return ItemListResult{}, err
	}
	whereSQL, whereArgs := buildItemListWhere(query)
	countSQL := `SELECT COUNT(*) FROM items`
	if whereSQL != "" {
		countSQL += "\n" + whereSQL
	}
	var total int
	if err := db.QueryRow(countSQL, whereArgs...).Scan(&total); err != nil {
		return ItemListResult{}, err
	}
	querySQL := `
SELECT id, rel_dir, strm_name, title, year, media_type, status,
       has_nfo, has_poster, has_pending, manual_done, tmdb_id, poster_rel, folder_name,
       file_count, ep_local, ep_tmdb, ep_scraped, tv_state, added_at
FROM items
`
	if whereSQL != "" {
		querySQL += whereSQL + "\n"
	}
	querySQL += itemListOrderBy(query.Sort) + "\nLIMIT ? OFFSET ?"
	args := append(append(make([]any, 0, len(whereArgs)+2), whereArgs...), query.Limit, query.Offset)
	rows, err := db.Query(querySQL, args...)
	if err != nil {
		return ItemListResult{}, err
	}
	defer rows.Close()
	items, err := scanIndexItems(rows, strmTaskID)
	if err != nil {
		return ItemListResult{}, err
	}
	return ItemListResult{
		Items:   items,
		Total:   total,
		Offset:  query.Offset,
		Limit:   query.Limit,
		HasMore: query.Offset+len(items) < total,
		Stats:   stats,
	}, nil
}

func (s *Service) ensureIndexLocked(ctx context.Context, strmTaskID int64, root string) error {
	rootAbs := root
	if abs, err := filepath.Abs(root); err == nil {
		rootAbs = abs
	}
	if !s.indexFileExists(strmTaskID) {
		return s.rebuildIndexLocked(ctx, strmTaskID)
	}
	db, err := openTaskIndexDB(s.indexPath(strmTaskID))
	if err != nil {
		return s.rebuildIndexLocked(ctx, strmTaskID)
	}
	defer db.Close()
	if ver, ok := readIndexMeta(db, "schema"); !ok || ver != indexSchemaVersion {
		// 任务索引是可重建缓存，不做列迁移；先关闭并删除旧库，兼容 Windows 文件锁。
		_ = db.Close()
		RemoveTaskIndex(s.dataDir, strmTaskID)
		return s.rebuildIndexLocked(ctx, strmTaskID)
	}
	if stored, ok := readIndexMeta(db, "root"); ok && stored != "" {
		storedAbs := stored
		if abs, err := filepath.Abs(stored); err == nil {
			storedAbs = abs
		}
		if storedAbs != rootAbs {
			return s.rebuildIndexLocked(ctx, strmTaskID)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n); err != nil || n == 0 {
		// 空索引常见于目录当时为空或重建中断；再扫一次对齐磁盘
		return s.rebuildIndexLocked(ctx, strmTaskID)
	}
	return nil
}
