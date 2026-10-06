package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"litepan/internal/domain"
)

// ——————————————————————— jav_movies / jav_actors / jav_magnets / jav_reviews ———————————————————————

type javMovieRepo struct{ db *DB }

const javMovieColumns = `id, number, title, origin_title, title_zh, title_zh_source,
       cover_url, thumb_url, javbus_cover,
       duration, release_date, score, summary, summary_source, review,
       director_id, director_name, maker_id, maker_name, publisher_id, publisher_name,
       series_id, series_name, tags_json, preview_images_json, preview_video_url,
       magnets_count, reviews_count, has_cnsub, has_preview_images, has_preview_video,
       can_play, type, number_letter, raw_json, fetched_at, last_viewed_at, created_at, updated_at,
       sidecar_synced_at`

// Upsert 写入或覆盖影片元数据。
//
// 刻意不用 INSERT OR REPLACE：那会先删后插，把 created_at 一起重置成当前时间。
// 榜单每次刷新都会重写整页影片，created_at 被反复重置的话「什么时候首次见到它」
// 这个信息就永久丢了。所以走 ON CONFLICT DO UPDATE，只更新业务列。
func (r *javMovieRepo) Upsert(ctx context.Context, m *domain.JavMovie) error {
	if m == nil || strings.TrimSpace(m.ID) == "" {
		return domain.Errorf(domain.CodeValidation, "无效的影片")
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_movies(id, number, title, origin_title, title_zh, title_zh_source, cover_url, thumb_url, javbus_cover,
                       duration, release_date, score, summary, summary_source, review,
                       director_id, director_name, maker_id, maker_name, publisher_id, publisher_name,
                       series_id, series_name, tags_json, preview_images_json, preview_video_url,
                       magnets_count, reviews_count, has_cnsub, has_preview_images, has_preview_video,
                       can_play, type, number_letter, raw_json, fetched_at, last_viewed_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
    number=excluded.number, title=excluded.title, origin_title=excluded.origin_title,
    -- 中文标题同 javbus_cover / summary：**只有补到了才覆盖**。JAVDB 那几条路
    -- （榜单刷新、影库同步）根本带不出这个字段，无条件写会把它抹成空串。
    title_zh=CASE WHEN excluded.title_zh <> '' THEN excluded.title_zh ELSE jav_movies.title_zh END,
    title_zh_source=CASE WHEN excluded.title_zh <> '' THEN excluded.title_zh_source ELSE jav_movies.title_zh_source END,
    cover_url=excluded.cover_url, thumb_url=excluded.thumb_url,
    -- javbus_cover 只在本次带值时才覆盖：榜单刷新时上游给不出它，
    -- 无条件写会把之前 JAVBUS 抓到的干净封面抹成空串。
    javbus_cover=CASE WHEN excluded.javbus_cover <> '' THEN excluded.javbus_cover ELSE jav_movies.javbus_cover END,
    -- duration / score / tags_json 三列**同 javbus_cover**：只有详情接口才给得出它们。
    --
    -- 起因（2026-10-04 真机）：用户说「打开详情页总是信息不全」。实测全库 2194 部
    -- 有 raw_json 的片里，**score 被冲空 481 部（21%）、tags 338 部（15%）、
    -- duration 28 部** —— 而 raw_json 里那些值**都好好的**（那正是它存在的意义）。
    --
    -- 冲它的是**摘要行**（榜单刷新 / 影库同步 / 搜索，upsertSummaries 那条路）：
    -- 列表接口不返回这三项，归一化后就是 0 / 空数组，而无条件写会盖掉详情抓来的值。
    -- 实测被冲的那批 updated_at 集中在 2026-09（448 部），与榜单/影库同步跑过的时间吻合。
    duration=CASE WHEN excluded.duration > 0 THEN excluded.duration ELSE jav_movies.duration END,
    release_date=excluded.release_date,
    score=CASE WHEN excluded.score > 0 THEN excluded.score ELSE jav_movies.score END,
    -- summary / review 同 javbus_cover：**只有详情接口才给得出它们**，
    -- 榜单与影库同步那些列表入库不带这两个字段（实测列表入库时它们是空串）。
    -- 现在库里的数据还没被冲过（三处一致），但这两列一直没这道保护 ——
    -- 哪天那 435 部有简介的片被榜单再入库一次，就被抹掉了，而且看不出是谁干的。
    summary=CASE WHEN excluded.summary <> '' THEN excluded.summary ELSE jav_movies.summary END,
    -- 来源跟着简介走：只有确实写进了新简介时才更新（否则「补来的」标签会飘到一个
    -- 空简介上，之后想重刷都挑不出该重刷的行）。
    summary_source=CASE WHEN excluded.summary <> '' THEN excluded.summary_source ELSE jav_movies.summary_source END,
    review=CASE WHEN excluded.review <> '' THEN excluded.review ELSE jav_movies.review END,
    director_id=excluded.director_id, director_name=excluded.director_name,
    maker_id=excluded.maker_id, maker_name=excluded.maker_name,
    publisher_id=excluded.publisher_id, publisher_name=excluded.publisher_name,
    series_id=excluded.series_id, series_name=excluded.series_name,
    -- tags_json 同 duration：列表接口不给标签，空数组不该盖掉详情抓来的。
    tags_json=CASE WHEN excluded.tags_json NOT IN ('', '[]') THEN excluded.tags_json ELSE jav_movies.tags_json END,
    -- preview_images_json 与 tags_json **逐字同形**（原来它漏了这道保护）。
    --
    -- 起因（2026-10-05 真机）：用户报「详情页打开有些内容是空的、要等自动获取才显示」。
    -- 实测群晖库 8178 部抓过详情的片子里 **425 部的 raw_json 里有剧照（最多 19 张）
    -- 而这一列是「空数组」** —— 摘要行（榜单 / 影库同步 / 搜索）不带剧照，无条件写就把它冲掉了。
    --
    -- 详情页首屏走 DetailLocal（只读本地），assembleDetail 直接把这一列塞进响应，
    -- 所以被冲空的片子**剧照那一块整个不渲染**，直到 hydrate / 「重新获取」再抓一次详情。
    -- 与 duration / score / tags / type 那一批是同一个 bug 的同一个位置（443fe11 漏了它）。
    preview_images_json=CASE WHEN excluded.preview_images_json NOT IN ('', '[]') THEN excluded.preview_images_json ELSE jav_movies.preview_images_json END,
    -- preview_video_url 同 javbus_cover：**只有详情接口才给得出它**，榜单/影库那些
    -- 列表入库时不带这个字段，无条件写会把详情抓来的地址冲成空串 —— 实测 60 部里
    -- 空掉了 15 部，而且看不出是谁干的。
    --
    -- 注意它天然是**会过期**的：上游签发的地址带 sign/t，十几个小时就失效。
    -- 这道保护只保证「同一天内的列表刷新不会白抹一次」，真要播还是得现取
    -- （见 jav.FreshPreviewVideoURL）。
    preview_video_url=CASE WHEN excluded.preview_video_url <> '' THEN excluded.preview_video_url ELSE jav_movies.preview_video_url END,
    -- magnets_count / reviews_count / 四个布尔标记：**按「这次是不是详情路」整组判**
    -- （判据「raw_json 非空」，与下面 type 那条同一个道理）。
    --
    -- 为什么不逐列判「值是不是 0」：这几个的 0 / false 都是**合法值**
    -- （真的没有磁链、真的不能播），光看值分不出「真没有」与「列表接口没给」。
    -- 而实测摘要行带进来的是列表页上的磁链数（常常是 0）、布尔一律 false，
    -- 于是详情的真值被盖掉 —— 抽样 400 部里 237 部「raw 有磁链但 can_play=0」。
    --
    -- 判据取 raw_json 而不是「值非零」的代价：**摘要行永远不更新这几列**。
    -- 这是刻意的取舍（宁可漏更新，不可冲空），与 type 那条一致。
    --
    -- ⚠️ 六列必须**整组**同判据：它们是一组快照（有没有磁链 / 有没有剧照 / 能不能播），
    -- 半新半旧会出现「has_preview_images=1 而 preview_images_json=[]」这种自相矛盾的行。
    magnets_count=CASE WHEN excluded.raw_json <> '' THEN excluded.magnets_count ELSE jav_movies.magnets_count END,
    reviews_count=CASE WHEN excluded.raw_json <> '' THEN excluded.reviews_count ELSE jav_movies.reviews_count END,
    has_cnsub=CASE WHEN excluded.raw_json <> '' THEN excluded.has_cnsub ELSE jav_movies.has_cnsub END,
    has_preview_images=CASE WHEN excluded.raw_json <> '' THEN excluded.has_preview_images ELSE jav_movies.has_preview_images END,
    has_preview_video=CASE WHEN excluded.raw_json <> '' THEN excluded.has_preview_video ELSE jav_movies.has_preview_video END,
    can_play=CASE WHEN excluded.raw_json <> '' THEN excluded.can_play ELSE jav_movies.can_play END,
    -- type 也**只有详情接口才给得出**，而且判据必须挂在 raw_json 上 ——
    -- 因为 '0'（有码）**既是合法值、也是 MovieTypeOf 的兜底值**：
    -- 列表接口不返回 type，归一化时 strings.TrimSpace(m.Type.String()) 是空串，
    -- 于是兜底成 '0'。光看值分不出「真是有码」与「没给」。
    --
    -- 而 raw_json 只有详情路才带（摘要行传的是空串）—— 所以拿它当判据是唯一可靠的。
    -- 代价：**摘要行永远不更新 type**。这是刻意的取舍（宁可漏更新，不可冲空），
    -- 将来上游若在列表里也给了 type，这条得改成「列表也带 raw」之类的方案。
    --
    -- 实测代价（2026-10-04）：63 部**无码片**（raw 里 type='1'）被摘要行冲成了 '0'，
    -- 表现是详情页/水印判定把它们当有码。
    type=CASE WHEN excluded.raw_json <> '' THEN excluded.type ELSE jav_movies.type END,
    number_letter=excluded.number_letter,
    -- raw_json 同理：摘要是从列表接口来的、没有 raw，别把详情的 raw 冲掉。
    raw_json=CASE WHEN excluded.raw_json <> '' THEN excluded.raw_json ELSE jav_movies.raw_json END,
    fetched_at=excluded.fetched_at,
    updated_at=CURRENT_TIMESTAMP`,
		m.ID, m.Number, m.Title, m.OriginTitle, m.TitleZH, m.TitleZHSource, m.CoverURL, m.ThumbURL, m.JavbusCover,
		nullableInt(m.Duration), m.ReleaseDate, nullableFloat(m.Score), m.Summary, m.SummarySource, m.Review,
		m.DirectorID, m.DirectorName, m.MakerID, m.MakerName, m.PublisherID, m.PublisherName,
		m.SeriesID, m.SeriesName, jsonOr(m.Tags, "[]"), jsonOr(m.PreviewImages, "[]"), m.PreviewVideoURL,
		m.MagnetsCount, m.ReviewsCount, boolToInt(m.HasCNSub), boolToInt(m.HasPreviewImages),
		boolToInt(m.HasPreviewVideo), boolToInt(m.CanPlay), m.Type, m.NumberLetter, m.RawJSON,
		tsValue(m.FetchedAt), tsValue(m.LastViewed),
	)
	return wrapDB(err)
}

func (r *javMovieRepo) Get(ctx context.Context, id string) (*domain.JavMovie, error) {
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javMovieColumns+` FROM jav_movies WHERE id=?`, id)
	return scanJavMovie(row)
}

func (r *javMovieRepo) GetByNumber(ctx context.Context, number string) (*domain.JavMovie, error) {
	n := strings.TrimSpace(number)
	if n == "" {
		return nil, domain.Errorf(domain.CodeNotFound, "影片不存在")
	}
	// 番号大小写不敏感，且优先取有封面的那条 —— 榜单摘要行常常没有封面。
	row := r.db.read.QueryRowContext(ctx, `SELECT `+javMovieColumns+` FROM jav_movies
WHERE number = ? COLLATE NOCASE
ORDER BY (CASE WHEN cover_url <> '' THEN 0 ELSE 1 END), updated_at DESC LIMIT 1`, n)
	return scanJavMovie(row)
}

func (r *javMovieRepo) List(ctx context.Context, f domain.JavMovieFilter) ([]*domain.JavMovie, int, error) {
	conds, params := javMovieConds(f)
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM jav_movies`+where, params...).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}

	order := javMovieOrder(f)
	q := `SELECT ` + javMovieColumns + ` FROM jav_movies` + where + order + ` LIMIT ? OFFSET ?`
	params = append(params, clampLimit(f.Limit, 60), maxInt(f.Offset, 0))

	rows, err := r.db.read.QueryContext(ctx, q, params...)
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()
	out, err := scanJavMovies(rows)
	if err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func javMovieConds(f domain.JavMovieFilter) ([]string, []any) {
	conds := make([]string, 0, 4)
	params := make([]any, 0, 4)

	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + kw + "%"
		conds = append(conds, `(number LIKE ? OR title LIKE ? OR origin_title LIKE ?)`)
		params = append(params, like, like, like)
	}
	if t := strings.TrimSpace(f.Type); t != "" {
		conds = append(conds, `type = ?`)
		params = append(params, t)
	}
	if y := strings.TrimSpace(f.Year); y != "" {
		conds = append(conds, `substr(release_date, 1, 4) = ?`)
		params = append(params, y)
	}
	if tag := strings.TrimSpace(f.Tag); tag != "" {
		// tags_json 是字符串数组，用 json_each 展开匹配。
		conds = append(conds, `EXISTS (SELECT 1 FROM json_each(jav_movies.tags_json) je WHERE je.value = ?)`)
		params = append(params, tag)
	}
	if f.NumberOnly && strings.TrimSpace(f.NumberExact) != "" {
		conds = append(conds, `number = ? COLLATE NOCASE`)
		params = append(params, strings.TrimSpace(f.NumberExact))
	} else if f.NumberOnly {
		conds = append(conds, `number <> ''`)
	}
	return conds, params
}

func javMovieOrder(f domain.JavMovieFilter) string {
	dir := "DESC"
	if !f.Desc {
		dir = "ASC"
	}
	switch f.Sort {
	case "score":
		// 没评分的排在最后，而不是当 0 分混在末尾的并列里。
		return ` ORDER BY (score IS NULL OR score = 0), score ` + dir + `, release_date DESC`
	case "release_date":
		return ` ORDER BY (release_date = ''), release_date ` + dir + `, id DESC`
	default:
		return ` ORDER BY updated_at DESC, id DESC`
	}
}

func (r *javMovieRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM jav_movies`).Scan(&n)
	return n, wrapDB(err)
}

func (r *javMovieRepo) GetMany(ctx context.Context, ids []string) ([]*domain.JavMovie, error) {
	out := make([]*domain.JavMovie, 0, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// 分批拼 IN：SQLite 的变量上限是 999，一批给 500 留足余量。
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			ph[i] = "?"
			args[i] = id
		}
		rows, err := r.db.read.QueryContext(ctx,
			`SELECT `+javMovieColumns+` FROM jav_movies WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return nil, wrapDB(err)
		}
		got, err := scanJavMovies(rows)
		rows.Close()
		if err != nil {
			return nil, err
		}
		out = append(out, got...)
	}
	return out, nil
}

func (r *javMovieRepo) TypesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	// 分批拼 IN：SQLite 的变量上限是 999，一批给 500 留足余量。
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			ph[i] = "?"
			args[i] = id
		}
		rows, err := r.db.read.QueryContext(ctx,
			`SELECT id, type FROM jav_movies WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return nil, wrapDB(err)
		}
		for rows.Next() {
			var id, t string
			if err := rows.Scan(&id, &t); err != nil {
				rows.Close()
				return nil, wrapDB(err)
			}
			out[id] = t
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, wrapDB(err)
		}
		rows.Close()
	}
	return out, nil
}

// TagVocabulary 返回库里出现过的全部标签（去重、按拼音/字典序）。
//
// 给「订阅 → 类别过滤」那两个下拉当选项源。**不用上游的标签接口**：
// 上游给的是一页影片各自的标签，攒不出全集；而过滤判的是「这部片有没有这个标签」，
// 判据是库里的 tags_json，所以选项也只能来自同一处 —— 否则会出现
// 「下拉里选得到、但库里没有任何一部片带它」的空选项。
//
// 没有走 `SELECT DISTINCT value FROM jav_movies, json_each(...)`：那是每行一次的
// 相关子查询，真库 15007 部片实测要 1.4 秒，而这个列表每次打开弹窗都要拉。
// 换成只扫 tags_json 一列、在 Go 侧去重，同一份数据 60 毫秒 ——
// 代价是标签字符串要在内存里过一遍，量级是「几千个短串」，可以忽略。
//
// 空数组与坏 JSON 都跳过（tags_json 是历史遗留列，早期有过非数组的写法）。
func (r *javMovieRepo) TagVocabulary(ctx context.Context) ([]string, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT tags_json FROM jav_movies WHERE tags_json IS NOT NULL AND tags_json <> '' AND tags_json <> '[]'`)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	seen := make(map[string]struct{}, 512)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, wrapDB(err)
		}
		var tags []string
		if err := json.Unmarshal([]byte(raw), &tags); err != nil {
			continue
		}
		for _, t := range tags {
			if t = strings.TrimSpace(t); t != "" {
				seen[t] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err)
	}

	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

func (r *javMovieRepo) MarkViewed(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_movies SET last_viewed_at=? WHERE id=?`, tsValue(at), id)
	return wrapDB(err)
}

// ReplaceMovieActors 用给定的演员集合整体替换该影片的关联。
//
// **入参顺序就是上游演员表的次序，必须原样存进 position**：上游那份是
// 「主要女演员在前、男优与导演之类在后」，nfo 的 `<set>`（演员合集）与详情页的
// 演员列表都按它排。丢掉次序的代价实测过：`<set>` 会取到按名字排序的第一个，
// 于是男优当了合集（见迁移 0050 的注释）。
//
// 去重与跳过空 id 之后再编号 —— 编在**最终写入的次序**上，不留空洞。
func (r *javMovieRepo) ReplaceMovieActors(ctx context.Context, movieID string, actorIDs []string) error {
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM jav_movie_actors WHERE movie_id=?`, movieID); err != nil {
		return wrapDB(err)
	}
	seen := make(map[string]struct{}, len(actorIDs))
	position := 0
	for _, id := range actorIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO jav_movie_actors(movie_id, actor_id, position) VALUES (?,?,?)`,
			movieID, id, position); err != nil {
			return wrapDB(err)
		}
		position++
	}
	return wrapDB(tx.Commit())
}

func (r *javMovieRepo) UpsertActor(ctx context.Context, a *domain.JavActor) error {
	if a == nil || strings.TrimSpace(a.ID) == "" {
		return domain.Errorf(domain.CodeValidation, "无效的演员")
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_actors(id, name, gender, avatar_url) VALUES (?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
    name=excluded.name,
    -- avatar 只在本次带值时覆盖，避免榜单摘要把已抓到的头像抹掉。
    avatar_url=CASE WHEN excluded.avatar_url <> '' THEN excluded.avatar_url ELSE jav_actors.avatar_url END,
    gender=COALESCE(excluded.gender, jav_actors.gender)`,
		a.ID, a.Name, nullableInt(a.Gender), a.AvatarURL)
	return wrapDB(err)
}

func (r *javMovieRepo) ActorsByIDs(ctx context.Context, ids []string) (map[string]*domain.JavActor, error) {
	out := make(map[string]*domain.JavActor, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := start + batch
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		ph := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, id := range chunk {
			ph[i] = "?"
			args[i] = id
		}
		rows, err := r.db.read.QueryContext(ctx,
			`SELECT id, name, gender, avatar_url FROM jav_actors WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
		if err != nil {
			return nil, wrapDB(err)
		}
		for rows.Next() {
			var (
				a      domain.JavActor
				gender sql.NullInt64
			)
			if err := rows.Scan(&a.ID, &a.Name, &gender, &a.AvatarURL); err != nil {
				rows.Close()
				return nil, wrapDB(err)
			}
			if gender.Valid {
				a.Gender = int(gender.Int64)
			}
			out[a.ID] = &a
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, wrapDB(err)
		}
		rows.Close()
	}
	return out, nil
}

// ListActors 取一部影片的演员，**按上游那份演员表的次序**（position 列）。
//
// 这里原本是 `ORDER BY a.name COLLATE NOCASE` —— 那个排序的代价是静默的：
// nfo 的 `<set>` 取 `Actors[0]`，于是合集变成了「按名字排序的第一个演员」，
// 男优 `デカ吉`（か行）就这样排到了女优 `彩月七緒`（さ行）前面（见迁移 0050）。
//
// 改成 position 之后，详情页的演员列表也顺带变成了「女演员在前、男优在后」，
// 与上游网页看到的一致。position 是 ReplaceMovieActors 按上游次序写的。
func (r *javMovieRepo) ListActors(ctx context.Context, movieID string) ([]*domain.JavActor, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT a.id, a.name, a.gender, a.avatar_url
FROM jav_movie_actors ma JOIN jav_actors a ON a.id = ma.actor_id
WHERE ma.movie_id = ? ORDER BY ma.position, a.name COLLATE NOCASE`, movieID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavActor, 0)
	for rows.Next() {
		var (
			a      domain.JavActor
			gender sql.NullInt64
		)
		if err := rows.Scan(&a.ID, &a.Name, &gender, &a.AvatarURL); err != nil {
			return nil, wrapDB(err)
		}
		if gender.Valid {
			a.Gender = int(gender.Int64)
		}
		out = append(out, &a)
	}
	return out, wrapDB(rows.Err())
}

func (r *javMovieRepo) ListMoviesByActor(ctx context.Context, actorID string) ([]*domain.JavMovie, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT `+prefixColumns("m", javMovieColumns)+`
FROM jav_movie_actors ma JOIN jav_movies m ON m.id = ma.movie_id
WHERE ma.actor_id = ?
ORDER BY (m.release_date = ''), m.release_date DESC, m.id DESC`, actorID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	return scanJavMovies(rows)
}

// MoviesWithAnyActor 返回 movieIDs 里**含有任一** actorIDs 的那些 movie_id。
//
// 给推送路径上的演员黑名单用（见 jav/push.go 的 dropBlacklistedPool）：一次推送要判
// 几百条候选，而判断「这部片的演员里有没有被拉黑的」需要 jav_movie_actors —— 逐候选
// 查库就是几百次 SQL，这里一次批量取回。
//
// **两侧都 LOWER 比对**，返回的 movie_id 也一律小写：黑名单的键由
// quality.CanonicalTargetKey 产出、强制小写，而 JAVDB 的 id 是大小写敏感的 base62
// （`ZY5eq` 与 `zy5eq` 是两个 id），拿小写键直接 IN 会漏掉带大写的那些。
// 表只有几千行，实测 LOWER 版比裸 IN 还快（5~13ms vs 115ms），不必为索引让步。
//
// 两个维度都分批，保证单条 SQL 的参数数留在 SQLite 的 999 上限之内。
func (r *javMovieRepo) MoviesWithAnyActor(ctx context.Context, movieIDs, actorIDs []string) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if len(movieIDs) == 0 || len(actorIDs) == 0 {
		return out, nil
	}
	// 单个 IN 子句的参数上限按 400 切：内外层各一次，合计最多 800 < 999。
	const batch = 400
	for aStart := 0; aStart < len(actorIDs); aStart += batch {
		aEnd := min(aStart+batch, len(actorIDs))
		aChunk := actorIDs[aStart:aEnd]
		aArgs := make([]any, len(aChunk))
		for i, id := range aChunk {
			aArgs[i] = id
		}
		for mStart := 0; mStart < len(movieIDs); mStart += batch {
			mEnd := min(mStart+batch, len(movieIDs))
			mChunk := movieIDs[mStart:mEnd]
			mArgs := make([]any, len(mChunk))
			for i, id := range mChunk {
				mArgs[i] = id
			}
			args := append(append([]any{}, aArgs...), mArgs...)
			rows, err := r.db.read.QueryContext(ctx,
				`SELECT DISTINCT movie_id FROM jav_movie_actors
				  WHERE LOWER(actor_id) IN (`+placeholders(len(aChunk))+`)
				    AND LOWER(movie_id) IN (`+placeholders(len(mChunk))+`)`, args...)
			if err != nil {
				return nil, wrapDB(err)
			}
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return nil, wrapDB(err)
				}
				out[strings.ToLower(id)] = struct{}{}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return nil, wrapDB(err)
			}
		}
	}
	return out, nil
}

// ——————————————————————— jav_magnets ———————————————————————

type javMagnetRepo struct{ db *DB }

const javMagnetColumns = `fingerprint, btih, movie_id, code, name, size_text, size_bytes, date_text,
       magnet, has_hd, has_sub, file_count, source, fetched_at, created_at`

func (r *javMagnetRepo) Upsert(ctx context.Context, m *domain.JavMagnet) (bool, error) {
	if m == nil || strings.TrimSpace(m.Fingerprint) == "" || strings.TrimSpace(m.Magnet) == "" {
		return false, domain.Errorf(domain.CodeValidation, "无效的磁链")
	}
	// 先查存在性再写：UPSERT 的 RowsAffected 覆盖也算 1，区分不出新旧。
	// 这个返回值只用于「本轮新增了几颗」的统计，不承担正确性，
	// 所以并发窗口可以接受，不值得为它开事务。
	var existed int
	if err := r.db.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jav_magnets WHERE fingerprint=?`, m.Fingerprint).Scan(&existed); err != nil {
		return false, wrapDB(err)
	}

	var size any
	if m.HasSize {
		size = m.SizeBytes
	}
	var files any
	if m.HasFiles {
		files = m.FileCount
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_magnets(fingerprint, btih, movie_id, code, name, size_text, size_bytes, date_text,
                        magnet, has_hd, has_sub, file_count, source, fetched_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(fingerprint) DO UPDATE SET
    movie_id=excluded.movie_id,
    code=CASE WHEN excluded.code <> '' THEN excluded.code ELSE jav_magnets.code END,
    name=CASE WHEN excluded.name <> '' THEN excluded.name ELSE jav_magnets.name END,
    size_text=CASE WHEN excluded.size_text <> '' THEN excluded.size_text ELSE jav_magnets.size_text END,
    size_bytes=COALESCE(excluded.size_bytes, jav_magnets.size_bytes),
    date_text=CASE WHEN excluded.date_text <> '' THEN excluded.date_text ELSE jav_magnets.date_text END,
    -- 角标是「或」语义：JAVBUS 不同批次给的信息完整度不同，
    -- 本次没标 HD 不代表上次标的 HD 是错的。
    has_hd=MAX(jav_magnets.has_hd, excluded.has_hd),
    has_sub=MAX(jav_magnets.has_sub, excluded.has_sub),
    file_count=COALESCE(excluded.file_count, jav_magnets.file_count),
    source=CASE WHEN excluded.source <> '' THEN excluded.source ELSE jav_magnets.source END,
    fetched_at=excluded.fetched_at`,
		m.Fingerprint, m.Btih, m.MovieID, m.Code, m.Name, m.SizeText, size, m.DateText,
		m.Magnet, boolToInt(m.HasHD), boolToInt(m.HasSub), files, m.Source, tsValue(m.FetchedAt))
	if err != nil {
		return false, wrapDB(err)
	}
	return existed == 0, nil
}

func (r *javMagnetRepo) ListByMovie(ctx context.Context, movieID string) ([]*domain.JavMagnet, error) {
	return r.listMagnets(ctx, `movie_id = ?`, movieID)
}

func (r *javMagnetRepo) ListByCode(ctx context.Context, code string) ([]*domain.JavMagnet, error) {
	return r.listMagnets(ctx, `code = ? COLLATE NOCASE`, strings.TrimSpace(code))
}

func (r *javMagnetRepo) listMagnets(ctx context.Context, where string, arg any) ([]*domain.JavMagnet, error) {
	// 按 rowid 倒序 = 后抓到的排前面。不能用 created_at：同一批抓取的
	// CURRENT_TIMESTAMP 完全相同，排序会退化成随机。
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javMagnetColumns+` FROM jav_magnets WHERE `+where+` ORDER BY rowid DESC`, arg)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavMagnet, 0)
	for rows.Next() {
		m, err := scanJavMagnet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapDB(rows.Err())
}

func (r *javMagnetRepo) CountByMovie(ctx context.Context, movieID string) (int, error) {
	var n int
	err := r.db.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM jav_magnets WHERE movie_id=?`, movieID).Scan(&n)
	return n, wrapDB(err)
}

// MarkSwept 记下「这部片的磁链已经问过一遍上游」。重复标记幂等，只刷新时间。
func (r *javMagnetRepo) MarkSwept(ctx context.Context, movieID string) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_magnet_sweeps(movie_id, checked_at) VALUES (?, CURRENT_TIMESTAMP)
ON CONFLICT(movie_id) DO UPDATE SET checked_at=CURRENT_TIMESTAMP`, movieID)
	return wrapDB(err)
}

// magnetResweepAfter 是「没磁链的片隔多久再问一遍」。
//
// 比评论那个 30 天短一档：磁链是**会**长出来的（新种子发布、上游补录），
// 而用户点开一部片最想看到的就是磁链。7 天一轮，一部片一年也就 50 来次。
const magnetResweepAfter = "7 days"

// PendingSweepMovieIDs 取还没问过磁链的影片 id，最近碰过的优先。
//
// 两条筛选合起来才是「该问的」：
//   - 磁链表里一颗都没有（有磁链就不用问了）；
//   - 没问过，或上次问已经是 magnetResweepAfter 前。
func (r *javMagnetRepo) PendingSweepMovieIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT m.id FROM jav_movies m
 WHERE NOT EXISTS (SELECT 1 FROM jav_magnets g WHERE g.movie_id = m.id)
   AND NOT EXISTS (
         SELECT 1 FROM jav_magnet_sweeps s
          WHERE s.movie_id = m.id AND s.checked_at > datetime('now', ?)
       )
 ORDER BY COALESCE(NULLIF(m.last_viewed_at, ''), m.fetched_at) DESC, m.id
 LIMIT ?`, "-"+magnetResweepAfter, clampLimit(limit, 25))
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]string, 0, 32)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out = append(out, id)
	}
	return out, wrapDB(rows.Err())
}

// ——————————————————————— jav_reviews ———————————————————————

type javReviewRepo struct{ db *DB }

const javReviewColumns = `id, movie_id, user_id, username, score, content, status, status_title,
       watched_count, likes_count, liked, created_at`

func (r *javReviewRepo) UpsertMany(ctx context.Context, movieID string, reviews []*domain.JavReview) error {
	if movieID == "" || len(reviews) == 0 {
		return nil
	}
	tx, err := r.db.write.BeginTx(ctx, nil)
	if err != nil {
		return wrapDB(err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, rv := range reviews {
		if rv == nil || rv.ID == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO jav_reviews(id, movie_id, user_id, username, score, content, status, status_title,
                        watched_count, likes_count, liked, created_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
    likes_count=excluded.likes_count, liked=excluded.liked, content=excluded.content`,
			rv.ID, movieID, rv.UserID, rv.Username, nullableFloat(rv.Score), rv.Content,
			rv.Status, rv.StatusTitle, rv.WatchedCount, rv.LikesCount,
			boolToInt(rv.Liked), rv.CreatedAt); err != nil {
			return wrapDB(err)
		}
	}
	return wrapDB(tx.Commit())
}

func (r *javReviewRepo) ListByMovie(ctx context.Context, movieID string, limit, offset int) ([]*domain.JavReview, int, error) {
	var total int
	if err := r.db.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM jav_reviews WHERE movie_id=?`, movieID).Scan(&total); err != nil {
		return nil, 0, wrapDB(err)
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javReviewColumns+` FROM jav_reviews WHERE movie_id=?
ORDER BY likes_count DESC, id DESC LIMIT ? OFFSET ?`,
		movieID, clampLimit(limit, 15), maxInt(offset, 0))
	if err != nil {
		return nil, 0, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavReview, 0)
	for rows.Next() {
		rv, err := scanJavReview(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, rv)
	}
	return out, total, wrapDB(rows.Err())
}

// PendingSummaryMovieIDs 取「还没有简介、该去别的站补」的影片 id，最近碰过的优先。
//
// 排序照 PendingSweepMovieIDs 那条：先补用户最近看过/刚入库的，尾巴慢慢来。
//
// 两个条件同时要满足（2026-09-27 起）：
//
//   - `enriched_at IS NULL` —— 补到过的不会再被挑中；
//   - `summary_attempts < summaryMaxAttempts` —— **问过几次都没结果的要收手**。
//
// 为什么不是「问过一次就记账」：那样上游哪天补了料、或我们加了新源，已经问过的
// 那批也永远捞不回来（实测正是这一条让几百部空简介卡死）。用次数兜底，
// 既会重试又有尽头。
func (r *javMovieRepo) PendingSummaryMovieIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT id FROM jav_movies
 WHERE enriched_at IS NULL
   AND summary_attempts < ?
   AND (
        summary IS NULL OR summary = ''
     OR title_zh IS NULL OR title_zh = ''
     OR director_name IS NULL OR director_name = ''
     OR duration IS NULL OR duration = 0
     OR release_date IS NULL OR release_date = ''
     OR number IS NULL OR number = ''
   )
 ORDER BY COALESCE(NULLIF(last_viewed_at, ''), fetched_at) DESC, id
 LIMIT ?`, summaryMaxAttempts, clampLimit(limit, 100))
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]string, 0, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out = append(out, id)
	}
	return out, wrapDB(rows.Err())
}

// summaryMaxAttempts 是「同一部最多问几轮」。
//
// 取 3：一轮里会把所有源都问一遍，3 轮就是「各家都给了 3 次机会」。再往上加
// 只会让永远补不上的那批（冷门片、没被任何站收录的国产）每轮都去打上游。
const summaryMaxAttempts = 3

// MarkEnriched 记下「这部片的缺失字段已经补过一轮」。
//
// **补到也记、没补到也记**——那正是"下轮别再问"的意思（这站没有这部片，问一次就够）。
// 失败不调用它（失败多半是限流，记了就再也不会试）。
func (r *javMovieRepo) MarkEnriched(ctx context.Context, movieID string) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_movies SET enriched_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=?`, movieID)
	return wrapDB(err)
}

// MarkSummaryFetched 记下「这部片的简介已经从 <source> 补过」。
//
// **只更新 source，不动 summary**（summary 由补到的正文单独写）—— 两者分开是为了
// 「补不到也要记一笔」：这站没有这部片，下轮就别再问了。写空串表示「问过了、没有」。
func (r *javMovieRepo) MarkSummaryFetched(ctx context.Context, movieID, source string) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_movies SET summary_source=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		strings.TrimSpace(source), movieID)
	return wrapDB(err)
}

// BumpSummaryAttempts 把「问过但什么都没补到」的次数 +1。
//
// 为什么要单独一列：`enriched_at` 是**一票否决**（记了就不再被挑中），而「这次没问到」
// 和「这部永远问不到」是两件事 —— 前者该重试，后者该放弃。用次数区分：
// 到 summaryMaxAttempts 次仍无结果，`PendingSummaryMovieIDs` 才不再挑它。
func (r *javMovieRepo) BumpSummaryAttempts(ctx context.Context, movieID string) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_movies SET summary_attempts = summary_attempts + 1, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		movieID)
	return wrapDB(err)
}

// PendingDetailMovieIDs 取「从没抓过详情」的影片 id（raw_json 为空），最近碰过的优先。
//
// 判据与理由见 domain.JavMovieRepository 里那条注释。排序照 PendingSummaryMovieIDs：
// 用户最近看过 / 刚入库的先补。
//
// **只挑有番号的**：番号是上游详情的入口（`/v4/movies/{id}` 用的是库里的 id，
// 但回填失败时日志与排查要靠番号定位），而真库里那 12813 部全都有番号 ——
// 加这一条是为了让「没有番号的行」永远不进候选，免得每轮都挑出来空跑一次。
func (r *javMovieRepo) PendingDetailMovieIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT id FROM jav_movies
 WHERE (raw_json IS NULL OR raw_json = '')
   AND number IS NOT NULL AND number <> ''
   AND detail_attempts < ?
 ORDER BY COALESCE(NULLIF(last_viewed_at, ''), fetched_at) DESC, id
 LIMIT ?`, detailMaxAttempts, clampLimit(limit, 100))
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()
	out := make([]string, 0, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out = append(out, id)
	}
	return out, wrapDB(rows.Err())
}

// CountPendingDetail 数「还没抓过详情」的部数（回填循环报进度用）。
//
// 用同一套判据（raw_json 为空 + 有番号），别另写一份 —— 两处一分家，
// 进度里说的「还剩 M 部」与实际每轮挑出来的批就对不上。
func (r *javMovieRepo) CountPendingDetail(ctx context.Context) (int, error) {
	var n int
	err := r.db.read.QueryRowContext(ctx, `
SELECT COUNT(*) FROM jav_movies
 WHERE (raw_json IS NULL OR raw_json = '')
   AND number IS NOT NULL AND number <> ''
   AND detail_attempts < ?`, detailMaxAttempts).Scan(&n)
	return n, wrapDB(err)
}

// detailMaxAttempts 是「同一部片最多试几轮详情」。
//
// 取 3（与 summaryMaxAttempts 同值，理由也同源）：一轮里问一次上游，
// 3 轮就是「上游三次都说没有」。再往上加只会让那批**上游根本没有**的片
// （国产/素人，实测真库里占比不小）每轮都白打一次 —— 而不封顶的话
// 「还剩 M 部」这个进度数永远不会归零，用户分不清「补完了」与「卡住了」。
//
// 成功不涨计数（raw_json 一写进去就自动离开候选集），只有失败才 +1。
const detailMaxAttempts = 3

// BumpDetailAttempts 把「这部片的详情没抓到」的次数 +1。
//
// 与 BumpSummaryAttempts 同一套语义：失败要记账，否则那批永远补不上的片
// 会每轮重来一遍。**成功不调它** —— 成功的判据是 raw_json 被写进去了。
func (r *javMovieRepo) BumpDetailAttempts(ctx context.Context, movieID string) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_movies SET detail_attempts = detail_attempts + 1, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		movieID)
	return wrapDB(err)
}

// MarkSidecarSynced 记下这部片的元数据已经回写过本地侧车。
//
// **找到侧车就记**（改了没改都记）—— 侧车里已经是新值就不必再走一遍 Walk。
// 「没找到侧车」要不要记由调用方判断，见 domain.JavMovieRepository 里那条注释。
func (r *javMovieRepo) MarkSidecarSynced(ctx context.Context, movieID string) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx,
		`UPDATE jav_movies SET sidecar_synced_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=?`, movieID)
	return wrapDB(err)
}

// MarkSwept 记下这部片的评论已经扫过一遍（见 domain 里那条注释）。
// 重复标记是幂等的，只刷新时间。
func (r *javReviewRepo) MarkSwept(ctx context.Context, movieID string) error {
	movieID = strings.TrimSpace(movieID)
	if movieID == "" {
		return nil
	}
	_, err := r.db.write.ExecContext(ctx, `
INSERT INTO jav_review_sweeps(movie_id, checked_at) VALUES (?, CURRENT_TIMESTAMP)
ON CONFLICT(movie_id) DO UPDATE SET checked_at=CURRENT_TIMESTAMP`, movieID)
	return wrapDB(err)
}

// reviewResweepAfter 是「没评论的片隔多久再扫一遍」。
//
// 永久拉黑是不对的：一部片今天没人评论，不代表以后也没有 —— 而分享者那一档
// 恰恰靠这些评论吃饭。30 天是个折中：新片有人评论很快会被扫到，
// 老片也不会被反复打。
const reviewResweepAfter = "30 days"

// PendingSweepMovieIDs 取还没扫过评论的影片 id，最近碰过的优先。
//
// 三条筛选合起来才是「该扫的」：
//   - 评论表里一行都没有（有评论就不用扫了）；
//   - 没扫过，或者上次扫已经是 30 天前（见 reviewResweepAfter）。
func (r *javReviewRepo) PendingSweepMovieIDs(ctx context.Context, limit int) ([]string, error) {
	rows, err := r.db.read.QueryContext(ctx, `
SELECT m.id FROM jav_movies m
 WHERE NOT EXISTS (SELECT 1 FROM jav_reviews r WHERE r.movie_id = m.id)
   AND NOT EXISTS (
         SELECT 1 FROM jav_review_sweeps s
          WHERE s.movie_id = m.id AND s.checked_at > datetime('now', ?)
       )
 ORDER BY COALESCE(NULLIF(m.last_viewed_at, ''), m.fetched_at) DESC, m.id
 LIMIT ?`, "-"+reviewResweepAfter, clampLimit(limit, 25))
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]string, 0, 32)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrapDB(err)
		}
		out = append(out, id)
	}
	return out, wrapDB(rows.Err())
}

// ListByUser 取某个用户发过的全部评论，按时间倒序。
func (r *javReviewRepo) ListByUser(ctx context.Context, userID int64) ([]*domain.JavReview, error) {
	if userID <= 0 {
		return nil, nil
	}
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javReviewColumns+` FROM jav_reviews WHERE user_id=?
ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavReview, 0)
	for rows.Next() {
		rv, err := scanJavReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, wrapDB(rows.Err())
}

// ListByMovieAll 取某部影片的全部评论，按时间倒序。
//
// 倒序与源码 _comment_magnets 的 ORDER BY created_at DESC 一致 —— 分享档里
// 同一质量档位内是按分享时间排的。
func (r *javReviewRepo) ListByMovieAll(ctx context.Context, movieID string) ([]*domain.JavReview, error) {
	rows, err := r.db.read.QueryContext(ctx,
		`SELECT `+javReviewColumns+` FROM jav_reviews WHERE movie_id=?
ORDER BY created_at DESC, id DESC`, movieID)
	if err != nil {
		return nil, wrapDB(err)
	}
	defer rows.Close()

	out := make([]*domain.JavReview, 0)
	for rows.Next() {
		rv, err := scanJavReview(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rv)
	}
	return out, wrapDB(rows.Err())
}

// scanJavReview 把一行评论读出来。ListByMovie 与 ListByMovieAll 共用 ——
// 两处各写一份扫描，迟早会有一处漏字段。
func scanJavReview(sc javRowScanner) (*domain.JavReview, error) {
	var (
		rv    domain.JavReview
		score sql.NullFloat64
		liked int
	)
	if err := sc.Scan(&rv.ID, &rv.MovieID, &rv.UserID, &rv.Username, &score, &rv.Content,
		&rv.Status, &rv.StatusTitle, &rv.WatchedCount, &rv.LikesCount,
		&liked, &rv.CreatedAt); err != nil {
		return nil, wrapDB(err)
	}
	if score.Valid {
		rv.Score = score.Float64
	}
	rv.Liked = liked != 0
	return &rv, nil
}

// ——————————————————————— 扫描与工具 ———————————————————————

type javRowScanner interface{ Scan(dest ...any) error }

func scanJavMovie(sc javRowScanner) (*domain.JavMovie, error) {
	var (
		m         domain.JavMovie
		duration  sql.NullInt64
		score     sql.NullFloat64
		tags      string
		previews  string
		fetchedAt sql.NullString
		lastView  sql.NullString
		createdAt sql.NullString
		updatedAt sql.NullString
		// sidecarSyncedAt 是 0051 加的列。用 NullString 而不是 time.Time：
		// 绝大多数行是 NULL（还没回写过），而 NULL 与零值时间必须区分得开。
		sidecarSyncedAt sql.NullString
		hasCN           int
		hasPrevImg      int
		hasPrevVid      int
		canPlay         int
	)
	err := sc.Scan(&m.ID, &m.Number, &m.Title, &m.OriginTitle, &m.TitleZH, &m.TitleZHSource,
		&m.CoverURL, &m.ThumbURL, &m.JavbusCover,
		&duration, &m.ReleaseDate, &score, &m.Summary, &m.SummarySource, &m.Review,
		&m.DirectorID, &m.DirectorName, &m.MakerID, &m.MakerName, &m.PublisherID, &m.PublisherName,
		&m.SeriesID, &m.SeriesName, &tags, &previews, &m.PreviewVideoURL,
		&m.MagnetsCount, &m.ReviewsCount, &hasCN, &hasPrevImg, &hasPrevVid,
		&canPlay, &m.Type, &m.NumberLetter, &m.RawJSON, &fetchedAt, &lastView, &createdAt, &updatedAt,
		&sidecarSyncedAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	if duration.Valid {
		m.Duration = int(duration.Int64)
	}
	if score.Valid {
		m.Score = score.Float64
	}
	_ = json.Unmarshal([]byte(tags), &m.Tags)
	_ = json.Unmarshal([]byte(previews), &m.PreviewImages)
	m.HasCNSub = hasCN != 0
	m.HasPreviewImages = hasPrevImg != 0
	m.HasPreviewVideo = hasPrevVid != 0
	m.CanPlay = canPlay != 0
	m.FetchedAt = parseTS(fetchedAt)
	m.LastViewed = parseTS(lastView)
	m.CreatedAt = parseTS(createdAt)
	m.UpdatedAt = parseTS(updatedAt)
	m.SidecarSyncedAt = parseTS(sidecarSyncedAt)
	return &m, nil
}

func scanJavMovies(rows *sql.Rows) ([]*domain.JavMovie, error) {
	out := make([]*domain.JavMovie, 0)
	for rows.Next() {
		m, err := scanJavMovie(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, wrapDB(rows.Err())
}

func scanJavMagnet(sc javRowScanner) (*domain.JavMagnet, error) {
	var (
		m         domain.JavMagnet
		size      sql.NullInt64
		files     sql.NullInt64
		hasHD     int
		hasSub    int
		fetchedAt sql.NullString
		createdAt sql.NullString
	)
	err := sc.Scan(&m.Fingerprint, &m.Btih, &m.MovieID, &m.Code, &m.Name, &m.SizeText, &size,
		&m.DateText, &m.Magnet, &hasHD, &hasSub, &files, &m.Source, &fetchedAt, &createdAt)
	if err != nil {
		return nil, wrapDB(err)
	}
	if size.Valid {
		m.SizeBytes, m.HasSize = size.Int64, true
	}
	if files.Valid {
		m.FileCount, m.HasFiles = int(files.Int64), true
	}
	m.HasHD = hasHD != 0
	m.HasSub = hasSub != 0
	m.FetchedAt = parseTS(fetchedAt)
	m.CreatedAt = parseTS(createdAt)
	return &m, nil
}

// prefixColumns 给逗号分隔的列名加上表别名，用于 JOIN 查询。
func prefixColumns(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

func nullableInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullableFloat(v float64) any {
	if v == 0 {
		return nil
	}
	return v
}

func clampLimit(v, def int) int {
	if v <= 0 {
		return def
	}
	if v > 1000 {
		return 1000
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
