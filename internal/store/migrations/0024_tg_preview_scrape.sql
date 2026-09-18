-- TG 影片订阅已从 Bot API 长轮询改为抓 t.me 公开网页预览（见 internal/tgsubscribe/preview），
-- 不再需要任何凭据，所以这几个键整体作废。
--
-- 从设置注册表里移除只是让它们读不到，值仍然留在磁盘上；而 tg_bot_token 是一个
-- 可用的 Bot 凭据，不该跟着数据库一起被打包、备份、发出去。所以这里物理删掉。
--
-- 时序是安全的：wire_store.go 先 db.Migrate 再 settings.New，加载时脏值已经没了。
DELETE FROM configs WHERE key IN (
    'tg_bot_token',
    'tg_bot_api_host',
    'tg_bot_update_offset',
    'tg_bot_name'
);
