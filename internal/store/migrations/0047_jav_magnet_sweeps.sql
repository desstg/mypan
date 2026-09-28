-- 磁链「扫过没有」的台账。
--
-- 与 0030 的 jav_review_sweeps 同一个理由：后台要把影库里没抓过磁链的片逐部扫一遍，
-- 而**这部片真没有磁链**与**还没扫过**在库里长得一模一样（jav_magnets 里都是零行）。
-- 不记一笔就会把同一批没磁链的片反复扫 —— 实测真库 8770 部里 6454 部没有磁链，
-- 反复扫它们等于每天白打一万多次上游请求。
--
-- 判据仍然是「上游真的回过了」才记（见 magnetSweepLoop）：失败/限流不记，
-- 否则越不通越扫不上（与评论那本账同一条规矩）。
CREATE TABLE IF NOT EXISTS jav_magnet_sweeps (
    movie_id   TEXT PRIMARY KEY,
    checked_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 扫的时候按「扫得最久的优先」，这个索引给它用。
CREATE INDEX IF NOT EXISTS idx_jav_magnet_sweeps_at ON jav_magnet_sweeps(checked_at);
