-- 代理设置从「媒体整理」与「TG 影片订阅」各一套，收敛成「系统设置 → 其他设置」里的
-- 一个全局代理（proxy_*），见 internal/settings/proxy.go。
--
-- 规则：**以 mo_ 那套为主**。mo_ 配了地址就整套用 mo_ 的（地址 + 认证 + 开关）；
-- mo_ 没地址才整套退到 tg_ 那套。整套地选，而不是逐字段取非空 ——
-- 逐字段合并会把两个不同出口的地址和密码拼在一起，拼出一个连不上的代理。
--
-- configs 表是 key TEXT PRIMARY KEY，所以 INSERT OR REPLACE 正好是「有则覆盖、无则插入」。
--
-- 时序安全：internal/app/wire_store.go 先 db.Migrate 再 settings.New，
-- settings 加载时脏值已经删干净了。

INSERT OR REPLACE INTO configs (key, value)
SELECT 'proxy_url', CASE
    WHEN COALESCE((SELECT TRIM(value) FROM configs WHERE key = 'mo_proxy_url'), '') <> ''
        THEN (SELECT value FROM configs WHERE key = 'mo_proxy_url')
    ELSE COALESCE((SELECT value FROM configs WHERE key = 'tg_bot_proxy_url'), '')
END;

INSERT OR REPLACE INTO configs (key, value)
SELECT 'proxy_username', CASE
    WHEN COALESCE((SELECT TRIM(value) FROM configs WHERE key = 'mo_proxy_url'), '') <> ''
        THEN COALESCE((SELECT value FROM configs WHERE key = 'mo_proxy_username'), '')
    ELSE COALESCE((SELECT value FROM configs WHERE key = 'tg_bot_proxy_username'), '')
END;

INSERT OR REPLACE INTO configs (key, value)
SELECT 'proxy_password', CASE
    WHEN COALESCE((SELECT TRIM(value) FROM configs WHERE key = 'mo_proxy_url'), '') <> ''
        THEN COALESCE((SELECT value FROM configs WHERE key = 'mo_proxy_password'), '')
    ELSE COALESCE((SELECT value FROM configs WHERE key = 'tg_bot_proxy_password'), '')
END;

INSERT OR REPLACE INTO configs (key, value)
SELECT 'proxy_enabled', CASE
    WHEN COALESCE((SELECT TRIM(value) FROM configs WHERE key = 'mo_proxy_url'), '') <> ''
        THEN COALESCE((SELECT value FROM configs WHERE key = 'mo_proxy_enabled'), 'false')
    ELSE COALESCE((SELECT value FROM configs WHERE key = 'tg_bot_proxy_enabled'), 'false')
END;

-- 兜底：没有地址就不许开着。settings.ProxyURL 遇到空地址直接返回空串并静默直连，
-- 用户却以为自己挂了代理 —— 这种悬空状态要压掉。
UPDATE configs SET value = 'false'
 WHERE key = 'proxy_enabled'
   AND value = 'true'
   AND TRIM(COALESCE((SELECT value FROM configs WHERE key = 'proxy_url'), '')) = '';

DELETE FROM configs WHERE key IN (
    'mo_proxy_enabled',
    'mo_proxy_url',
    'mo_proxy_username',
    'mo_proxy_password',
    'tg_bot_proxy_enabled',
    'tg_bot_proxy_url',
    'tg_bot_proxy_username',
    'tg_bot_proxy_password'
);
