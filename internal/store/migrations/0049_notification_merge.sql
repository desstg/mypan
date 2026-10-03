-- 通知按「同键」合并：同一个 (category, ref_id, title, message) 在**未读期间**
-- 重复出现时不再新增行，而是把已有那行 count+1 并把时间推到最近一次。
--
-- 起因（2026-10-03 真机）：番号任务的「STRM 扫描安全保护阻止清理」每 6 小时发一条，
-- 内容一字不差，两天就堆了 8 条，把通知列表整个刷满 —— 用户看到的是一屏重复，
-- 真正要处理的那条被淹在里面。
--
-- 两条刻意的规则（见 internal/store/notification_repo.go 的 CreateOrMerge）：
--   * **只在未读行上合并**。读过之后再发生 = 新消息，必须重新亮红点；
--     否则「读掉一条反复发生的告警」就等于把它永久静音了。
--   * **message 参与判重**。内容变了就是新情况（例如待删目录从 28 变成 30），
--     要单独出一条，不能被合并成「同一个问题的第 N 次」而丢掉变化本身。
--
-- count 默认 1：既有行与将来的新行语义一致，前端只在 count>1 时显示次数。
ALTER TABLE notifications ADD COLUMN count INTEGER NOT NULL DEFAULT 1;

-- first_at 记第一次发生的时间。created_at 在合并时会被推到最近一次（列表按它倒序，
-- 反复发生的告警要一直浮在顶部），所以「最早是什么时候开始的」必须另存一列。
ALTER TABLE notifications ADD COLUMN first_at TIMESTAMP;

UPDATE notifications SET first_at = created_at WHERE first_at IS NULL;

-- 存量数据就地合并一遍：老库里的重复行（例如那 8 条一模一样的保护告警）
-- 升级后不该继续各占一行 —— 否则「合并」只对新产生的通知生效，
-- 用户第一眼看到的还是一屏重复。
--
-- 只并**未读**的，与上面的运行时规则一致。保留同键里 id 最大的那一行
-- （最新的一条），count 累加为同键行数，first_at 取最早，created_at 取最晚。
--
-- 两处 `strftime` 不是装饰：MIN/MAX 的结果经驱动往返会变成 Go 的 time.Time，
-- 再写回去就成了 RFC3339（`2026-10-02T13:11:18Z`），与库里其余时间列的
-- `YYYY-MM-DD HH:MM:SS` 不一致。parseTS 两种都认，所以不影响读取，
-- 但同一列里混两种格式迟早会坑到别的比较（`created_at > ?` 这类字符串比较）。
UPDATE notifications
   SET count = (
           SELECT COUNT(*) FROM notifications x
            WHERE x.is_read = 0 AND x.category = notifications.category
              AND x.ref_id = notifications.ref_id AND x.title = notifications.title
              AND x.message = notifications.message),
       first_at = (
           SELECT strftime('%Y-%m-%d %H:%M:%S', MIN(COALESCE(x.first_at, x.created_at)))
             FROM notifications x
            WHERE x.is_read = 0 AND x.category = notifications.category
              AND x.ref_id = notifications.ref_id AND x.title = notifications.title
              AND x.message = notifications.message),
       created_at = (
           SELECT strftime('%Y-%m-%d %H:%M:%S', MAX(x.created_at))
             FROM notifications x
            WHERE x.is_read = 0 AND x.category = notifications.category
              AND x.ref_id = notifications.ref_id AND x.title = notifications.title
              AND x.message = notifications.message)
 WHERE is_read = 0
   AND id IN (
       SELECT MAX(id) FROM notifications
        WHERE is_read = 0
        GROUP BY category, ref_id, title, message
        HAVING COUNT(*) > 1);

DELETE FROM notifications
 WHERE is_read = 0
   AND id NOT IN (
       SELECT MAX(id) FROM notifications
        WHERE is_read = 0
        GROUP BY category, ref_id, title, message);
