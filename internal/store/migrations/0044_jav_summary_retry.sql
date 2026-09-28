-- 0044：让「问过但什么都没补到」的片还能被重问。
--
-- 背景（2026-09-27）：补简介那条后台循环（internal/jav/loops.go 的 backfillSummary）
-- 原来在「问过所有源、一家都没给」时也调 MarkEnriched —— 那一笔是**一票否决**，
-- 而 PendingSummaryMovieIDs 的候选条件就是 `enriched_at IS NULL`。于是那批片
-- **永远不会再被挑中**：上游后来补了料、我们后来加了新源（airav / missav），
-- 都救不回来。实测用户库里简介为空的大头正是这么卡住的。
--
-- 两件事一起做：
--   1. 新列 summary_attempts：同一部最多问 3 轮，到顶才收手（既会重试也有尽头）。
--   2. 把存量那批「白问一场」的记账清掉，让新源重新问一遍。
--      只清「简介仍为空」的行 —— 补到过的不动（它们的 summary_source 有值）。

ALTER TABLE jav_movies ADD COLUMN summary_attempts INTEGER NOT NULL DEFAULT 0;

-- 只重置「真的什么都没补到」的那批：
--   enriched_at 记过账（说明问过），但 summary 与 summary_source 都还是空。
-- 已补到的不满足 WHERE 的后两条，不会被碰。
UPDATE jav_movies
   SET enriched_at       = NULL,
       summary_source    = '',
       summary_attempts  = 0,
       updated_at        = CURRENT_TIMESTAMP
 WHERE enriched_at IS NOT NULL
   AND (summary IS NULL OR summary = '')
   AND (summary_source IS NULL OR summary_source = '');
