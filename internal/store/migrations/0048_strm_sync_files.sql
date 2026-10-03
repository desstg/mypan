-- 把原来那一个 sync_metadata 开关拆成两个：
--
--   sync_metadata（原列，语义收窄）= 「刮削元数据」：STRM 同步完之后要不要刮削。
--     番号影片：开 = 下 json 侧车 + 字幕/nfo/图片，并生成 nfo / 封面 / 剧照；
--               关 = 只出 strm（json 照下）。
--     tmdb 影片：开 = 扫描有新增/更新时自动排一次刮削；关 = 不刮。
--
--   sync_files（新列）= 「同步元数据」：要不要把网盘上的元数据小文件
--     （字幕/nfo/图片，按全局设置 strm_metadata_extensions 的扩展名）同步到本地。
--     默认关（新任务的默认值由前端表单给出，与这一列的 DEFAULT 一致）。
--     番号影片的 json 侧车**不受这一列控制** —— 它是刮削的输入，只要媒体类型是
--     番号就一定同步（见 internal/strm 的 taskMetaExtensions）。
--
-- 为什么新列默认 0 而不是 1：这一列是新概念，历史任务里没有对应信息，
-- 只能靠下面那条 UPDATE 从旧列搬过来。DEFAULT 只服务于「将来直接 INSERT 的行」。
--
-- 为什么 sync_metadata 的 DB 默认值仍是 0（没有一起改）：SQLite 不支持
-- ALTER COLUMN ... SET DEFAULT。Go 侧的 Create/Update 每次都会显式写这一列，
-- 所以默认值只在「绕过仓储直接 INSERT」时才有影响，不值得为它重建表。
ALTER TABLE strm_tasks ADD COLUMN sync_files INTEGER NOT NULL DEFAULT 0;

-- 老任务：旧开关的旧语义 = 「同步元数据」（下楼盘小文件），原样搬进新列，
-- 保证升级后小文件同步行为不变（否则老用户会发现字幕/nfo 静默不再同步）。
UPDATE strm_tasks SET sync_files = sync_metadata;

-- 再把「刮削」一律置为开：用户确认老任务也改成开，与前端新任务的默认值一致。
UPDATE strm_tasks SET sync_metadata = 1;
