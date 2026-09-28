-- 0046：清掉被 airav 的「标题」冒充过的那批简介，让它们被真正的简介源重问一遍。
--
-- 背景（2026-09-27 当天发现并回滚的一个错）：airav 那个源给的是**一行中文标题**
-- （`多层次传销之女：case69`、14~30 字），我一开始把它当简介写进了 `summary`。
-- 它排在简介链的第一位，而 `synopsis.Enrich` 是「首个非空胜」，于是**把 missav
-- 那段 136 字的真简介挡掉了** —— 用户看到的就是「获取到的是标题，不是简介」。
--
-- 代码侧已改（airav 撤出简介链，只作为中文标题来源）。但库里那批已经被写进去的
-- 行还留着，得清掉才会被重新问：
--   - 来源是 airav 的（那就是这一批）；
--   - 或者简介短得明显不像简介的（**兜底**，万一当时是别的路径写进去的）。
--
-- 那批 airav 标题**没有丢**：它们本来就是从 `title_zh` 那条链来的，而 0045 之后
-- 那一列会重新补上（如果确实要留中文标题的话）。
--
-- ⚠️ 阈值 40 字是**按真库量出来的**：现有正经简介最短 60 字（jav321）、109 字
-- （caribbeancom），而 airav 那批最长 32 字 —— 中间留了段安全距离，不会误伤。

UPDATE jav_movies
   SET summary           = '',
       summary_source    = '',
       enriched_at       = NULL,
       summary_attempts  = 0,
       updated_at        = CURRENT_TIMESTAMP
 WHERE (summary_source = 'airav'
        OR (summary <> '' AND length(summary) < 40))
   AND summary IS NOT NULL;
