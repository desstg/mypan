-- 媒体类型：决定 STRM 生成之后要不要按番号的规矩补 nfo 与图片。
--
--   tmdb（默认）：行为与加这一列之前**逐字相同** —— 只生成 .strm，元数据按扩展名同步。
--   jav        ：额外把侧车 json 同步到本地，再读本地那份生成 nfo / 封面 / 剧照。
--
-- 老库里的任务全部落在默认值 'tmdb' 上，所以升级不会有任何行为变化 ——
-- 这正是把默认值写死成 'tmdb' 而不是留空串的原因（空串还要在 Go 侧兜一次，
-- 而兜底一旦漏在某个入口上，一个番号任务会被悄悄当成 tmdb 任务跑）。
ALTER TABLE strm_tasks ADD COLUMN media_kind TEXT NOT NULL DEFAULT 'tmdb';
