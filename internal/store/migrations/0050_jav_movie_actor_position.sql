-- 给「影片 ↔ 演员」关联表加一列顺序，保住上游那份演员表的**原始次序**。
--
-- 起因（2026-10-03 真机）：用户库里的 nfo 把 `<set>`（Emby/Kodi 的「所属合集」）
-- 写成了 `デカ吉` —— 一个男优。追下去是两件事叠在一起：
--
--   1. `ListActors` 用的是 `ORDER BY a.name COLLATE NOCASE`（按名字排序），
--      而上游 JAVDB 的演员数组是**有次序语义的**：主要女演员在前、男优与导演之类在后。
--      按名字排之后 `デカ吉`（か行）就排到了 `彩月七緒`（さ行）前面，nfo 的
--      `Actors[0]` 于是取到了男优。
--   2. `<set>` 的取值完全不看性别，谁排第一谁就是合集。
--
-- 次序其实**一直在库里**：`jav_movie_actors` 是普通表（有 rowid），
-- `ReplaceMovieActors` 按上游顺序逐条 INSERT，所以 rowid 次序 == 上游次序。
-- 已用真库抽查核对（CAWD-987 / REBD-916 / SVDVD-456 / JUX-215 全部逐位吻合）。
-- 只是**读的时候**被名字排序盖掉了 —— 这一列就是把那份次序显式记下来。
--
-- 回填用 `rowid - MIN(rowid)` 而不是 `COUNT(*)`：两者在真库上验过等价（12989 行零差异），
-- 但前者不依赖「同一个 movie 内 rowid 连续」这个更强的前提。
-- `ReplaceMovieActors` 会先 DELETE 再 INSERT，所以它写出来的行 rowid 必然连续；
-- 而这一列一旦写进去，后续就只按它排序，与 rowid 再无关系。
ALTER TABLE jav_movie_actors ADD COLUMN position INTEGER NOT NULL DEFAULT 0;

UPDATE jav_movie_actors
SET position = rowid - (
  SELECT MIN(b.rowid) FROM jav_movie_actors b WHERE b.movie_id = jav_movie_actors.movie_id
);

-- ListActors 现在按 (movie_id, position) 取，这条索引让它不必回表排序。
CREATE INDEX IF NOT EXISTS idx_jav_movie_actors_movie_pos ON jav_movie_actors(movie_id, position);
