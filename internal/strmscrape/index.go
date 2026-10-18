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
//
// 与 `SourceIndexPath` 的关系：任务那侧的 ScopeKey 就是裸十进制，所以两者对任务
// 给出**同一个**文件名 —— 已经存在的 `5.sqlite` 会被原样复用，不需要迁移。
func TaskIndexPath(dataDir string, taskID int64) string {
	return SourceIndexPath(dataDir, strconv.FormatInt(taskID, 10))
}

// SourceIndexPath 返回某个**数据源**的刮削索引库路径：data/strmscrape/{scopeKey}.sqlite
//
// `scopeKey` 是 `wallSrc.ScopeKey()`：任务是裸十进制（`"5"`），挂上来的本地目录
// 是 `local-<hex>`。两者都只含 `[0-9a-z-]`，所以直接当文件名是安全的 ——
// 这里**不做**哈希：哈希会让已经存在的 `5.sqlite` 全部作废（用户升级后要重扫一遍），
// 而键本身已经是「文件名安全」的。
func SourceIndexPath(dataDir, scopeKey string) string {
	key := sanitizeIndexKey(scopeKey)
	if key == "" {
		key = "unknown"
	}
	return filepath.Join(strings.TrimSpace(dataDir), "strmscrape", key+".sqlite")
}

// sanitizeIndexKey 把 ScopeKey 收敛成一个安全的文件名片段。
//
// 只放行 `[0-9a-z-]`（大写转小写）：本地目录的 id 是十六进制、任务是十进制，
// 两者本来就在这个字符集里；这一层是防「将来有人往键里塞别的东西」——
// 键一旦带 `/` 或 `..`，索引库就会被写到 data 目录之外。
func sanitizeIndexKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RemoveTaskIndex 删除任务索引及其 WAL/SHM（任务删除时调用）。
func RemoveTaskIndex(dataDir string, taskID int64) {
	RemoveSourceIndex(dataDir, strconv.FormatInt(taskID, 10))
}

// RemoveSourceIndex 删除某个数据源的索引及其 WAL/SHM。
//
// 三个文件一起删：SQLite 的 WAL/SHM 留着会让下一次打开读到**已经删掉的库的**
// 残留页（Windows 上尤其明显，文件锁还没释放时更是）。
func RemoveSourceIndex(dataDir, scopeKey string) {
	base := SourceIndexPath(dataDir, scopeKey)
	for _, p := range []string{base, base + "-wal", base + "-shm"} {
		_ = os.Remove(p)
	}
}

func (s *Service) indexPath(scopeKey string) string {
	return SourceIndexPath(s.dataDir, scopeKey)
}

func (s *Service) withTaskIndexLock(scopeKey string, fn func() error) error {
	v, _ := s.indexLocks.LoadOrStore(scopeKey, &sync.Mutex{})
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

func (s *Service) indexFileExists(scopeKey string) bool {
	st, err := os.Stat(s.indexPath(scopeKey))
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

func posterURLFromRel(ref posterRef, rel string) string {
	return posterURLFromRelWidth(ref, rel, 0)
}

// posterURLFromRelWidth 同上，但带一个**显示宽度**（CSS 像素）。
//
// `w` 是纯增量参数：带上它 `/poster` 就走按需缩放 + 磁盘缓存（见 api 层那条注释），
// 不带就按原图发 —— 所以 0 与以前的行为逐字一致，既有调用方一个都不受影响。
//
// 详情抽屉里的图（演员头像 w185、剧照 w780）比卡片海报大得多，不缩的话一次开抽屉
// 就是几 MB，所以那一侧拼 URL 时带上它。
func posterURLFromRelWidth(ref posterRef, rel string, width int) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return ""
	}
	base := "/api/admin/strm-scrape/poster?" + ref.query() + "&rel=" + pathEscape(rel)
	if width > 0 {
		return fmt.Sprintf("%s&w=%d", base, width)
	}
	return base
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// RebuildIndex 扫盘重建某个**数据源**的索引。
//
// 只给 tmdb 那面墙用：番号那套不落索引库（直接扫本地目录，见 javWallRows），
// 给它建索引只会留下一个没人读的 sqlite。
func (s *Service) RebuildIndex(ctx context.Context, src *wallSrc) error {
	if err := requireTmdbSource(src); err != nil {
		return err
	}
	return s.withTaskIndexLock(src.ScopeKey(), func() error {
		return s.rebuildIndexLocked(ctx, src)
	})
}

func (s *Service) rebuildIndexLocked(ctx context.Context, src *wallSrc) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filepath.Abs(src.Root)
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
	works = filterWorksByScope(works, s.GetScope(src.ScopeKey()).ExcludedDirs)
	items := make([]Item, 0, len(works))
	rels := make([]string, 0, len(works))
	for _, g := range works {
		if err := ctx.Err(); err != nil {
			return err
		}
		it := buildItem(src.PosterRef(), root, g)
		items = append(items, it)
		rels = append(rels, itemPosterRel(root, g, it.MediaType))
	}

	db, err := openTaskIndexDB(s.indexPath(src.ScopeKey()))
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

func (s *Service) upsertIndexItem(ctx context.Context, src *wallSrc, root string, g workGroup) {
	_ = s.withTaskIndexLock(src.ScopeKey(), func() error {
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
		if !s.indexFileExists(src.ScopeKey()) {
			return s.rebuildIndexLocked(ctx, src)
		}
		db, err := openTaskIndexDB(s.indexPath(src.ScopeKey()))
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
				return s.rebuildIndexLocked(ctx, src)
			}
		}
		it := buildItem(src.PosterRef(), root, g)
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

func scanIndexItems(rows *sql.Rows, ref posterRef) ([]Item, error) {
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
		it.PosterURL = posterURLFromRel(ref, posterRel)
		out = append(out, it)
	}
	return out, rows.Err()
}

func readIndexStats(db *sql.DB) (ItemListStats, error) {
	var stats ItemListStats
	// ⚠️ 三个 SUM 都要 COALESCE：**空表**时聚合返回 NULL，直接 Scan 进 int 会
	// "converting NULL to int is unsupported" → 整个列表接口 500。
	//
	// 这条以前踩不到，是因为任务源的索引库只在**有作品**时才建；现在的
	// `refresh-index` 会先给目录建一个空索引再扫（本地目录首屏就是这条路），
	// 于是「空的 items 表」成了常态：选一个刚挂上、里面还没扫出东西的目录，
	// 或者**一个作品都没有的目录**，列表就整片报「服务内部错误」。
	// COUNT(*) 天生返回 0，不受影响。
	err := db.QueryRow(`
SELECT
  COUNT(*),
  COALESCE(SUM(CASE WHEN status = 'ok' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status = 'miss' THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE WHEN status = 'doubt' THEN 1 ELSE 0 END), 0)
FROM items
`).Scan(&stats.Total, &stats.OK, &stats.Miss, &stats.Doubt)
	return stats, err
}

func (s *Service) listIndexItems(scopeKey string, ref posterRef, query ItemListQuery) (ItemListResult, error) {
	db, err := openTaskIndexDB(s.indexPath(scopeKey))
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
	items, err := scanIndexItems(rows, ref)
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

func (s *Service) ensureIndexLocked(ctx context.Context, src *wallSrc, root string) error {
	rootAbs := root
	if abs, err := filepath.Abs(root); err == nil {
		rootAbs = abs
	}
	if !s.indexFileExists(src.ScopeKey()) {
		return s.rebuildIndexLocked(ctx, src)
	}
	db, err := openTaskIndexDB(s.indexPath(src.ScopeKey()))
	if err != nil {
		return s.rebuildIndexLocked(ctx, src)
	}
	defer db.Close()
	if ver, ok := readIndexMeta(db, "schema"); !ok || ver != indexSchemaVersion {
		// 任务索引是可重建缓存，不做列迁移；先关闭并删除旧库，兼容 Windows 文件锁。
		_ = db.Close()
		RemoveSourceIndex(s.dataDir, src.ScopeKey())
		return s.rebuildIndexLocked(ctx, src)
	}
	if stored, ok := readIndexMeta(db, "root"); ok && stored != "" {
		storedAbs := stored
		if abs, err := filepath.Abs(stored); err == nil {
			storedAbs = abs
		}
		if storedAbs != rootAbs {
			return s.rebuildIndexLocked(ctx, src)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&n); err != nil || n == 0 {
		// 空索引常见于目录当时为空或重建中断；再扫一次对齐磁盘
		return s.rebuildIndexLocked(ctx, src)
	}
	return nil
}
