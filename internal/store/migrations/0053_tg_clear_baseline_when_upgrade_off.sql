-- 清掉「关着洗版」的订阅上那份**用不着的洗版基线**。
--
-- 背景（2026-10-10 真机）：用户报「我没开洗版，一样会洗版」。查到三处：
--   ① isUpgradeCandidate 把「整季包」与「这一集还没收到」混成同一条放行分支；
--   ② pusher.go 的 upgraded 只看 `BestQualityScore > 0`，不读开关；
--   ③ MarkPushed 无条件抬 best_quality_score。
-- ①②③ 已在代码里修掉（分别见 handler.go / pusher.go / store 的 MarkPushed）。
-- 这一条补的是**存量数据**。
--
-- 为什么清 0 是安全的：best_quality_score 的唯一用途是给洗版当门槛，而洗版关着时
-- 它不拦任何东西（isUpgradeCandidate 走到开关那一句就 return false 了）。留着它
-- 反而有害 —— 用户哪天打开洗版，第一轮就会拿这份**关着时攒下的**基线去拦候选，
-- 把本来就该推的挡在门外。清 0 只会「解锁」，绝不会「拦住」。
--
-- 真机实测：43 条订阅里 22 条 upgrade_enabled=0，其中 21 条带着非零基线
-- （最高 78.3，来自当初推的整季包，而那次推送本身还失败了 —— UnmarkPushed
-- 会退回 pushed_count 但**不回退基线**）。
UPDATE tg_subscriptions SET best_quality_score = 0 WHERE upgrade_enabled = 0;

-- 把「关着洗版却标成洗版升级」的历史记录改回 pushed。
--
-- 判据只看订阅当前的开关：关着洗版的订阅**不可能**产生真正的洗版（代码里
-- isUpgradePush 现在要求 UpgradeEnabled 为真）。这些记录的 reason 里还写着
-- 「洗版升级：画质分 50 超过此前最好版本 78.3」这种与事实相反的话，一并清掉。
--
-- ⚠️ 只改状态与那句后缀，不动其他 reason 正文 —— 那些是「自动网盘搜索命中（…）」
-- 之类真实的信息，删了用户就看不出这条是怎么来的了。
UPDATE tg_match_records
SET status = 'pushed',
    reason = replace(reason, '；洗版升级：画质分', '（旧记录：曾误标为洗版）｜画质分')
WHERE status = 'upgraded'
  AND subscription_id IN (SELECT id FROM tg_subscriptions WHERE upgrade_enabled = 0);
