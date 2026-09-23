-- 评论「扫过没有」的台账。
--
-- 为什么要单独记一笔：后台要把影库里没抓过评论的片逐部扫一遍，而**没评论**的片
-- 与**还没扫过**的片在库里长得一模一样（评论表里都是零行），不记一笔就会把同一批
-- 没评论的片反复扫。这是「评论区分享 / 分享者」那一档能列全的前提 ——
-- 那两档只能看到**已经抓过评论**的影片。
CREATE TABLE IF NOT EXISTS jav_review_sweeps (
    movie_id   TEXT PRIMARY KEY,
    checked_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 扫的时候按「扫得最久的优先」，这个索引给它用。
CREATE INDEX IF NOT EXISTS idx_jav_review_sweeps_at ON jav_review_sweeps(checked_at);
