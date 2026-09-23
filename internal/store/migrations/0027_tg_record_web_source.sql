-- 让「没有对应 TG 消息」的资源也能落库。
--
-- 背景：新增了第二个资源来源 —— 按订阅片名去网盘搜索引擎搜磁力（见
-- internal/tgsubscribe/websearch.go）。这类资源不属于任何频道消息，
-- channel_id 与 message_id 都是 0。
--
-- 而 idx_tg_rec_msg_magnet 的语义是「**同一条消息里的**同一资源幂等」
-- （offset 回退、崩溃重放、跨频道转发都靠它）。对它来说 (0, 0, hash) 是合法的，
-- 于是**同一个磁力全局只能落一条记录**：订阅 A 搜到它之后，订阅 B 再搜到同一个
-- 磁力就会被静默丢弃，而 B 的匹配历史里什么都看不到、也就无从推送。
--
-- 改成部分索引：只约束真正的消息来源。无消息来源的记录交由
-- idx_tg_rec_sub_magnet(subscription_id, magnet_hash) 约束 —— 那才是
-- 「同一条订阅不重复收同一份资源」这条规则真正该落的地方。
--
-- ⚠️ 现有行全部来自 TG 抓取（message_id > 0），加 WHERE 后索引覆盖范围不变，
-- 不存在漏约束的旧数据。
DROP INDEX IF EXISTS idx_tg_rec_msg_magnet;
CREATE UNIQUE INDEX IF NOT EXISTS idx_tg_rec_msg_magnet
    ON tg_match_records(channel_id, message_id, magnet_hash) WHERE message_id > 0;
