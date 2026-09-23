-- 候选与推送记录上的**来源**标记。'' = JAVBUS 磁力（默认，老数据也落这里）；
-- 'comment' = 影片评论区里用户分享的链接（见 internal/jav/commentlink）。
--
-- 候选表为什么也要存：候选是**检查那一刻**的产物，之后推送时是按 id / 指纹重新
-- 读出来的（push.go 的 pickCandidate），评论链接与 JAVBUS 磁力在那个形状上
-- 完全无法区分。现算「这条 URI 是不是某条评论里的链接」要重读评论 + 重跑正则，
-- 而且评论会被重新抓取覆盖 —— 历史候选的含义会跟着变。
--
-- 推送记录为什么**再存一份**（而不是 join 候选表推出来）：
--   1. DeleteSubscription 会级联删掉 jav_subscription_candidates，但**不删**
--      jav_push_records（记录是历史）—— 靠 join 推的话，删掉订阅之后那批记录的
--      来源就永远查不出来了；
--   2. 手动推送（subscription_id = 0）压根没有候选行。
-- 冗余一个词换「记录永远自解释」，值。
--
-- jav_push_records 的 Update 不写这一列：重试复用的是同一条记录、同一颗候选，
-- 来源不会变，写它只是多一次无意义的覆盖。
ALTER TABLE jav_subscription_candidates ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE jav_push_records ADD COLUMN source TEXT NOT NULL DEFAULT '';
