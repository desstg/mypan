-- 清掉「同一颗资源失败后反复重试」攒出来的重复推送记录。
--
-- 背景：同一颗磁链投递失败后会重试，幂等键不变、attempt 也复用（这是对的），
-- 但**推送记录以前每次重试都是新插一行** —— 实测订阅 4 里那颗 dff1198e…
-- 一条 attempt 重试 10 次，留下了 11 条一模一样的 failed 记录；订阅 2 里
-- 那颗 DCC6F8EA… 留了 12 条。下载记录页看上去就成了「同一部片下载了好多部」。
--
-- 代码里已经改成「重试复用同一条记录」（见 internal/jav/push.go 的 savePushRecord），
-- 这一条只是把**已经攒在库里的**那些清掉。
--
-- 两条保护，都必要：
--   1. **只删 failed**。pending（在途）和 pushed（真下到网盘了）是事实，不能删；
--   2. **被 attempt 引用着的不删**。离线完成事件是拿 attempt.push_record_id 回写
--      状态的，删掉它指向的那一行，那次投递的完成结果就没地方落了。
--      每组的「当前那一条」正是被引用的那条，所以保留它就够了。
--
-- 只动那些「同一颗磁链 + 同一订阅」之下不止一行的组；单独的失败记录原样留着
-- （那是「试过一次、没成」，是有效信息）。
DELETE FROM jav_push_records
 WHERE status = 'failed'
   AND id NOT IN (SELECT push_record_id FROM jav_subscription_push_attempts WHERE push_record_id > 0)
   AND (magnet, subscription_id) IN (
         SELECT magnet, subscription_id FROM jav_push_records
          GROUP BY magnet, subscription_id
         HAVING COUNT(*) > 1
       );
