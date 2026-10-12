-- 「搜索转存」页的转存记录。
--
-- 记的是「用户从搜索页或转存页提交过一次转存」这件事，两种来源共用一张表：
--   source = search（搜索结果行上的「转存」按钮）
--   source = manual（转存 tab 里手工粘贴的链接）
--
-- 为什么不复用 offline_download_tasks：那张表只覆盖「走离线下载」的那一半，
-- 115 分享转存**不产生离线任务**（它同步落盘）。而且它会被任务列表频繁改写
-- （进度、速度、临时路径），而这一页要的是「提交那一刻的留档」——
-- 链接原文、提取码、提交到哪个目录，这些在离线任务里都不全。
--
-- 状态与 offline_download_tasks **刻意不共用一套**：这一页要的是用户视角的
-- 四态（处理中/保存成功/保存失败，另留两个秒传态给将来的秒传通道），
-- 而那张表的状态是网盘任务的细粒度状态。映射在 internal/searchtransfer/records.go。
CREATE TABLE IF NOT EXISTS transfer_records (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    -- kind 是资源类型：magnet / ed2k / http / ftp / thunder / share_115。
    -- 与 tgsubscribe 的 ResourceKind 同名字符串，但**不是同一份常量** ——
    -- 那张表里的值域由投递器决定，这里由「转存记录」这一页要展示什么决定。
    kind                TEXT NOT NULL DEFAULT '',
    link                TEXT NOT NULL DEFAULT '',
    password            TEXT NOT NULL DEFAULT '',
    -- share_name 是分享标题 / 资源名；file_name 是转存进来的文件名。
    -- 离线下载那条路只有前者（网盘还没下完，谈不上文件名）。
    share_name          TEXT NOT NULL DEFAULT '',
    file_id             TEXT NOT NULL DEFAULT '',
    file_name           TEXT NOT NULL DEFAULT '',
    -- status: pending / success / failed / rapid_success / rapid_failed
    status              TEXT NOT NULL DEFAULT 'pending',
    note                TEXT NOT NULL DEFAULT '',
    -- source: search / manual
    source              TEXT NOT NULL DEFAULT '',
    account_id          INTEGER NOT NULL DEFAULT 0,
    account_name        TEXT NOT NULL DEFAULT '',
    -- task_id 关联 offline_download_tasks.task_id；分享转存为空。
    -- 靠它在列表加载时把 pending 的记录对账成真实结果。
    task_id             TEXT NOT NULL DEFAULT '',
    target_display_path TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 列表默认按时间倒序翻页。
CREATE INDEX IF NOT EXISTS idx_transfer_records_created ON transfer_records(created_at DESC);
-- 按状态/类型筛选。
CREATE INDEX IF NOT EXISTS idx_transfer_records_status ON transfer_records(status);
CREATE INDEX IF NOT EXISTS idx_transfer_records_kind ON transfer_records(kind);
-- 对账要按 task_id 反查（见 records.go 的 reconcile）。
CREATE INDEX IF NOT EXISTS idx_transfer_records_task ON transfer_records(task_id);
